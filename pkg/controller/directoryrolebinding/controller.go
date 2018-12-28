package directoryrolebinding

import (
	"context"
	"fmt"

	kitlog "github.com/go-kit/kit/log"

	admin "google.golang.org/api/admin/directory/v1"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp" // this is required to auth against GCP
	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rbacv1alpha1 "github.com/lawrencejones/theatre/pkg/apis/rbac/v1alpha1"
	"github.com/lawrencejones/theatre/pkg/logging"
)

const (
	EventReconcile          = "Reconcile"
	EventNotFound           = "NotFound"
	EventRoleBindingCreated = "Created"
	EventError              = "Error"
	EventSubjectAdd         = "SubjectAdd"
	EventSubjectRemove      = "SubjectRemove"
	EventSubjectsModified   = "SubjectsModified"
)

func NewDirectoryRoleBindingController(ctx context.Context, logger kitlog.Logger, recorder record.EventRecorder, client client.Client, adminClient *admin.Service) *DirectoryRoleBindingController {
	return &DirectoryRoleBindingController{
		ctx:         ctx,
		logger:      logger,
		recorder:    recorder,
		client:      client,
		adminClient: adminClient,
	}
}

type DirectoryRoleBindingController struct {
	ctx         context.Context
	logger      kitlog.Logger
	recorder    record.EventRecorder
	client      client.Client
	adminClient *admin.Service
}

// Reconcile achieves the declarative state defined by DirectoryRoleBinding resources.
func (r *DirectoryRoleBindingController) Reconcile(request reconcile.Request) (res reconcile.Result, err error) {
	logger := kitlog.With(r.logger, "request", request)
	logger.Log("event", EventReconcile)

	drb := &rbacv1alpha1.DirectoryRoleBinding{}
	if err := r.client.Get(r.ctx, request.NamespacedName, drb); err != nil {
		if errors.IsNotFound(err) {
			return res, logger.Log("event", EventNotFound)
		}

		logger.Log("event", EventError, "error", err)
		return res, err
	}

	logger = logging.WithRecorder(logger, r.recorder, drb)

	defer func() {
		if err != nil {
			logger.Log("event", EventError, "error", err)
		}
	}()

	rb := &rbacv1.RoleBinding{}
	identifier := types.NamespacedName{Name: drb.Name, Namespace: drb.Namespace}
	err = r.client.Get(r.ctx, identifier, rb)
	if err != nil && errors.IsNotFound(err) {
		rb.ObjectMeta = metav1.ObjectMeta{
			Name:      drb.Name,
			Namespace: drb.Namespace,
		}
		rb.RoleRef = drb.RoleRef
		rb.Subjects = []rbacv1.Subject{}

		if err := controllerutil.SetControllerReference(drb, rb, scheme.Scheme); err != nil {
			return reconcile.Result{}, err
		}

		if err = r.client.Create(r.ctx, rb); err != nil {
			return reconcile.Result{}, err
		}

		if err = r.client.Get(r.ctx, identifier, rb); err != nil {
			return reconcile.Result{}, err
		}

		logger.Log("event", EventRoleBindingCreated, "msg", fmt.Sprintf(
			"Created RoleBinding: %s", identifier,
		))
	}

	subjects, err := r.resolve(drb.Subjects)
	if err != nil {
		return reconcile.Result{}, err
	}

	add, remove := diff(subjects, rb.Subjects), diff(rb.Subjects, subjects)
	if len(add) > 0 || len(remove) > 0 {
		logger.Log("event", EventSubjectsModified, "add", len(add), "remove", len(remove), "msg", fmt.Sprintf(
			"Modifying subject list, adding %d and removing %d", len(add), len(remove),
		))

		for _, member := range add {
			logging.WithNoRecord(logger).Log("event", EventSubjectAdd, "subject", member.Name)
		}

		for _, member := range remove {
			logging.WithNoRecord(logger).Log("event", EventSubjectRemove, "subject", member.Name)
		}

		rb.Subjects = subjects
		if err := r.client.Update(r.ctx, rb); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func diff(s1 []rbacv1.Subject, s2 []rbacv1.Subject) []rbacv1.Subject {
	result := make([]rbacv1.Subject, 0)
	for _, s := range s1 {
		if !includesSubject(s2, s) {
			result = append(result, s)
		}
	}

	return result
}

func includesSubject(ss []rbacv1.Subject, s rbacv1.Subject) bool {
	for _, existing := range ss {
		if existing.Kind == s.Kind && existing.Name == s.Name && existing.Namespace == s.Namespace {
			return true
		}
	}

	return false
}

func (r *DirectoryRoleBindingController) membersOf(group string) ([]rbacv1.Subject, error) {
	subjects := make([]rbacv1.Subject, 0)
	resp, err := r.adminClient.Members.List(group).Do()

	if err == nil {
		for _, member := range resp.Members {
			subjects = append(subjects, rbacv1.Subject{
				APIGroup: rbacv1.GroupName,
				Kind:     rbacv1.UserKind,
				Name:     member.Email,
			})
		}
	}

	return subjects, err
}

func (r *DirectoryRoleBindingController) resolve(in []rbacv1.Subject) ([]rbacv1.Subject, error) {
	out := make([]rbacv1.Subject, 0)
	for _, subject := range in {
		switch subject.Kind {
		case "GoogleGroup":
			members, err := r.membersOf(subject.Name)
			if err != nil {
				return nil, err
			}

			// For each of our group members, add them if they weren't already here
			for _, member := range members {
				if !includesSubject(out, member) {
					out = append(out, member)
				}
			}

		default:
			out = append(out, subject)
		}
	}

	return out, nil
}
