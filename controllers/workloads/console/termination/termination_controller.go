package termination

import (
	"context"
	"fmt"

	workloadsv1alpha1 "github.com/gocardless/theatre/v3/apis/workloads/v1alpha1"

	"github.com/gocardless/theatre/v3/controllers/workloads/console/termination/handler"
	"github.com/gocardless/theatre/v3/controllers/workloads/console/termination/status"
	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	watchhandler "sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	h := handler.NewTerminationHandler(mgr.GetClient(), &handler.Options{})
	return &TerminationReconciler{Client: mgr.GetClient(), handler: h, scheme: mgr.GetScheme()}
}

func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// create the controller
	c, err := controller.New("console-termination-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for Console changes
	err = c.Watch(&source.Kind{Type: &workloadsv1alpha1.Console{}}, &watchhandler.EnqueueRequestForOwner{
		IsController: true,
	})
	if err != nil {
		return nil
	}

	// Watch for job changes
	err = c.Watch(&source.Kind{Type: &batchv1.Job{}}, &watchhandler.EnqueueRequestForOwner{
		OwnerType:    &workloadsv1alpha1.Console{},
		IsController: true,
	})
	if err != nil {
		return nil
	}

	// err = mgr.GetCache().IndexField(&batchv1.Job{}, "metadata.labels" ,func(obj runtime.Object) []string

	return nil
}

var _ reconcile.Reconciler = &TerminationReconciler{}

type TerminationReconciler struct {
	client.Client
	handler *handler.TerminationHandler
	scheme  *runtime.Scheme
}

func (r *TerminationReconciler) SetupWithManager(ctx context.Context, mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

func (r *TerminationReconciler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	instance := &workloadsv1alpha1.Console{}
	err := r.Get(context.Background(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, err
	}

	result, err := r.handler.Handle(instance)
	if err != nil {
		statusErr := status.UpdateStatus(r.Client, instance, result)
		if statusErr != nil {
			// todo: logging good
			panic("failed to update status")
		}

		return reconcile.Result{}, fmt.Errorf("error handling console %s: %v", instance.GetName(), err)
	}

	err = status.UpdateStatus(r.Client, instance, result)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("error updating status: %v", err)
	}
	return reconcile.Result{}, nil
}
