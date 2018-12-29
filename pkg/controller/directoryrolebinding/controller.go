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
	"k8s.io/client-go/tools/record"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	rbacv1alpha1 "github.com/lawrencejones/theatre/pkg/apis/rbac/v1alpha1"
	"github.com/lawrencejones/theatre/pkg/controlflow"
	"github.com/lawrencejones/theatre/pkg/logging"
	"github.com/lawrencejones/theatre/pkg/rbacutils"
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

// Add instantiates a DirectoryRoleBinding controller and adds it to the manager.
func Add(ctx context.Context, mgr manager.Manager, logger kitlog.Logger, client client.Client, adminClient *admin.Service) (controller.Controller, error) {
	c, err := controller.New("directoryrolebinding-controller", mgr,
		controller.Options{
			Reconciler: &DirectoryRoleBindingReconciler{
				ctx:         ctx,
				logger:      kitlog.With(logger, "component", "DirectoryRoleBinding"),
				recorder:    mgr.GetRecorder("DirectoryRoleBinding"),
				client:      client,
				adminClient: adminClient,
			},
		},
	)

	err = controlflow.All(
		func() error {
			return c.Watch(
				&source.Kind{Type: &rbacv1alpha1.DirectoryRoleBinding{}}, &handler.EnqueueRequestForObject{},
			)
		},
		func() error {
			return c.Watch(
				&source.Kind{Type: &rbacv1.RoleBinding{}}, &handler.EnqueueRequestForOwner{
					IsController: true,
					OwnerType:    &rbacv1alpha1.DirectoryRoleBinding{},
				},
			)
		},
	)

	return c, err
}

type DirectoryRoleBindingReconciler struct {
	ctx         context.Context
	logger      kitlog.Logger
	recorder    record.EventRecorder
	client      client.Client
	adminClient *admin.Service
}

// Reconcile achieves the declarative state defined by DirectoryRoleBinding resources.
func (r *DirectoryRoleBindingReconciler) Reconcile(request reconcile.Request) (res reconcile.Result, err error) {
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
		rb := &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      drb.Name,
				Namespace: drb.Namespace,
			},
			RoleRef:  drb.Spec.RoleBinding.RoleRef,
			Subjects: []rbacv1.Subject{},
		}

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

	subjects, err := r.resolve(drb.Spec.RoleBinding.Subjects)
	if err != nil {
		return reconcile.Result{}, err
	}

	add, remove := rbacutils.Diff(subjects, rb.Subjects), rbacutils.Diff(rb.Subjects, subjects)
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

func (r *DirectoryRoleBindingReconciler) membersOf(group string) ([]rbacv1.Subject, error) {
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

func (r *DirectoryRoleBindingReconciler) resolve(in []rbacv1.Subject) ([]rbacv1.Subject, error) {
	out := make([]rbacv1.Subject, 0)
	for _, subject := range in {
		switch subject.Kind {
		case rbacv1alpha1.GoogleGroupKind:
			members, err := r.membersOf(subject.Name)
			if err != nil {
				return nil, err
			}

			// For each of our group members, add them if they weren't already here
			for _, member := range members {
				if !rbacutils.IncludesSubject(out, member) {
					out = append(out, member)
				}
			}

		default:
			out = append(out, subject)
		}
	}

	return out, nil
}
