package deploy

import (
	"context"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

type ReleaseReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func (r *ReleaseReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := r.Log.WithValues("component", "Release")
	return ctrl.NewControllerManagedBy(mgr).
		For(&deployv1alpha1.Release{}).
		Watches(
			&deployv1alpha1.Release{},
			&handler.EnqueueRequestForObject{},
		).
		Complete(
			recutil.ResolveAndReconcile(
				ctx, logger, mgr, &deployv1alpha1.Release{},
				func(logger logr.Logger, request ctrl.Request, obj runtime.Object) (ctrl.Result, error) {
					return r.Reconcile(logger, ctx, request, obj.(*deployv1alpha1.Release))
				},
			),
		)
}

func (r *ReleaseReconciler) Reconcile(logger logr.Logger, ctx context.Context, req ctrl.Request, rel *deployv1alpha1.Release) (ctrl.Result, error) {
	logger = logger.WithValues("release", req.NamespacedName)
	logger.Info("reconciling release", rel.ObjectMeta.Name)
	return ctrl.Result{}, nil
}
