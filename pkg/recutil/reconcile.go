package recutil

import (
	"context"
	"fmt"
	"reflect"

	kitlog "github.com/go-kit/kit/log"
	"github.com/gocardless/theatre/pkg/logging"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	EventStart    = "Start"
	EventNotFound = "NotFound"
	EventError    = "Error"
	EventComplete = "Complete"
)

// ObjectReconcileFunc defines the expected interface for the reconciliation of a single
// object type- it can be used to avoid boilerplate for finding and initializing objects
// at the start of traditional reconciliation loops.
type ObjectReconcileFunc func(logger kitlog.Logger, request reconcile.Request, obj runtime.Object) (reconcile.Result, error)

// ResolveAndReconcile helps avoid boilerplate where you would normally attempt to fetch
// your modified object at the start of a reconciliation loop, and instead calls an inner
// reconciliation function with the already resolved object.
func ResolveAndReconcile(ctx context.Context, logger kitlog.Logger, mgr manager.Manager, objType runtime.Object, inner ObjectReconcileFunc) reconcile.Reconciler {
	return reconcile.Func(func(request reconcile.Request) (res reconcile.Result, err error) {
		logger := kitlog.With(logger, "request", request)
		logger.Log("event", EventStart, "msg", "starting reconciliation")

		defer func() {
			if err != nil {
				logger.Log("event", EventError, "error", err)
			} else {
				logger.Log("event", EventComplete, "msg", "completed reconciliation")
			}

		}()

		// Prepare a new object for this request
		obj := objType.DeepCopyObject()

		if err := mgr.GetClient().Get(ctx, request.NamespacedName, obj); err != nil {
			if errors.IsNotFound(err) {
				return res, logger.Log("event", EventNotFound)
			}

			return res, err
		}

		logger = logging.WithRecorder(logger, mgr.GetRecorder("theatre"), obj)

		return inner(logger, request, obj)
	})
}

// DiffFunc takes two Kubernetes resources: expected and existing. Both are assumed to be
// the same Kind. It compares the two, and returns an Outcome indicating how to
// transition from existing to expected. If an update is required, it will set the
// relevant fields on existing to their intended values. This is so that we can simply
// resubmit the existing resource, and any fields automatically set by the Kubernetes API
// server will be retained.
type DiffFunc func(runtime.Object, runtime.Object) Outcome

// Outcome describes the operation performed by CreateOrUpdate.
type Outcome string

const (
	Create Outcome = "create"
	Update Outcome = "update"
	None   Outcome = "none"
	Error  Outcome = "error"
)

// ObjWithMeta describes a Kubernetes resource with a metadata field. It's a combination
// of two common existing Kubernetes interfaces. We do this because we want to use methods
// from each in CreateOrUpdate, whilst still keeping the argument type generic.
type ObjWithMeta interface {
	metav1.Object
	runtime.Object
}

// CreateOrUpdate takes a Kubernetes object and a "diff function" and attempts to ensure
// that the the object exists in the cluster with the correct state. It will use the diff
// function to determine any differences between the cluster state and the local state and
// use that to decide how to update it.
func CreateOrUpdate(ctx context.Context, c client.Client, existing ObjWithMeta, kind string, diffFunc DiffFunc) (Outcome, error) {
	name := types.NamespacedName{
		Namespace: existing.GetNamespace(),
		Name:      existing.GetName(),
	}
	expected := existing.(runtime.Object).DeepCopyObject()
	err := c.Get(ctx, name, existing)
	if err != nil {
		if !errors.IsNotFound(err) {
			return Error, err
		}
		if err := c.Create(ctx, existing); err != nil {
			return Error, err
		}
		return Create, nil
	}

	// existing contains the state we just fetched from Kubernetes.
	// expected contains the state we're trying to reconcile towards.
	// If an update is required, DiffFunc will set the relevant fields on existing such that we
	// can just resubmit it to the cluster to achieve our desired state.

	op := diffFunc(expected, existing)
	switch op {
	case Update:
		if err := c.Update(ctx, existing); err != nil {
			return Error, err
		}
		return Update, nil
	case None:
		return None, nil
	default:
		return Error, fmt.Errorf("Unrecognised operation: %s", op)
	}
}

// RoleDiff is a DiffFunc for Roles
func RoleDiff(expectedObj runtime.Object, existingObj runtime.Object) Outcome {
	expected := expectedObj.(*rbacv1.Role)
	existing := existingObj.(*rbacv1.Role)

	if !reflect.DeepEqual(expected.Rules, existing.Rules) {
		existing.Rules = expected.Rules
		return Update
	}

	return None
}

// RoleBindingDiff is a DiffFunc for RoleBindings
func RoleBindingDiff(expectedObj runtime.Object, existingObj runtime.Object) Outcome {
	expected := expectedObj.(*rbacv1.RoleBinding)
	existing := existingObj.(*rbacv1.RoleBinding)
	operation := None

	if !reflect.DeepEqual(expected.Subjects, existing.Subjects) {
		existing.Subjects = expected.Subjects
		operation = Update
	}

	if !reflect.DeepEqual(expected.RoleRef, existing.RoleRef) {
		existing.RoleRef = expected.RoleRef
		operation = Update
	}

	return operation
}
