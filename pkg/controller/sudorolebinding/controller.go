package sudorolebinding

import (
	"context"
	"fmt"
	"reflect"
	"time"

	kitlog "github.com/go-kit/kit/log"

	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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

// TODO: This implementation isn't possible right now, as Kubernetes doesn't provide the
// ability to define arbitrary subresources on CRDs. The plan was initially to expose a
// /sudo subresource on the SudoRoleBinding and anyone who hit that endpoint would be
// added to the grants RoleBinding.
//
// An example of how this might integrate with DirectoryRoleBindings would be the
// following:

/*
# Define a Role that permits POST to the /sudo subresource of our
# SudoRoleBinding called sudoers
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: sudoers
rules:
  - apiGroups:
      - rbac.lawrjone.xyz
    resourceNames:
      - sudoers
    resources:
      - sudorolebindings/sudo
    verbs:
      - '*'

# Now define a DirectoryRoleBinding that would add the platform@ group into our
# sudoers Role
---
apiVersion: rbac.lawrjone.xyz/v1alpha1
kind: DirectoryRoleBinding
metadata:
  name: sudoers-group
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: sudoers
subjects:
  - kind: GoogleGroup
    name: platform@gocardless.com

# Finally we use a SudoRoleBinding to permit users to temporarily elevate
# themselves into the superuser ClusterRole. Our previous RBAC configuration
# ensures only platform@ can perform the required /sudo action, thereby
# elevating their permissions.
---
apiVersion: rbac.lawrjone.xyz/v1alpha1
kind: SudoRoleBinding
metadata:
  name: sudoers
spec:
  expiry: 60  # 1m
  roleBinding:
    roleRef:
      apiGroup: rbac.authorization.k8s.io
      kind: ClusterRole
      name: superuser
*/

// While we can create this functionality without subresources, it's probably not worth
// doing it until we have them.

const (
	EventError          = "Error"
	EventReconcile      = "Reconcile"
	EventNotFound       = "NotFound"
	EventGrantsNotFound = "GrantsNotFound"
	EventGrantsCreated  = "GrantsCreated"
	EventInvalidExpiry  = "InvalidExpiry"
	EventGrantExpired   = "GrantExpired"
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

type SudoRoleBindingReconciler struct {
	ctx      context.Context
	logger   kitlog.Logger
	recorder record.EventRecorder
	client   client.Client
}

// Reconcile manages a SudoRoleBinding. SudoRoleBindings allow users to temporarily
// elevate their permissions by adding themselves into a rolebinding.
//
// Each SudoRoleBinding creates an associated rolebindings that it adds subjects to when
// they successfully sudo.
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

	rb, err := r.findOrCreateGrants(logger, srb)
	if err != nil {
		return
	}

	validGrants := []rbacv1alpha1.SudoRoleBindingGrant{}
	for _, grant := range srb.Status.Grants {
		expiry, err := time.Parse(time.RFC3339, grant.Expiry)
		if err != nil {
			logger.Log("event", EventInvalidExpiry, "subject", grant.Subject.Name, "error", fmt.Errorf(
				"Invalid expiry time for grant subject %s: %v", grant.Subject.Name, err,
			))

			continue
		}

		if time.Now().After(expiry) {
			logger.Log("event", EventGrantExpired, "subject", grant.Subject.Name, "msg", fmt.Sprintf(
				"Grant expired for subject: %s", grant.Subject.Name,
			))

			continue
		}

		// If we get here, we know we're looking at a valid grant
		validGrants = append(validGrants, grant)
	}

	subjects := mapSubjects(validGrants)
	add, remove := rbacutils.Diff(subjects, rb.Subjects), rbacutils.Diff(rb.Subjects, subjects)
	if len(add) > 0 || len(remove) > 0 {
		rb.Subjects = subjects
		if err := r.client.Update(r.ctx, rb); err != nil {
			return reconcile.Result{}, err
		}
	}

	if !reflect.DeepEqual(srb.Status.Grants, validGrants) {
		srb.Status.Grants = validGrants
		if err := r.client.Status().Update(r.ctx, srb); err != nil {
			return reconcile.Result{}, err
		}
	}

	return reconcile.Result{}, nil
}

func mapSubjects(grants []rbacv1alpha1.SudoRoleBindingGrant) []rbacv1.Subject {
	subjects := []rbacv1.Subject{}
	for _, grant := range grants {
		subjects = append(subjects, grant.Subject)
	}

	return subjects
}

// findOrCreateGrants finds the rolebinding that powers the elevation of permissions to
// those defined in our SudoRoleBinding. It will initially have an empty set of subjects,
// as subjects should only be added to this role via the sudo action.
func (r *SudoRoleBindingReconciler) findOrCreateGrants(logger kitlog.Logger, srb *rbacv1alpha1.SudoRoleBinding) (rb *rbacv1.RoleBinding, err error) {
	rb = &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      srb.GetName(),
			Namespace: srb.GetNamespace(),
		},
		RoleRef:  srb.Spec.RoleBinding.RoleRef,
		Subjects: []rbacv1.Subject{},
	}

	return rb, r.findOrCreate(logger, srb, EventGrantsNotFound, EventGrantsCreated, rb)
}

// findOrCreate generates a Kubernetes resource if it doesn't already exist in the
// cluster. We set the object metadata to ensure the resource is owned by the
// SudoRoleBinding, and will get cleaned up whenever that resource is deleted.
func (r *SudoRoleBindingReconciler) findOrCreate(logger kitlog.Logger, srb *rbacv1alpha1.SudoRoleBinding, eventNotFound, eventCreated string, obj runtime.Object) (err error) {
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
				"Created %s: %s", reflect.TypeOf(obj), key,
			))
		}
	}

	return nil
}
