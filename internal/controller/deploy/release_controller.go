package deploy

import (
	"context"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type ReleaseReconciler struct {
	client.Client
	Log                  logr.Logger
	Scheme               *runtime.Scheme
	MaxReleasesPerTarget int
}

func (r *ReleaseReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := r.Log.WithValues("component", "Release")

	err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&deployv1alpha1.Release{},
		"config.targetName",
		func(rawObj client.Object) []string {
			release := rawObj.(*deployv1alpha1.Release)
			return []string{release.ReleaseConfig.TargetName}
		},
	)

	if err != nil {
		return err
	}

	err = mgr.GetFieldIndexer().IndexField(
		ctx,
		&deployv1alpha1.Release{},
		"status.conditions.active",
		func(rawObj client.Object) []string {
			release := rawObj.(*deployv1alpha1.Release)
			condition := meta.FindStatusCondition(release.Status.Conditions, deployv1alpha1.ReleaseConditionActive)
			if condition == nil {
				return []string{}
			}
			return []string{string(condition.Status)}
		},
	)

	if err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&deployv1alpha1.Release{}).
		Complete(
			recutil.ResolveAndReconcile(
				ctx, logger, mgr, &deployv1alpha1.Release{},
				func(logger logr.Logger, request ctrl.Request, obj runtime.Object) (ctrl.Result, error) {
					return r.Reconcile(ctx, logger, request, obj.(*deployv1alpha1.Release))
				},
			),
		)
}

func (r *ReleaseReconciler) Reconcile(ctx context.Context, logger logr.Logger, req ctrl.Request, release *deployv1alpha1.Release) (ctrl.Result, error) {
	logger = logger.WithValues("namespace", req.Namespace, "release", release.Name)
	logger.Info("reconciling release")

	return ctrl.Result{}, nil
}
