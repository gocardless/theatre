/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

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
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	rbacv1alpha1 "github.com/gocardless/theatre/apis/rbac/v1alpha1"
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

// DirectoryRoleBindingReconciler reconciles a DirectoryRoleBinding object
type DirectoryRoleBindingReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme

	ctx             context.Context
	provider        DirectoryProvider
	refreshInterval time.Duration
}

// +kubebuilder:rbac:groups=rbac.crd.gocardless.com,resources=directoryrolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.crd.gocardless.com,resources=directoryrolebindings/status,verbs=get;update;patch

func (r *DirectoryRoleBindingReconciler) ReconcileObject(logger logr.Logger, req ctrl.Request, drb *rbacv1alpha1.DirectoryRoleBinding) (ctrl.Result, error) {
	var err error
	rb := &rbacv1.RoleBinding{}
	identifier := types.NamespacedName{Name: drb.Name, Namespace: drb.Namespace}
	err = r.Get(r.ctx, identifier, rb)
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

		if err := controllerutil.SetControllerReference(drb, rb, scheme.Scheme); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to set controller reference: %w", err)
		}

		if err = r.Create(r.ctx, rb); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to create RoleBinding: %w", err)
		}

		r.Log.Info("event", EventRoleBindingCreated, "msg", fmt.Sprintf(
			"Created RoleBinding: %s", identifier,
		))
	}

	subjects, err := r.resolve(drb.Spec.Subjects)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("failed to resolve subjects: %w", err)
	}

	add, remove := rbacutils.Diff(subjects, rb.Subjects), rbacutils.Diff(rb.Subjects, subjects)
	if len(add) > 0 || len(remove) > 0 {
		r.Log.Info("event", EventSubjectsModified, "add", len(add), "remove", len(remove), "msg", fmt.Sprintf(
			"Modifying subject list, adding %d and removing %d", len(add), len(remove),
		))

		for _, member := range add {
			r.Log.Info("event", EventSubjectAdd, "subject", member.Name)
		}

		for _, member := range remove {
			r.Log.Info("event", EventSubjectRemove, "subject", member.Name)
		}

		rb.Subjects = subjects
		if err := r.Update(r.ctx, rb); err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to update RoleBinding: %w", err)
		}
	}

	return reconcile.Result{RequeueAfter: r.refreshInterval}, nil
}

func (r *DirectoryRoleBindingReconciler) SetupWithManager(ctx context.Context, mgr manager.Manager, provider DirectoryProvider, refreshInterval time.Duration) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&rbacv1alpha1.DirectoryRoleBinding{}).
		Watches(
			&source.Kind{Type: &rbacv1.RoleBinding{}},
			&handler.EnqueueRequestForOwner{
				IsController: true,
				OwnerType:    &rbacv1alpha1.DirectoryRoleBinding{},
			},
		).
		Complete(
			recutil.ResolveAndReconcile(
				ctx, r.Log, mgr, &rbacv1alpha1.DirectoryRoleBinding{},
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

func (r *DirectoryRoleBindingReconciler) membersOf(directory Directory, group string) ([]rbacv1.Subject, error) {
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
