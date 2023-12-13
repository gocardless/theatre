package directoryrolebinding

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rbacv1alpha1 "github.com/gocardless/theatre/v4/apis/rbac/v1alpha1"
	rbacutils "github.com/gocardless/theatre/v4/pkg/rbac"
	"github.com/gocardless/theatre/v4/pkg/recutil"
)

const (
	EventRoleBindingCreated = "Created"
	EventError              = "Error"
	EventSubjectAdd         = "SubjectAdd"
	EventSubjectRemove      = "SubjectRemove"
	EventSubjectsModified   = "SubjectsModified"
)

// DirectoryRoleBindingReconciler reconciles a DirectoryRoleBinding object
type DirectoryRoleBindingReconciler struct {
	client.Client
	Ctx             context.Context
	Log             logr.Logger
	Provider        DirectoryProvider
	RefreshInterval time.Duration
	Scheme          *runtime.Scheme
}

func (r *DirectoryRoleBindingReconciler) ReconcileObject(logger logr.Logger, req ctrl.Request, drb *rbacv1alpha1.DirectoryRoleBinding) (ctrl.Result, error) {
	var err error
	rb := &rbacv1.RoleBinding{}
	identifier := types.NamespacedName{Name: drb.Name, Namespace: drb.Namespace}
	err = r.Get(r.Ctx, identifier, rb)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return reconcile.Result{}, fmt.Errorf("failed to get DirectoryRoleBinding: %w", err)
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

		if err := controllerutil.SetControllerReference(drb, rb, r.Scheme); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to set controller reference: %w", err)
		}

		if err = r.Create(r.Ctx, rb); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to create RoleBinding: %w", err)
		}

		r.Log.Info(
			fmt.Sprintf("Created RoleBinding: %s", identifier),
			"event", EventRoleBindingCreated,
		)
	}

	subjects, err := r.resolve(drb.Spec.Subjects)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to resolve subjects: %w", err)
	}

	add, remove := rbacutils.Diff(subjects, rb.Subjects), rbacutils.Diff(rb.Subjects, subjects)
	if len(add) > 0 || len(remove) > 0 {
		r.Log.Info(
			fmt.Sprintf(
				"Modifying subject list, adding %d and removing %d", len(add), len(remove),
			),
			"event", EventSubjectsModified, "add", len(add), "remove", len(remove),
		)

		for _, member := range add {
			r.Log.Info("adding subject", "event", EventSubjectAdd, "subject", member.Name)
		}

		for _, member := range remove {
			r.Log.Info("removing subject", "event", EventSubjectRemove, "subject", member.Name)
		}

		rb.Subjects = subjects
		if err := r.Update(r.Ctx, rb); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to update RoleBinding: %w", err)
		}
	}

	return reconcile.Result{RequeueAfter: r.RefreshInterval}, nil
}

func (r *DirectoryRoleBindingReconciler) SetupWithManager(mgr manager.Manager) error {
	logger := r.Log.WithValues("component", "DirectoryRoleBinding")
	return ctrl.NewControllerManagedBy(mgr).
		For(&rbacv1alpha1.DirectoryRoleBinding{}).
		Watches(
			&rbacv1.RoleBinding{},
			handler.EnqueueRequestForOwner(r.Scheme, mgr.GetRESTMapper(), &rbacv1alpha1.DirectoryRoleBinding{}, handler.OnlyControllerOwner()),
		).
		Complete(
			recutil.ResolveAndReconcile(
				r.Ctx, logger, mgr, &rbacv1alpha1.DirectoryRoleBinding{},
				func(logger logr.Logger, request reconcile.Request, obj runtime.Object) (reconcile.Result, error) {
					return r.ReconcileObject(logger, request, obj.(*rbacv1alpha1.DirectoryRoleBinding))
				},
			),
		)
}

// resolve expands the given subject list by using the directory provider. If our provider
// recognises the subject Kind then we attempt to resolve the members, otherwise we
// proceed assuming the subject is a native RBAC kind.
func (r *DirectoryRoleBindingReconciler) resolve(in []rbacv1.Subject) ([]rbacv1.Subject, error) {
	out := make([]rbacv1.Subject, 0)
	for _, subject := range in {
		directory := r.Provider.Get(subject.Kind)
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

func (r *DirectoryRoleBindingReconciler) membersOf(directory Directory, group string) ([]rbacv1.Subject, error) {
	subjects := make([]rbacv1.Subject, 0)
	members, err := directory.MembersOf(r.Ctx, group)

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
