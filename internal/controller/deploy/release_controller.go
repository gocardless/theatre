package deploy

import (
	"context"
	"sort"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		"spec.utopiaServiceTargetRelease",
		func(rawObj client.Object) []string {
			release := rawObj.(*deployv1alpha1.Release)
			return []string{release.Spec.UtopiaServiceTargetRelease}
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
					return r.Reconcile(logger, ctx, request, obj.(*deployv1alpha1.Release))
				},
			),
		)
}

func (r *ReleaseReconciler) handleNewRelease(logger logr.Logger, ctx context.Context, release *deployv1alpha1.Release) error {
	release.Status.Phase = deployv1alpha1.PhaseActive
	release.Status.LastAppliedTime = metav1.Now()

	err := r.Status().Update(ctx, release)
	if err != nil {
		return err
	}
	// Mark all other releases as inactive
	var releaseList deployv1alpha1.ReleaseList
	err = r.List(ctx, &releaseList,
		client.InNamespace(release.Namespace),
		client.MatchingFields(map[string]string{
			"spec.utopiaServiceTargetRelease": release.Spec.UtopiaServiceTargetRelease,
		}),
	)

	if err != nil {
		return err
	}

	for _, otherRelease := range releaseList.Items {
		if otherRelease.Name != release.Name {
			if otherRelease.Status.Phase == deployv1alpha1.PhaseActive {
				otherRelease.Status.Phase = deployv1alpha1.PhaseInactive
				otherRelease.Status.SupersededBy = release.Name
				otherRelease.Status.SupersededTime = metav1.Now()
				err := r.Status().Update(ctx, &otherRelease)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (r *ReleaseReconciler) trimExtraReleases(logger logr.Logger, ctx context.Context, namespace string, target string) error {

	releases := &deployv1alpha1.ReleaseList{}
	err := r.List(ctx, releases,
		client.InNamespace(namespace),
		client.MatchingFields(map[string]string{
			"spec.utopiaServiceTargetRelease": target,
		}),
	)

	if err != nil {
		return err
	}

	if len(releases.Items) <= r.MaxReleasesPerTarget {
		return nil
	}

	sort.Slice(releases.Items, func(i, j int) bool {
		iActive := releases.Items[i].Status.Phase == deployv1alpha1.PhaseActive
		jActive := releases.Items[j].Status.Phase == deployv1alpha1.PhaseActive

		// If phases differ, Active comes first
		if iActive != jActive {
			return iActive
		}

		// If both have same phase, sort by LastAppliedTime descending (newer first)
		return releases.Items[j].Status.LastAppliedTime.Before(&releases.Items[i].Status.LastAppliedTime)
	})

	// trim releases to max
	releasesToDelete := releases.Items[r.MaxReleasesPerTarget:]

	for _, releaseToDelete := range releasesToDelete {
		logger.Info("deleting release", "release", releaseToDelete.Name)
		err := r.Delete(ctx, &releaseToDelete)
		if err != nil {
			logger.Error(err, "failed to delete release", "release", releaseToDelete.Name)
			return err
		}
	}

	return nil
}

func (r *ReleaseReconciler) Reconcile(logger logr.Logger, ctx context.Context, req ctrl.Request, release *deployv1alpha1.Release) (ctrl.Result, error) {
	logger = logger.WithValues("namespace", req.Namespace, "release", release.Name)
	logger.Info("reconciling release")

	if release.Status.Phase == "" {
		err := r.handleNewRelease(logger, ctx, release)
		if err != nil {
			logger.Error(err, "failed to handle new release")
			return ctrl.Result{}, err
		}

		err = r.trimExtraReleases(logger, ctx, req.Namespace, release.Spec.UtopiaServiceTargetRelease)
		if err != nil {
			logger.Error(err, "failed to trim extra releases")
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}
