package directoryrolebinding

import (
	"context"
	"fmt"
	"time"

	kitlog "github.com/go-kit/kit/log"
	"github.com/pkg/errors"

	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	rbacv1alpha1 "github.com/gocardless/theatre/pkg/apis/rbac/v1alpha1"
	"github.com/gocardless/theatre/pkg/logging"
	rbacutils "github.com/gocardless/theatre/pkg/rbac"
	"github.com/gocardless/theatre/pkg/recutil"
)

const (
	EventRoleBindingCreated = "Created"
	EventError              = "Error"
	EventSubjectAdd         = "SubjectAdd"
	EventSubjectRemove      = "SubjectRemove"
	EventSubjectsModified   = "SubjectsModified"
)

// Add instantiates a DirectoryRoleBinding controller and adds it to the manager. To
// ensure we respond to changes in the directory source, we provide a refreshInterval
// duration that tells the controller to re-enqueue a reconcile after each successful
// process. Setting this to 0 will disable the re-enqueue.
func Add(ctx context.Context, logger kitlog.Logger, mgr manager.Manager, provider DirectoryProvider, refreshInterval time.Duration, opts ...func(*controller.Options)) (controller.Controller, error) {
	logger = kitlog.With(logger, "component", "DirectoryRoleBinding")
	reconciler := &Reconciler{
		ctx:             ctx,
		client:          mgr.GetClient(),
		provider:        provider,
		refreshInterval: refreshInterval,
	}

	ctrlOptions := controller.Options{
		Reconciler: recutil.ResolveAndReconcile(
			ctx, logger, mgr, &rbacv1alpha1.DirectoryRoleBinding{},
			func(logger kitlog.Logger, request reconcile.Request, obj runtime.Object) (reconcile.Result, error) {
				return reconciler.ReconcileObject(logger, request, obj.(*rbacv1alpha1.DirectoryRoleBinding))
			},
		),
	}

	for _, opt := range opts {
		opt(&ctrlOptions)
	}

	ctrl, err := controller.New("directoryrolebinding-controller", mgr, ctrlOptions)
	if err != nil {
		return ctrl, err
	}

	err = ctrl.Watch(
		&source.Kind{Type: &rbacv1alpha1.DirectoryRoleBinding{}},
		&handler.EnqueueRequestForObject{},
	)

	if err != nil {
		return nil, err
	}

	err = ctrl.Watch(
		&source.Kind{Type: &rbacv1.RoleBinding{}},
		&handler.EnqueueRequestForOwner{
			IsController: true,
			OwnerType:    &rbacv1alpha1.DirectoryRoleBinding{},
		},
	)

	return ctrl, err
}

type Reconciler struct {
	ctx             context.Context
	client          client.Client
	provider        DirectoryProvider
	refreshInterval time.Duration
}

func (r *Reconciler) ReconcileObject(logger kitlog.Logger, request reconcile.Request, drb *rbacv1alpha1.DirectoryRoleBinding) (res reconcile.Result, err error) {
	rb := &rbacv1.RoleBinding{}
	identifier := types.NamespacedName{Name: drb.Name, Namespace: drb.Namespace}
	err = r.client.Get(r.ctx, identifier, rb)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, errors.Wrap(err, "failed to get DirectoryRoleBinding")
		}

		rb = &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      drb.Name,
				Namespace: drb.Namespace,
				Labels:    drb.Labels,
			},
			RoleRef:  drb.Spec.RoleRef,
			Subjects: []rbacv1.Subject{},
		}

		if err := controllerutil.SetControllerReference(drb, rb, scheme.Scheme); err != nil {
			return reconcile.Result{}, errors.Wrap(err, "failed to set controller reference")
		}

		if err = r.client.Create(r.ctx, rb); err != nil {
			return reconcile.Result{}, errors.Wrap(err, "failed to create RoleBinding")
		}

		logger.Log("event", EventRoleBindingCreated, "msg", fmt.Sprintf(
			"Created RoleBinding: %s", identifier,
		))
	}

	subjects, err := r.resolve(drb.Spec.Subjects)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "failed to resolve subjects")
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
			return reconcile.Result{}, errors.Wrap(err, "failed to update RoleBinding")
		}
	}

	return reconcile.Result{RequeueAfter: r.refreshInterval}, nil
}

// resolve expands the given subject list by using the directory provider. If our provider
// recognises the subject Kind then we attempt to resolve the members, otherwise we
// proceed assuming the subject is a native RBAC kind.
func (r *Reconciler) resolve(in []rbacv1.Subject) ([]rbacv1.Subject, error) {
	out := make([]rbacv1.Subject, 0)
	for _, subject := range in {
		directory := r.provider.Get(subject.Kind)
		if directory == nil {
			out = append(out, subject)
			continue // move onto the next subject
		}

		members, err := r.membersOf(directory, subject.Name)
		if err != nil {
			return nil, err
		}

		// For each of our group members, add them if they weren't already here
		for _, member := range members {
			if !rbacutils.IncludesSubject(out, member) {
				out = append(out, member)
			}
		}
	}

	return out, nil
}

func (r *Reconciler) membersOf(directory Directory, group string) ([]rbacv1.Subject, error) {
	subjects := make([]rbacv1.Subject, 0)
	members, err := directory.MembersOf(r.ctx, group)

	if err == nil {
		for _, member := range members {
			subjects = append(subjects, rbacv1.Subject{
				APIGroup: rbacv1.GroupName,
				Kind:     rbacv1.UserKind,
				Name:     member,
			})
		}
	}

	return subjects, err
}
