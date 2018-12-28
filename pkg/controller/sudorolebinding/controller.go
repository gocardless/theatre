package sudorolebinding

import (
	"context"
	"fmt"

	kitlog "github.com/go-kit/kit/log"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
)

const (
	EventError          = "Error"
	EventReconcile      = "Reconcile"
	EventNotFound       = "NotFound"
	EventGrantsNotFound = "GrantsNotFound"
	EventGrantsCreated  = "GrantsCreated"
	EventSudoNotFound   = "SudoNotFound"
	EventSudoCreated    = "SudoCreated"
)

// Add instantiates a SudoRoleBinding controller and adds it to the manager.
func Add(ctx context.Context, mgr manager.Manager, logger kitlog.Logger, client client.Client) (controller.Controller, error) {
	c, err := controller.New("sudorolebinding-controller", mgr,
		controller.Options{
			Reconciler: &SudoRoleBindingReconciler{
				ctx:      ctx,
				logger:   kitlog.With(logger, "component", "SudoRoleBinding"),
				recorder: mgr.GetRecorder("SudoRoleBinding"),
				client:   client,
			},
		},
	)

	err = controlflow.All(
		func() error {
			return c.Watch(
				&source.Kind{Type: &rbacv1alpha1.SudoRoleBinding{}}, &handler.EnqueueRequestForObject{},
			)
		},
		func() error {
			return c.Watch(
				&source.Kind{Type: &rbacv1.RoleBinding{}}, &handler.EnqueueRequestForOwner{
					IsController: true,
					OwnerType:    &rbacv1alpha1.SudoRoleBinding{},
				},
			)
		},
	)

	return c, err
}

func NewSudoRoleBindingReconciler(ctx context.Context, logger kitlog.Logger, recorder record.EventRecorder, client client.Client) *SudoRoleBindingReconciler {
	return &SudoRoleBindingReconciler{
		ctx:      ctx,
		logger:   logger,
		recorder: recorder,
		client:   client,
	}
}

type SudoRoleBindingReconciler struct {
	ctx      context.Context
	logger   kitlog.Logger
	recorder record.EventRecorder
	client   client.Client
}

// Reconcile manages a SudoRoleBinding. SudoRoleBindings allow a specific set of users to
// temporarily elevate their permissions by adding binding themselves into a rolebinding.
//
// Each SudoRoleBinding creates two associated rolebindings, one for granting the elevated
// permissions and another to permit the users to elevate themselves.
func (r *SudoRoleBindingReconciler) Reconcile(request reconcile.Request) (res reconcile.Result, err error) {
	logger := kitlog.With(r.logger, "request", request)
	logger.Log("event", EventReconcile)

	defer func() {
		if err != nil {
			logger.Log("event", EventError, "error", err)
		}
	}()

	srb := &rbacv1alpha1.SudoRoleBinding{}
	if err := r.client.Get(r.ctx, request.NamespacedName, srb); err != nil {
		if errors.IsNotFound(err) {
			return res, logger.Log("event", EventNotFound)
		}

		return res, err
	}

	logger = logging.WithRecorder(logger, r.recorder, srb)

	_, err = r.findOrCreateGrants(logger, srb)
	if err != nil {
		return
	}

	// create sudo role binding
	// produce subject list, update sudo role binding

	return reconcile.Result{}, nil
}

// findOrCreate generates a Kubernetes resource if it doesn't already exist in the
// cluster. We set the object metadata to ensure the resource is owned by the
// SudoRoleBinding, and will get cleaned up whenever that resource is deleted.
func (r *SudoRoleBindingReconciler) findOrCreate(logger kitlog.Logger, srb *rbacv1alpha1.SudoRoleBinding, obj runtime.Object, eventNotFound, eventCreated string) (err error) {
	key, err := client.ObjectKeyFromObject(obj)
	if err != nil {
		return err
	}

	desired := obj.DeepCopyObject()
	if err = r.client.Get(r.ctx, key, obj); err != nil {
		if errors.IsNotFound(err) {
			logger.Log("event", eventNotFound)

			if err = controllerutil.SetControllerReference(srb, desired.(v1.Object), scheme.Scheme); err != nil {
				return
			}

			if err = r.client.Create(r.ctx, obj); err != nil {
				return
			}

			logger.Log("event", eventCreated, "msg", fmt.Sprintf(
				"Created %s: %s", desired.GetObjectKind(), key,
			))
		}
	}

	return nil
}

// findOrCreateGrants finds the rolebinding that powers the elevation of permissions to
// those defined in our SudoRoleBinding. It will initially have an empty set of subjects,
// as subjects should only be added to this role via the sudo action.
func (r *SudoRoleBindingReconciler) findOrCreateGrants(logger kitlog.Logger, srb *rbacv1alpha1.SudoRoleBinding) (rb *rbacv1.RoleBinding, err error) {
	identifier := types.NamespacedName{Name: fmt.Sprintf("%s-grants", srb.GetName()), Namespace: srb.GetNamespace()}
	if err = r.client.Get(r.ctx, identifier, rb); err != nil {
		if errors.IsNotFound(err) {
			logger.Log("event", EventGrantsNotFound)
			rb = &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{},
				RoleRef:    srb.Spec.RoleBinding.RoleRef,
				Subjects:   []rbacv1.Subject{},
			}

			if err = controllerutil.SetControllerReference(srb, rb, scheme.Scheme); err != nil {
				return
			}

			if err = r.client.Create(r.ctx, rb); err != nil {
				return
			}

			logger.Log("event", EventGrantsCreated, "msg", fmt.Sprintf(
				"Created grants RoleBinding: %s", identifier,
			))
		}
	}

	return
}

// findOrCreateSudo finds the directory role binding that permits the SudoRoleBinding
// subject list to perform the sudo action on our SudoRoleBinding.
func (r *SudoRoleBindingReconciler) findOrCreateSudo(logger kitlog.Logger, srb *rbacv1alpha1.SudoRoleBinding) (rb *rbacv1alpha1.DirectoryRoleBinding, err error) {
	identifier := types.NamespacedName{Name: fmt.Sprintf("%s-sudo", srb.GetName()), Namespace: srb.GetNamespace()}
	if err = r.client.Get(r.ctx, identifier, rb); err != nil {
		if errors.IsNotFound(err) {
			logger.Log("event", EventSudoNotFound)
			rb = &rbacv1alpha1.DirectoryRoleBinding{
				ObjectMeta: metav1.ObjectMeta{
					Name:      identifier.Name,
					Namespace: identifier.Namespace,
				},
				RoleRef:  rbacv1.RoleRef{},
				Subjects: []rbacv1.Subject{},
			}

			if err = controllerutil.SetControllerReference(srb, rb, scheme.Scheme); err != nil {
				return
			}

			if err = r.client.Create(r.ctx, rb); err != nil {
				return
			}

			logger.Log("event", EventGrantsCreated, "msg", fmt.Sprintf(
				"Created grants RoleBinding: %s", identifier,
			))
		}
	}

	return
}
