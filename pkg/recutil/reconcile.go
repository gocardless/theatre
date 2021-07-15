package recutil

import (
	"context"
	"fmt"
	"reflect"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	rbacv1alpha1 "github.com/gocardless/theatre/v3/apis/rbac/v1alpha1"
	"github.com/gocardless/theatre/v3/pkg/logging"
)

const (
	EventRequestStart = "ReconcileRequestStart"
	EventNotFound     = "ReconcileNotFound"
	EventStart        = "ReconcileStart"
	EventSkipped      = "ReconcileSkipped"
	EventRequeued     = "ReconcileRequeued"
	EventError        = "ReconcileError"
	EventComplete     = "ReconcileComplete"
)

var (
	reconcileErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_reconcile_errors_total",
			Help: "Counter of errors from reconcile loops, labelled by group_version_kind",
		},
		[]string{"kind"},
	)
)

func init() {
	prometheus.MustRegister(reconcileErrorsTotal)
}

// ObjectReconcileFunc defines the expected interface for the reconciliation of a single
// object type- it can be used to avoid boilerplate for finding and initializing objects
// at the start of traditional reconciliation loops.
type ObjectReconcileFunc func(logger logr.Logger, request reconcile.Request, obj runtime.Object) (reconcile.Result, error)

// ResolveAndReconcile helps avoid boilerplate where you would normally attempt to fetch
// your modified object at the start of a reconciliation loop, and instead calls an inner
// reconciliation function with the already resolved object.
func ResolveAndReconcile(ctx context.Context, logger logr.Logger, mgr manager.Manager, objType runtime.Object, inner ObjectReconcileFunc) reconcile.Reconciler {
	return reconcile.Func(func(ctx context.Context, request reconcile.Request) (res reconcile.Result, err error) {
		logger := logger.WithValues("request", request)
		logger.Info("Reconcile request start", "event", EventRequestStart)

		// Prepare a new object for this request
		rawObj := objType.DeepCopyObject()
		obj, ok := rawObj.(ObjWithMeta)
		if !ok {
			return res, errors.New("reconciled object does not have metadata")
		}

		defer func() {
			if err != nil {
				// Conflict errors are typically temporary, caused by something
				// external to the controller updating the object that is being
				// reconciled, or the controller's object cache not being up-to-date
				// (which can occur as a result of an update in a previous
				// reconciliation).
				// In these cases the reconciliation will be retried, and we do not
				// want to pollute the object's events with transient errors which have
				// no means of avoiding.
				if apierrors.IsConflict(errors.Cause(err)) {
					logging.WithNoRecord(logger).Info(err.Error(), "event", EventError, "error", err)
				} else {
					logger.Info(err.Error(), "event", EventError, "error", err)
					reconcileErrorsTotal.WithLabelValues(obj.GetObjectKind().GroupVersionKind().Kind).Inc()
				}
			} else {
				logger.Info("Completed reconciliation", "event", EventComplete)
			}

		}()

		if err := mgr.GetClient().Get(ctx, request.NamespacedName, obj); err != nil {
			if apierrors.IsNotFound(err) {
				logger.Info("could not find event", "event", EventNotFound)
				return res, nil
			}

			return res, err
		}

		logger = logging.WithEventRecorder(logger, mgr.GetEventRecorderFor("theatre"), obj)
		logger.Info("Starting reconciliation", "event", EventStart)

		// If the object is being deleted then don't attempt any further
		// reconciliation, as this can lead to recreating child resources (which
		// we'd expect to be eventually deleted via propagation) and getting stuck
		// in an infinite loop, due to these resources now blocking the deletion of
		// the parent.
		// If we need to support finalizers in the future then this will need to be
		// extended to call a function that performs the finalizer actions.
		if !obj.GetDeletionTimestamp().IsZero() {
			logger.Info("Skipping reconciliation due to deletion", "event", EventSkipped)
			res = reconcile.Result{Requeue: false}
			return res, nil
		}

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
func CreateOrUpdate(ctx context.Context, c client.Client, existing ObjWithMeta, diffFunc DiffFunc) (Outcome, error) {
	name := client.ObjectKey{
		Namespace: existing.GetNamespace(),
		Name:      existing.GetName(),
	}
	expected := existing.(runtime.Object).DeepCopyObject()
	err := c.Get(ctx, name, existing)
	if err != nil {
		if !apierrors.IsNotFound(err) {
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

// DirectoryRoleBindingDiff is a DiffFunc for DirectoryRoleBindings
func DirectoryRoleBindingDiff(expectedObj runtime.Object, existingObj runtime.Object) Outcome {
	expected := expectedObj.(*rbacv1alpha1.DirectoryRoleBinding)
	existing := existingObj.(*rbacv1alpha1.DirectoryRoleBinding)

	operation := None

	if !reflect.DeepEqual(expected.Spec.Subjects, existing.Spec.Subjects) {
		existing.Spec.Subjects = expected.Spec.Subjects
		operation = Update
	}

	if !reflect.DeepEqual(expected.Spec.RoleRef, existing.Spec.RoleRef) {
		existing.Spec.RoleRef = expected.Spec.RoleRef
		operation = Update
	}

	return operation
}
