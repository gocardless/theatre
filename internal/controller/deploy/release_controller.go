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

type ReleaseReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func (r *ReleaseReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := r.Log.WithValues("component", "Release")
	return ctrl.NewControllerManagedBy(mgr).
		For(&deployv1alpha1.Release{}).
		Complete(
			recutil.ResolveAndReconcile(
				ctx, logger, mgr, &deployv1alpha1.Release{},
				func(logger logr.Logger, request ctrl.Request, obj runtime.Object) (ctrl.Result, error) {
					return r.Reconcile(logger, ctx, request, obj.(*deployv1alpha1.Release))
				},
			),
		)
}

func (r *ReleaseReconciler) Reconcile(logger logr.Logger, ctx context.Context, req ctrl.Request, release *deployv1alpha1.Release) (ctrl.Result, error) {
	logger = logger.WithValues("namespace", req.Namespace, "release", release.Name)
	logger.Info("reconciling release")

	if release.Status.Phase == "" {
		release.Status.Phase = deployv1alpha1.PhaseActive

		err := r.Status().Update(ctx, release)
		if err != nil {
			logger.Error(err, "failed to update release status")
			return ctrl.Result{}, err
		}
		// Mark all other releases as inactive
		var releaseList deployv1alpha1.ReleaseList
		// TODO: maybe filter by label
		err = r.List(ctx, &releaseList, client.InNamespace(req.Namespace))
		if err != nil {
			logger.Error(err, "failed to list releases")
			return ctrl.Result{}, err
		}

		for _, otherRelease := range releaseList.Items {
			if otherRelease.Name != release.Name {
				if otherRelease.Status.Phase == deployv1alpha1.PhaseActive {
					otherRelease.Status.Phase = deployv1alpha1.PhaseInactive
					err := r.Status().Update(ctx, &otherRelease)
					if err != nil {
						logger.Error(err, "failed to set release as inactive", "release", otherRelease.Name)
						return ctrl.Result{}, err
					}
					logger.Info("set release as inactive", "release", otherRelease.Name)
				}
			}
		}
	}

	return ctrl.Result{}, nil
}
