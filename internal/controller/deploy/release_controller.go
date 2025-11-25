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
	MaxHistoryLimit      int
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

	err = mgr.GetFieldIndexer().IndexField(
		ctx,
		&deployv1alpha1.Release{},
		"status.phase",
		func(rawObj client.Object) []string {
			release := rawObj.(*deployv1alpha1.Release)
			return []string{string(release.Status.Phase)}
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

func isNewRelease(phase deployv1alpha1.Phase) bool {
	return phase == ""
}

func (r *ReleaseReconciler) prependToHistory(release *deployv1alpha1.Release) {
	entry := deployv1alpha1.ReleaseStatusEntry{
		Phase:           release.Status.Phase,
		Message:         release.Status.Message,
		LastAppliedTime: release.Status.LastAppliedTime,
		SupersededBy:    release.Status.SupersededBy,
		SupersededTime:  release.Status.SupersededTime,
	}

	release.Status.History = append([]deployv1alpha1.ReleaseStatusEntry{entry}, release.Status.History...)

	if len(release.Status.History) > r.MaxHistoryLimit {
		release.Status.History = release.Status.History[len(release.Status.History)-r.MaxHistoryLimit:]
	}
}

func (r *ReleaseReconciler) markReleaseActive(ctx context.Context, release *deployv1alpha1.Release) error {
	release.Status.LastAppliedTime = metav1.Now()
	release.Status.SupersededBy = ""
	release.Status.SupersededTime = metav1.Time{}
	release.Status.Phase = deployv1alpha1.PhaseActive
	return r.Status().Update(ctx, release)
}

func (r *ReleaseReconciler) markReleaseSuperseded(ctx context.Context, release *deployv1alpha1.Release, supersededBy string) error {
	r.prependToHistory(release)
	release.Status.SupersededBy = supersededBy
	release.Status.SupersededTime = metav1.Now()
	release.Status.Phase = deployv1alpha1.PhaseInactive
	return r.Status().Update(ctx, release)
}

func (r *ReleaseReconciler) supersedePreviousReleases(ctx context.Context, activeRelease *deployv1alpha1.Release) error {
	// Mark all other releases as inactive
	var releaseList deployv1alpha1.ReleaseList
	err := r.List(ctx, &releaseList,
		client.InNamespace(activeRelease.Namespace),
		client.MatchingFields(map[string]string{
			"spec.utopiaServiceTargetRelease": activeRelease.Spec.UtopiaServiceTargetRelease,
			"status.phase":                    string(deployv1alpha1.PhaseActive),
		}),
	)

	if err != nil {
		return err
	}

	for _, otherRelease := range releaseList.Items {
		if otherRelease.Name != activeRelease.Name {
			if otherRelease.Status.Phase == deployv1alpha1.PhaseActive {
				err := r.markReleaseSuperseded(ctx, &otherRelease, activeRelease.Name)
				if err != nil {
					return err
				}
			}
		}
	}

	return nil
}

func (r *ReleaseReconciler) Reconcile(logger logr.Logger, ctx context.Context, req ctrl.Request, release *deployv1alpha1.Release) (ctrl.Result, error) {
	logger = logger.WithValues("namespace", req.Namespace, "release", release.Name)
	logger.Info("reconciling release")

	if isNewRelease(release.Status.Phase) {
		err := r.markReleaseActive(ctx, release)
		if err != nil {
			logger.Error(err, "failed to mark release active")
			return ctrl.Result{}, err
		}

		err = r.supersedePreviousReleases(ctx, release)
		if err != nil {
			logger.Error(err, "failed to supersede previous releases")
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
