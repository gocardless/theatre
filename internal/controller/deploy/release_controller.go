package deploy

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/util/retry"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

	var err error

	logger.Info("release is new, will initialise")
	pendingActivation, err := r.findPendingActivationReleases(ctx, release)
	if err != nil {
		logger.Error(err, "failed to find pending activation releases")
		return ctrl.Result{}, err
	}

	// Multiple releases pending activation indicates something went wrong, e.g.
	// the controller was offline. Attempt to reconstruct timeline.
	if len(pendingActivation) > 1 {
		logger.Info("multiple releases pending activation, something went wrong, attempting to reconstruct timeline")

		err := r.safeReleaseActivation(ctx, logger, pendingActivation)
		if err != nil {
			logger.Error(err, "failed to reconcile multiple pending activation releases")
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	if !release.IsStatusInitialised() {
		err = r.initialiseReleaseStatus(ctx, *release)
		if err != nil {
			logger.Error(err, "failed to initialise release")
			return ctrl.Result{}, err
		}
		// requeue to update the release with the latest version
		return ctrl.Result{RequeueAfter: time.Microsecond * 1}, nil
	}

	err = r.handleAnnotations(ctx, logger, release)

	if err != nil {
		logger.Error(err, "failed to update status field of release")
	}

	err = r.trimExtraReleases(ctx, logger, req.Namespace, release.ReleaseConfig.TargetName)
	if err != nil {
		logger.Error(err, "failed to trim extra releases")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ReleaseReconciler) trimExtraReleases(ctx context.Context, logger logr.Logger, namespace string, target string) error {
	releases := &deployv1alpha1.ReleaseList{}
	err := r.List(ctx, releases,
		client.InNamespace(namespace),
		client.MatchingFields(map[string]string{
			"config.targetName":        target,
			"status.conditions.active": string(metav1.ConditionFalse),
		}),
	)

	if err != nil {
		return err
	}

	logger.Info("found inactive releases", "count", len(releases.Items))

	if len(releases.Items) < r.MaxReleasesPerTarget {
		return nil
	}

	releases.Sort()
	// trim releases to the configured maximum
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

// TODO: this should be refactored to either find the latest active release(s) or the latest release with a deployment end time
func (r *ReleaseReconciler) findActiveRelease(ctx context.Context, namespace string, target string) (*deployv1alpha1.Release, error) {
	releases := &deployv1alpha1.ReleaseList{}
	err := r.List(ctx, releases,
		client.InNamespace(namespace),
		client.MatchingFields(map[string]string{
			"config.targetName":        target,
			"status.conditions.active": string(metav1.ConditionTrue),
		}),
	)

	if err != nil {
		return nil, err
	}

	if len(releases.Items) == 0 {
		return nil, nil
	}

	if len(releases.Items) > 1 {
		return nil, fmt.Errorf("expected 1 active release for target %s, found %d", target, len(releases.Items))
	}

	return &releases.Items[0], nil
}

// The current way to active releases is by setting the deployment end time. The
// release controller will activate the release with the latest deployment end
// time.
func (r *ReleaseReconciler) handleAnnotations(ctx context.Context, logger logr.Logger, release *deployv1alpha1.Release) error {
	logger.Info("handling annotations for release", "release", release.Name)
	modified := false

	if release.AnnotatedWithSetDeploymentStartTime() {
		startTime, err := time.Parse(time.RFC3339, release.Annotations[deployv1alpha1.AnnotationKeyReleaseSetDeploymentStartTime])
		if err != nil {
			return err
		}

		if release.Status.DeploymentStartTime.IsZero() || !release.Status.DeploymentStartTime.Time.UTC().Equal(startTime.UTC()) {
			release.Status.DeploymentStartTime = metav1.NewTime(startTime)
			modified = true
		}
	}

	if release.AnnotatedWithSetDeploymentEndTime() {
		endTime, err := time.Parse(time.RFC3339, release.Annotations[deployv1alpha1.AnnotationKeyReleaseSetDeploymentEndTime])
		if err != nil {
			return err
		}

		logger.Info("setting deployment end time", "release", release.Name, "endTime", endTime.UTC(), "currentEndTime", release.Status.DeploymentEndTime.Time.UTC())

		if release.Status.DeploymentEndTime.IsZero() || !release.Status.DeploymentEndTime.Time.UTC().Equal(endTime.UTC()) {
			release.Status.DeploymentEndTime = metav1.NewTime(endTime)
			modified = true

			// activate release if deployment end time is after the current active releases time
			logger.Info("fetching current active release", "release", release.Name)
			previousRelease, err := r.findActiveRelease(ctx, release.Namespace, release.ReleaseConfig.TargetName)
			if err != nil {
				return err
			}

			if previousRelease == nil || previousRelease.Status.DeploymentEndTime.Time.Before(endTime) {
				logger.Info("activating release", "release", release.Name)
				release.Activate(MessageReleaseActive, previousRelease)
				modified = true

				if previousRelease != nil {
					messageSuperseded := fmt.Sprintf(MessageReleaseSuperseded, release.Name)
					previousRelease.Deactivate(messageSuperseded, release)
					err = r.updateReleaseStatus(ctx, previousRelease)
					if err != nil {
						return err
					}
				}
			}
		}
	}

	if modified {
		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			return r.updateReleaseStatus(ctx, release)
		})
	}

	return nil
}

func (r *ReleaseReconciler) initialiseReleaseStatus(ctx context.Context, release deployv1alpha1.Release) error {
	release.InitialiseStatus(MessageReleaseCreated)
	return r.updateReleaseStatus(ctx, &release)
}

func (r *ReleaseReconciler) updateReleaseStatus(ctx context.Context, release *deployv1alpha1.Release) error {
	return r.Status().Update(ctx, release)
}

func (r *ReleaseReconciler) findPendingActivationReleases(ctx context.Context, release *deployv1alpha1.Release) ([]deployv1alpha1.Release, error) {
	var releaseList deployv1alpha1.ReleaseList

	err := r.List(ctx, &releaseList, client.InNamespace(release.Namespace), client.MatchingFields(map[string]string{
		"config.targetName": release.ReleaseConfig.TargetName,
	}))

	if err != nil {
		return nil, err
	}

	var pendingActivation []deployv1alpha1.Release

	for _, release := range releaseList.Items {
		if release.AnnotatedWithSetDeploymentEndTime() && release.Status.DeploymentEndTime.IsZero() {
			pendingActivation = append(pendingActivation, release)
		}
	}

	return pendingActivation, nil
}

func effectiveReleaseTime(r *deployv1alpha1.Release) time.Time {
	// if !r.Status.DeploymentEndTime.IsZero() {
	return r.Status.DeploymentEndTime.Time
	// }
	// if !r.Status.DeploymentStartTime.IsZero() {
	// 	return r.Status.DeploymentStartTime.Time
	// }
	// return r.CreationTimestamp.Time
}

// The function returns two slices, one of viable releases and one of unknown releases.
// A viable release is a release that has a unique deployment end time. All other
// releases are will be marked with a condition unknown as we cannot determine
// which one should be activated.
func partitionReleasesByEndTimeTies(releases []deployv1alpha1.Release) (viable []deployv1alpha1.Release, unknown []deployv1alpha1.Release) {
	viable = make([]deployv1alpha1.Release, 0)
	unknown = make([]deployv1alpha1.Release, 0)

	releasesEndTimes := make(map[time.Time][]deployv1alpha1.Release)

	for i := range releases {
		if !releases[i].Status.DeploymentEndTime.IsZero() {
			releasesEndTimes[releases[i].Status.DeploymentEndTime.Time] = append(releasesEndTimes[releases[i].Status.DeploymentEndTime.Time], releases[i])
		}
	}

	for i := range releases {
		if !releases[i].Status.DeploymentEndTime.IsZero() && len(releasesEndTimes[releases[i].Status.DeploymentEndTime.Time]) > 1 {
			unknown = append(unknown, releases[i])
		} else {
			viable = append(viable, releases[i])
		}
	}

	return
}

// Attempts to reconstruct the timeline of releases by setting the active and
// superseded conditions based on the deployment end time.
func (r *ReleaseReconciler) safeReleaseActivation(ctx context.Context, logger logr.Logger, releases []deployv1alpha1.Release) error {
	if len(releases) == 0 {
		return nil
	}

	// Ensure every release has initialised conditions so we can reason about Active=True/False.
	for i := range releases {
		if !releases[i].IsStatusInitialised() {
			releases[i].InitialiseStatus(MessageReleaseCreated)
			releases[i].ParseAnnotations("Recreating timeline", nil)
		}
	}

	namespace := releases[0].Namespace
	target := releases[0].ReleaseConfig.TargetName

	viable, unknown := partitionReleasesByEndTimeTies(releases)

	for i := range unknown {
		if err := r.updateReleaseStatus(ctx, &unknown[i]); err != nil {
			return err
		}
	}

	if len(viable) == 0 {
		return nil
	}

	sort.SliceStable(viable, func(i, j int) bool {
		// Newest first
		ti := effectiveReleaseTime(&viable[i])
		tj := effectiveReleaseTime(&viable[j])
		if !ti.Equal(tj) {
			return ti.After(tj)
		}

		// Stable tie-breaker
		return string(viable[i].UID) > string(viable[j].UID)
	})

	winner := viable[0]

	// the winner is the one that should be active
	winner.Activate(MessageReleaseActive, &viable[1])
	// nextRelease := winner
	for i := 1; i < len(viable); i++ {
		viable[i].Deactivate(MessageReleaseSuperseded, nil)
		// nextRelease = viable[i]
	}

	activeRelease, err := r.findActiveRelease(ctx, namespace, target)
	if err != nil {
		return err
	}

	if activeRelease != nil {
		activeRelease.Deactivate(MessageReleaseSuperseded, nil)
		viable = append(viable, *activeRelease)
	}

	// Converge: exactly one active, all others inactive.
	// We do this using Status().Update per object to avoid needing a single multi-object transaction.
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {

		// Deactivate everyone else
		for i := range viable {
			current := viable[i]

			if err := r.updateReleaseStatus(ctx, &current); err != nil {
				return err
			}

		}

		logger.Info(
			"recovered active release after downtime",
			"target", target,
			"winner", winner.Name,
			"activeCandidates", len(viable),
			"total", len(releases),
		)

		return nil
	})
}
