package deploy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	analysisv1alpha1 "github.com/akuity/kargo/api/stubs/rollouts/v1alpha1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/deploy"
	"github.com/gocardless/theatre/v5/pkg/logging"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultCullingStrategy = deployv1alpha1.AnnotationValueCullingStrategyEndTime
	DefaultReleaseLimit    = 10

	// Events
	EventSuccessfulStatusUpdate = "SuccessfulStatusUpdate"
	EventNoStatusUpdate         = "NoStatusUpdate"

	IndexFieldOwner = ".metadata.controller"
)

var apiGVStr = deployv1alpha1.GroupVersion.String()

type ReleaseReconciler struct {
	client.Client
	Log             logr.Logger
	Scheme          *runtime.Scheme
	AnalysisEnabled bool
}

func (r *ReleaseReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := r.Log.WithValues("component", "Release")

	ctrlBuilder := ctrl.NewControllerManagedBy(mgr).For(&deployv1alpha1.Release{})

	// Only set AnalysisRun ownership, and owner indexed field, if analysis is enabled
	if r.AnalysisEnabled {
		ctrlBuilder = ctrlBuilder.Owns(&analysisv1alpha1.AnalysisRun{})

		err := mgr.GetFieldIndexer().IndexField(
			ctx,
			&analysisv1alpha1.AnalysisRun{},
			IndexFieldOwner,
			func(rawObj client.Object) []string {
				run := rawObj.(*analysisv1alpha1.AnalysisRun)
				owner := metav1.GetControllerOf(run)
				if owner == nil {
					return nil
				}
				if owner.APIVersion != apiGVStr || owner.Kind != "Release" {
					return nil
				}
				return []string{owner.Name}
			},
		)
		if err != nil {
			return err
		}
	}

	err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&deployv1alpha1.Release{},
		"config.targetName",
		func(rawObj client.Object) []string {
			release := rawObj.(*deployv1alpha1.Release)
			return []string{release.TargetName}
		},
	)
	if err != nil {
		return err
	}

	return ctrlBuilder.Complete(
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

	if !release.IsStatusInitialised() {
		logger.Info("release is new, will initialise")
		release.InitialiseStatus(MessageReleaseCreated)
	}

	r.handleAnnotations(logger, release)
	analysisErr := r.ReconcileAnalysis(ctx, logger, req, release)

	outcome, err := recutil.CreateOrUpdate(ctx, r.Client, release, recutil.StatusDiff)
	if err != nil {
		logger.Error(err, "failed to update release status")
		return ctrl.Result{}, errors.Join(err, analysisErr)
	}

	switch outcome {
	case recutil.StatusUpdate:
		logger.Info("Updated release status", "event", EventSuccessfulStatusUpdate)
	case recutil.None:
		logging.WithNoRecord(logger).Info("No status update needed", "event", EventNoStatusUpdate)
	default:
		logger.Info("Unexpected outcome from CreateOrUpdate", "outcome", outcome)
	}

	err = r.cullReleases(ctx, logger, req.Namespace, release.TargetName)
	if err != nil {
		logger.Error(err, "failed to cull releases")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, analysisErr
}

// The current way to active releases is by setting the deployment end time. The
// release controller will activate the release with the latest deployment end
// time.
func (r *ReleaseReconciler) handleAnnotations(logger logr.Logger, release *deployv1alpha1.Release) {
	logger.Info("handling annotations for release", "release", release.Name)

	// Handle theatre.gocardless.com/deployment-start-time annotation
	startTimeString, found := release.Annotations[deployv1alpha1.AnnotationKeyReleaseDeploymentStartTime]
	if !found || startTimeString == "" {
		if !release.Status.DeploymentStartTime.IsZero() {
			release.SetDeploymentStartTime(metav1.Time{})
		}
	} else {
		startTime, err := time.Parse(time.RFC3339, startTimeString)
		if err != nil {
			logger.Error(err, "failed to parse deployment start time annotation", "annotation", release.Annotations[deployv1alpha1.AnnotationKeyReleaseDeploymentStartTime])
		} else if !release.Status.DeploymentStartTime.Time.UTC().Equal(startTime.UTC()) {
			release.SetDeploymentStartTime(metav1.NewTime(startTime))
		}
	}

	// Handle theatre.gocardless.com/deployment-end-time
	endTimeString, found := release.Annotations[deployv1alpha1.AnnotationKeyReleaseDeploymentEndTime]
	if !found || endTimeString == "" {
		if !release.Status.DeploymentEndTime.IsZero() {
			release.SetDeploymentEndTime(metav1.Time{})
		}
	} else {
		endTime, err := time.Parse(time.RFC3339, endTimeString)
		if err != nil {
			logger.Error(err, "failed to parse deployment end time annotation", "annotation", release.Annotations[deployv1alpha1.AnnotationKeyReleaseDeploymentEndTime])
		} else if !release.Status.DeploymentEndTime.Time.UTC().Equal(endTime.UTC()) {
			release.SetDeploymentEndTime(metav1.NewTime(endTime))
		}
	}

	// Handle theatre.gocardless.com/active annotation
	activate, found := release.Annotations[deployv1alpha1.AnnotationKeyReleaseActivate]
	desiredActive := found && activate == deployv1alpha1.AnnotationValueReleaseActivateTrue
	if desiredActive != release.IsConditionActiveTrue() {
		if desiredActive {
			release.Activate(MessageReleaseActive)
		} else {
			release.Deactivate(MessageReleaseInactive)
		}
	}

	// Handle theatre.gocardless.com/previous-release
	previousRelease, found := release.Annotations[deployv1alpha1.AnnotationKeyReleasePreviousRelease]
	if found {
		if previousRelease != release.Status.PreviousRelease.ReleaseRef {
			release.SetPreviousRelease(previousRelease)
		}
	} else if release.Status.PreviousRelease.ReleaseRef != "" {
		release.SetPreviousRelease("")
	}
}

func (r *ReleaseReconciler) cullConfig(ctx context.Context, logger logr.Logger, namespace string) (limit int, strategy string, err error) {
	limit = DefaultReleaseLimit
	strategy = DefaultCullingStrategy

	var namespaceObj corev1.Namespace
	if err := r.Client.Get(ctx, client.ObjectKey{Name: namespace}, &namespaceObj); err != nil {
		return 0, "", err
	}

	if mpt, ok := namespaceObj.Annotations[deployv1alpha1.AnnotationKeyReleaseLimit]; ok {
		newLimit, err := strconv.Atoi(mpt)
		if err != nil {
			logger.Error(err, fmt.Sprintf("invalid release limit annotation value, defaulting to %d", DefaultReleaseLimit),
				"annotation", deployv1alpha1.AnnotationKeyReleaseLimit, "value", mpt)
		} else {
			limit = newLimit
		}
	}

	if cs, ok := namespaceObj.Annotations[deployv1alpha1.AnnotationKeyCullingStrategy]; ok {
		if cs == deployv1alpha1.AnnotationValueCullingStrategyEndTime || cs == deployv1alpha1.AnnotationValueCullingStrategySignature {
			strategy = cs
		} else {
			logger.Error(fmt.Errorf("unsupported culling strategy"), fmt.Sprintf("%s=%s is not a valid culling strategy, defaulting to %s", deployv1alpha1.AnnotationKeyCullingStrategy, cs, DefaultCullingStrategy),
				"annotation", deployv1alpha1.AnnotationKeyCullingStrategy, "value", cs)
		}
	}

	return limit, strategy, nil
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
	limit, strategy, err := r.cullConfig(ctx, logger, namespace)
	if err != nil {
		return err
	}

	releaseList := &deployv1alpha1.ReleaseList{}
	matchFields := client.MatchingFields(map[string]string{"config.targetName": target})
	if err := r.List(ctx, releaseList, client.InNamespace(namespace), matchFields); err != nil {
		return err
	}

	if len(releaseList.Items) <= limit {
		logger.Info("number of releases is within limit, skipping", "current", len(releaseList.Items), "limit", limit)
		return nil
	}

	inactiveReleases := []deployv1alpha1.Release{}
	for _, release := range releaseList.Items {
		// We want to cull releases that are initialised but not active
		if release.IsStatusInitialised() && !release.IsConditionActiveTrue() {
			inactiveReleases = append(inactiveReleases, release)
		}
	}

	cullingCandidates := make([]deployv1alpha1.Release, 0)
	if strategy == deployv1alpha1.AnnotationValueCullingStrategySignature {
		cullingCandidates = releasesWithRepeatingSignatures(inactiveReleases)
	}

	// Regardless of the strategy, if no candidates were found, fall back to all inactive releases
	if len(cullingCandidates) == 0 {
		cullingCandidates = append(cullingCandidates, inactiveReleases...)
	}

	slices.SortFunc(cullingCandidates, func(a, b deployv1alpha1.Release) int {
		// Oldest first (oldest at index 0, newest at the end)
		return a.GetEffectiveTime().Compare(b.GetEffectiveTime())
	})

	// trim releases to the configured maximum
	excessCount := len(releaseList.Items) - limit
	if excessCount > len(cullingCandidates) {
		logger.Info("not enough culling candidates to meet limit", "current", len(releaseList.Items), "limit", limit, "candidates", len(cullingCandidates))
		return nil
	}

	excessReleases := cullingCandidates[:excessCount]
	logger.Info("acquiring culling lease", "target", target, "limit", limit, "strategy", strategy, "excessCount", excessCount)
	acquired, err := r.acquireCullingLease(ctx, logger, namespace, target)
	if err != nil {
		return err
	}

	if !acquired {
		logger.Info("culling lease not acquired, skipping culling", "target", target)
		return nil
	}

	for _, releaseToDelete := range excessReleases {
		logger.Info("deleting release", "name", releaseToDelete.Name)
		err := r.Delete(ctx, &releaseToDelete)
		if err != nil {
			logger.Error(err, "failed to delete release", "name", releaseToDelete.Name)
			return err
		}
	}

	logger.Info("deleted excess releases", "count", len(excessReleases))
	return nil
}

// acquireCullingLease attempts to acquire a Lease for the given namespace/target.
// Returns true if the lease was acquired (caller should proceed with culling),
// false if another reconcile holds the lease (caller should skip culling).
func (r *ReleaseReconciler) acquireCullingLease(ctx context.Context, logger logr.Logger, namespace, target string) (bool, error) {
	leaseName := cullingLeaseName(target)
	now := metav1.NowMicro()
	leaseDuration := int32(5) // seconds
	holderID := fmt.Sprintf("%d", time.Now().UnixNano())

	lease := &coordinationv1.Lease{}
	err := r.Get(ctx, client.ObjectKey{Namespace: namespace, Name: leaseName}, lease)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return false, err
		}
		// Lease doesn't exist, create it
		lease = &coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      leaseName,
				Namespace: namespace,
			},
			Spec: coordinationv1.LeaseSpec{
				HolderIdentity:       &holderID,
				AcquireTime:          &now,
				RenewTime:            &now,
				LeaseDurationSeconds: &leaseDuration,
			},
		}
		if err := r.Create(ctx, lease); err != nil {
			if apierrors.IsAlreadyExists(err) {
				// Another reconcile just created it, skip
				return false, nil
			}
			return false, err
		}
		logger.Info("acquired culling lease", "lease", leaseName)
		return true, nil
	}

	// Lease exists, check if it's expired
	if lease.Spec.RenewTime != nil && lease.Spec.LeaseDurationSeconds != nil {
		expiry := lease.Spec.RenewTime.Time.Add(time.Duration(*lease.Spec.LeaseDurationSeconds) * time.Second)
		if time.Now().Before(expiry) {
			// Lease is still valid, skip culling
			return false, nil
		}
	}

	// Lease is expired, try to take it over
	lease.Spec.HolderIdentity = &holderID
	lease.Spec.AcquireTime = &now
	lease.Spec.RenewTime = &now
	lease.Spec.LeaseDurationSeconds = &leaseDuration
	if err := r.Update(ctx, lease); err != nil {
		if apierrors.IsConflict(err) {
			// Another reconcile updated it first, skip
			return false, nil
		}
		return false, err
	}

	logger.Info("acquired expired culling lease", "lease", leaseName)
	return true, nil
}

func cullingLeaseName(target string) string {
	hash := deploy.HashString([]byte(target))
	return fmt.Sprintf("theatre-release-cull-%s", hash)
}

func releasesWithRepeatingSignatures(releases []deployv1alpha1.Release) []deployv1alpha1.Release {
	signatureOccurrences := make(map[string]int)
	cullingCandidates := make([]deployv1alpha1.Release, 0)

	for _, release := range releases {
		signatureOccurrences[release.Status.Signature]++
	}

	for _, release := range releases {
		if signatureOccurrences[release.Status.Signature] > 1 {
			cullingCandidates = append(cullingCandidates, release)
		}
	}

	return cullingCandidates
}
