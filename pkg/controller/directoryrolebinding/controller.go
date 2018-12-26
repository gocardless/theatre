package directoryrolebinding

import (
	"context"
	"fmt"

	kitlog "github.com/go-kit/kit/log"

	admin "google.golang.org/api/admin/directory/v1"

	corev1 "k8s.io/api/core/v1"
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
)

const (
	EventCreated          = "Created"
	EventSubjectsModified = "SubjectsModified"
)

func NewController(ctx context.Context, logger kitlog.Logger, recorder record.EventRecorder, client client.Client, adminClient *admin.Service) *Controller {
	return &Controller{
		ctx:         ctx,
		logger:      logger,
		recorder:    recorder,
		client:      client,
		adminClient: adminClient,
	}
}

type Controller struct {
	ctx         context.Context
	logger      kitlog.Logger
	recorder    record.EventRecorder
	client      client.Client
	adminClient *admin.Service
}

// Reconcile achieves the declarative state defined by DirectoryRoleBinding resources.
func (r *Controller) Reconcile(request reconcile.Request) (res reconcile.Result, err error) {
	logger := kitlog.With(r.logger, "request", request)
	logger.Log("event", "reconcile.start")

	defer func() {
		if err != nil {
			logger.Log("event", "reconcile.error", "error", err)
		}
	}()

	drb := &rbacv1alpha1.DirectoryRoleBinding{}
	if err := r.client.Get(r.ctx, request.NamespacedName, drb); err != nil {
		if errors.IsNotFound(err) {
			r.logger.Log("event", "reconcile.not_found")
			return res, nil
		}

		r.logger.Log("event", "reconcile.error", "error", err)
		return res, err
	}

	rb := &rbacv1.RoleBinding{}
	identifier := types.NamespacedName{Name: drb.Name, Namespace: drb.Namespace}
	err = r.client.Get(r.ctx, identifier, rb)
	if err != nil && errors.IsNotFound(err) {
		logger.Log("event", "reconcile.create", "msg", "no RoleBinding found, creating")

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

		r.recorder.Event(drb, corev1.EventTypeNormal, EventCreated, fmt.Sprintf(
			"Created RoleBinding: %s", identifier,
		))
	}

	subjects, err := r.resolve(drb.Subjects)
	if err != nil {
		return reconcile.Result{}, err
	}

	add, remove := diff(subjects, rb.Subjects), diff(rb.Subjects, subjects)
	if len(add) > 0 || len(remove) > 0 {
		r.recorder.Event(drb, corev1.EventTypeNormal, EventSubjectsModified, fmt.Sprintf(
			"Modifying subject list, adding %d and removing %d", len(add), len(remove),
		))

		for _, member := range add {
			logger.Log("event", "member.add", "member", member.Name)
		}

		for _, member := range remove {
			logger.Log("event", "member.remove", "member", member.Name)
		}

		logger.Log("event", "reconcile.update", "msg", "updating RoleBinding subjects")
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

func (r *Controller) membersOf(group string) ([]rbacv1.Subject, error) {
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

func (r *Controller) resolve(in []rbacv1.Subject) ([]rbacv1.Subject, error) {
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
