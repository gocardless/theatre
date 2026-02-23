package deploy

import (
	"context"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type AutomatedRollbackPolicyReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func (r *AutomatedRollbackPolicyReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := r.Log.WithValues("component", "AutomatedRollback")

	ctrlBuilder := ctrl.NewControllerManagedBy(mgr).
		For(&deployv1alpha1.AutomatedRollbackPolicy{})

	return ctrlBuilder.Complete(
		recutil.ResolveAndReconcile(ctx, logger, mgr, &deployv1alpha1.AutomatedRollbackPolicy{},
			func(logger logr.Logger, request ctrl.Request, obj runtime.Object) (ctrl.Result, error) {
				return r.Reconcile(ctx, logger, request, obj.(*deployv1alpha1.AutomatedRollbackPolicy))
			},
		),
	)
}

func (r *AutomatedRollbackPolicyReconciler) Reconcile(ctx context.Context, logger logr.Logger, request ctrl.Request, obj runtime.Object) (ctrl.Result, error) {
	return ctrl.Result{}, nil
}
