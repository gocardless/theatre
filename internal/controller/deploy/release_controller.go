package deploy

import (
	"context"
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

type CullingStrategy string

const (
	CullingStrategyDeploymentEndTime CullingStrategy = "deployment-end-time"
	CullingStrategySignature         CullingStrategy = "signature"
)

type ReleaseReconciler struct {
	client.Client
	Log                  logr.Logger
	Scheme               *runtime.Scheme
	MaxReleasesPerTarget int
	CullingStrategy      CullingStrategy
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
		logger.Info("release is new, will initialise")
		release.InitialiseStatus(MessageReleaseCreated)
		err = r.Status().Update(ctx, release)
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
		return ctrl.Result{}, err
	}

	// refetch the release
	err = r.Client.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: release.Name}, release)
	if err != nil {
		logger.Error(err, "failed to refetch release")
		return ctrl.Result{}, err
	}

	err = r.reconcileActiveReleaseByDeploymentEndTime(ctx, logger, *release)
	if err != nil {
		logger.Error(err, "failed to determine active release")
		return ctrl.Result{}, err
	}

	err = r.cullReleases(ctx, logger, req.Namespace, release.ReleaseConfig.TargetName)
	if err != nil {
		logger.Error(err, "failed to trim extra releases")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// This function ensures that the number of inactive releases does not exceed
// the configured maximum. It has two operating modes:
// 1. If the culling strategy is "deployment-end-time", it will delete based on
// effective time (deployment end time if set, otherwise creation time).
// 2. If the culling strategy is "signature", it will delete based on release
// signature uniqueness, where it will firstly cull releases that have repeating
// signatures, and only then delete releases based on effective time (deployment
// end time if set, otherwise creation time).
func (r *ReleaseReconciler) cullReleases(ctx context.Context, logger logr.Logger, namespace string, target string) error {
	if r.MaxReleasesPerTarget < 0 {
		logger.Info("culling is disabled, skipping")
		return nil
	}

	allReleasesList := &deployv1alpha1.ReleaseList{}
	err := r.List(ctx, allReleasesList,
		client.InNamespace(namespace),
		client.MatchingFields(map[string]string{
			"config.targetName": target,
		}),
	)

	if err != nil {
		return err
	}

	if len(allReleasesList.Items) < r.MaxReleasesPerTarget {
		return nil
	}

	inactiveReleases := []deployv1alpha1.Release{}
	for _, release := range allReleasesList.Items {
		if !release.IsConditionActive() {
			inactiveReleases = append(inactiveReleases, release)
		}
	}

	logger.Info("found inactive releases", "count", len(allReleasesList.Items))
	// excessReleaseCount is (active releases + inactive releases) - max releases
	excessReleaseCount := len(allReleasesList.Items) - r.MaxReleasesPerTarget

	signatureOccurrences := make(map[string]int)
	cullingCandidates := make([]deployv1alpha1.Release, 0)

	if r.CullingStrategy == CullingStrategySignature {
		for _, release := range inactiveReleases {
			signatureOccurrences[release.Status.Signature]++
		}

		for _, release := range inactiveReleases {
			if signatureOccurrences[release.Status.Signature] > 1 {
				cullingCandidates = append(cullingCandidates, release)
			}
		}
	}

	if len(cullingCandidates) == 0 {
		cullingCandidates = append(cullingCandidates, inactiveReleases...)
	}

	sort.Slice(cullingCandidates, func(i, j int) bool {
		// Oldest first (oldest at index 0, newest at the end)
		return cullingCandidates[i].GetEffectiveTime().Before(cullingCandidates[j].GetEffectiveTime())
	})

	// trim releases to the configured maximum
	releasesToDelete := cullingCandidates[:excessReleaseCount]

	logger.Info("culling releases", "count", len(releasesToDelete))

	for _, releaseToDelete := range releasesToDelete {
		logger.Info("deleting release", "releaseToDelete", releaseToDelete.Name)
		err := r.Delete(ctx, &releaseToDelete)
		if err != nil {
			logger.Error(err, "failed to delete release", "releaseToDelete", releaseToDelete.Name)
			return err
		}
	}

	return nil
}

// The active release is the one with the latest deployment end time, therefore
// in some cases the current release with an "Active" condition, might not have
// the latest timestamp (e.g. someone patched the release status). In this case
// we want to find all releases with a deployment end time after the given time
// + all the releases with an "Active" condition.
func (r *ReleaseReconciler) findActiveReleases(ctx context.Context, namespace string, target string, releaseEndTime *time.Time) ([]deployv1alpha1.Release, error) {
	releases := &deployv1alpha1.ReleaseList{}
	err := r.List(ctx, releases,
		client.InNamespace(namespace),
		client.MatchingFields(map[string]string{
			"config.targetName": target,
		}),
	)

	if err != nil {
		return nil, err
	}

	activeReleases := make([]deployv1alpha1.Release, 0)
	for _, rel := range releases.Items {
		if (releaseEndTime != nil && rel.Status.DeploymentEndTime.Time.After(*releaseEndTime)) || rel.IsConditionActive() {
			activeReleases = append(activeReleases, rel)
		}
	}

	return activeReleases, nil
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

		if !release.Status.DeploymentStartTime.Time.UTC().Equal(startTime.UTC()) {
			release.Status.DeploymentStartTime = metav1.NewTime(startTime)
			modified = true
		}
	}

	if release.AnnotatedWithSetDeploymentEndTime() {
		endTime, err := time.Parse(time.RFC3339, release.Annotations[deployv1alpha1.AnnotationKeyReleaseSetDeploymentEndTime])
		if err != nil {
			return err
		}

		if !release.Status.DeploymentEndTime.Time.UTC().Equal(endTime.UTC()) {
			release.Status.DeploymentEndTime = metav1.NewTime(endTime)
			modified = true
		}
	}

	if modified {
		return r.Status().Update(ctx, release)
	}

	return nil
}

// Reconciles the active release by the deployment end time, where the release
// with the latest deployment end time is set to be the active release.
func (r *ReleaseReconciler) reconcileActiveReleaseByDeploymentEndTime(ctx context.Context, logger logr.Logger, release deployv1alpha1.Release) error {
	if release.Status.DeploymentEndTime.IsZero() {
		return nil
	}

	previousActiveReleases, err := r.findActiveReleases(ctx, release.Namespace, release.ReleaseConfig.TargetName, &release.Status.DeploymentEndTime.Time)
	if err != nil {
		return err
	}

	logger.Info("found previous active releases", "count", len(previousActiveReleases))
	if len(previousActiveReleases) > 0 {
		sortReleasesByEndTime(previousActiveReleases)
		currentReleaseEndTime := release.Status.DeploymentEndTime.Time
		latestActiveReleaseTime := previousActiveReleases[0].Status.DeploymentEndTime.Time

		if currentReleaseEndTime.Before(latestActiveReleaseTime) || currentReleaseEndTime.Equal(latestActiveReleaseTime) {
			logger.Info("release end time is before or equal to latest active release end time, not activating")
			return nil
		}
	}

	var candidateReleases []deployv1alpha1.Release
	candidateReleases = append(candidateReleases, previousActiveReleases...)
	candidateReleases = append(candidateReleases, release)

	return r.setActiveReleaseAndSupersedeOthers(ctx, logger, candidateReleases)
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

// The function returns two slices, one of viable releases and one of unknown releases.
// A viable release is a release that has a unique deployment end time. All other
// releases will be marked with a condition unknown as we cannot determine which
// one should be activated.
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
		// If the deployment end time is not set or there are multiple releases with the same deployment end time,
		// we cannot determine which release should be active
		if !releases[i].Status.DeploymentEndTime.IsZero() && len(releasesEndTimes[releases[i].Status.DeploymentEndTime.Time]) > 1 {
			unknown = append(unknown, releases[i])
		} else {
			viable = append(viable, releases[i])
		}
	}

	return
}

func (r *ReleaseReconciler) setActiveReleaseAndSupersedeOthers(ctx context.Context, logger logr.Logger, releases []deployv1alpha1.Release) error {
	if len(releases) == 0 {
		return nil
	}

	sortReleasesByEndTime(releases)
	logger.Info("setting active release", "active release candidates", len(releases), "new active release", releases[0].Name)

	if len(releases) == 1 {
		releases[0].Activate(MessageReleaseActive, nil)
	} else {
		// the first release should be the active one
		releases[0].Activate(MessageReleaseActive, &releases[1])
		nextRelease := releases[0]
		// all of the rest are being superseded
		for i := 1; i < len(releases); i++ {
			if i+1 < len(releases) && releases[i].Status.PreviousRelease.ReleaseRef == "" {
				releases[i].Status.PreviousRelease = deployv1alpha1.ReleaseTransition{
					ReleaseRef:     releases[i+1].Name,
					TransitionTime: metav1.Now(),
				}
			}
			releases[i].Deactivate(MessageReleaseSuperseded, &nextRelease)
			nextRelease = releases[i]
		}
	}

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		for i := range releases {
			logger.Info("updating release", "release", releases[i].Name, "previousRelease", releases[i].Status.PreviousRelease.ReleaseRef)

			if err := r.Status().Update(ctx, &releases[i]); err != nil {
				return err
			}
		}

		return nil
	})
}

func sortReleasesByEndTime(releases []deployv1alpha1.Release) {
	sort.SliceStable(releases, func(i, j int) bool {
		// Newest first
		ti := releases[i].Status.DeploymentEndTime.Time
		tj := releases[j].Status.DeploymentEndTime.Time
		if !ti.Equal(tj) {
			return ti.After(tj)
		}

		// Stable tie-breaker
		return string(releases[i].UID) > string(releases[j].UID)
	})
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
		}
		releases[i].ParseAnnotations()
	}

	namespace := releases[0].Namespace
	target := releases[0].ReleaseConfig.TargetName

	viable, unknown := partitionReleasesByEndTimeTies(releases)
	for i := range unknown {
		if err := r.Status().Update(ctx, &unknown[i]); err != nil {
			return err
		}
	}

	if len(viable) == 0 {
		return nil
	}

	activeReleases, err := r.findActiveReleases(ctx, namespace, target, nil)
	if err != nil {
		return err
	}

	if activeReleases != nil {
		viable = append(viable, activeReleases...)
	}

	return r.setActiveReleaseAndSupersedeOthers(ctx, logger, viable)
}
