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

type RollbackReconciler struct {
	client.Client
	Log                  logr.Logger
	Scheme               *runtime.Scheme
	RollbackHistoryLimit int
}

func (r *RollbackReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := r.Log.WithValues("component", "Rollback")

	// err := mgr.GetFieldIndexer().IndexField(
	// 	ctx,
	// 	&deployv1alpha1.Rollback{},
	// 	"config.targetName",
	// 	func(rawObj client.Object) []string {
	// 		rollback := rawObj.(*deployv1alpha1.Rollback)
	// 		return []string{rollback.RollbackConfig.TargetName}
	// 	},
	// )

	// if err != nil {
	// 	return err
	// }

	// err := mgr.GetFieldIndexer().IndexField(
	// 	ctx,
	// 	&deployv1alpha1.Rollback{},
	// 	"status.conditions.active",
	// 	func(rawObj client.Object) []string {
	// 		rollback := rawObj.(*deployv1alpha1.Rollback)
	// 		condition := meta.FindStatusCondition(rollback.Status.Conditions, deployv1alpha1.RollbackConditionActive)
	// 		if condition == nil {
	// 			return []string{}
	// 		}
	// 		return []string{string(condition.Status)}
	// 	},
	// )

	// if err != nil {
	// 	return err
	// }

	return ctrl.NewControllerManagedBy(mgr).
		For(&deployv1alpha1.Rollback{}).
		Complete(
			recutil.ResolveAndReconcile(
				ctx, logger, mgr, &deployv1alpha1.Rollback{},
				func(logger logr.Logger, request ctrl.Request, obj runtime.Object) (ctrl.Result, error) {
					return r.Reconcile(ctx, logger, request, obj.(*deployv1alpha1.Rollback))
				},
			),
		)
}

func (r *RollbackReconciler) Reconcile(ctx context.Context, logger logr.Logger, req ctrl.Request, rollback *deployv1alpha1.Rollback) (ctrl.Result, error) {
	logger = logger.WithValues("namespace", req.Namespace, "rollback", rollback.Name)
	logger.Info("reconciling rollback")

	return ctrl.Result{}, nil
}
