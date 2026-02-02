package deploy

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	analysisv1alpha1 "github.com/akuity/kargo/api/stubs/rollouts/v1alpha1"
	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/logging"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	DefaultCullingStrategy = deployv1alpha1.AnnotationValueCullingStrategyEndTime
	DefaultMaxReleaseCount = 10

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

func (r *ReleaseReconciler) cullConfig(ctx context.Context, logger logr.Logger, namespace string) (maxReleasesPerTarget int, cullingStrategy string, err error) {
	maxReleasesPerTarget = DefaultMaxReleaseCount
	cullingStrategy = DefaultCullingStrategy

	var namespaceObj corev1.Namespace
	if err := r.Client.Get(ctx, client.ObjectKey{Name: namespace}, &namespaceObj); err != nil {
		return 0, "", err
	}

	if mpt, ok := namespaceObj.Annotations[deployv1alpha1.AnnotationKeyMaxReleasesPerTarget]; ok {
		newMaxReleasesPerTarget, err := strconv.Atoi(mpt)
		if err != nil {
			logger.Error(err, fmt.Sprintf("invalid max releases per target annotation value, defaulting to %d", DefaultMaxReleaseCount),
				"annotation", deployv1alpha1.AnnotationKeyMaxReleasesPerTarget, "value", mpt)
		} else {
			maxReleasesPerTarget = newMaxReleasesPerTarget
		}
	}

	if cs, ok := namespaceObj.Annotations[deployv1alpha1.AnnotationKeyCullingStrategy]; ok {
		if cs == deployv1alpha1.AnnotationValueCullingStrategyEndTime || cs == deployv1alpha1.AnnotationValueCullingStrategySignature {
			cullingStrategy = cs
		} else {
			logger.Error(fmt.Errorf("unsupported culling strategy"), fmt.Sprintf("%s=%s is not a valid culling strategy, defaulting to %s", deployv1alpha1.AnnotationKeyCullingStrategy, cs, DefaultCullingStrategy),
				"annotation", deployv1alpha1.AnnotationKeyCullingStrategy, "value", cs)
		}
	}

	return maxReleasesPerTarget, cullingStrategy, nil
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
	maxReleasesPerTarget, cullingStrategy, err := r.cullConfig(ctx, logger, namespace)
	if err != nil {
		return err
	}

	releaseList := &deployv1alpha1.ReleaseList{}
	matchFields := client.MatchingFields(map[string]string{"config.targetName": target})
	if err := r.List(ctx, releaseList, client.InNamespace(namespace), matchFields); err != nil {
		return err
	}

	if len(releaseList.Items) < maxReleasesPerTarget {
		// No culling needed
		return nil
	}

	inactiveReleases := []deployv1alpha1.Release{}
	for _, release := range releaseList.Items {
		if !release.IsConditionActiveTrue() {
			inactiveReleases = append(inactiveReleases, release)
		}
	}

	logger.Info("found inactive releases", "count", len(releaseList.Items))
	// excessReleaseCount is (active releases + inactive releases) - max releases
	excessCount := len(releaseList.Items) - maxReleasesPerTarget

	signatureOccurrences := make(map[string]int)
	cullingCandidates := make([]deployv1alpha1.Release, 0)

	if cullingStrategy == deployv1alpha1.AnnotationValueCullingStrategySignature {
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
	releasesToDelete := cullingCandidates[:excessCount]

	for _, releaseToDelete := range releasesToDelete {
		err := r.Delete(ctx, &releaseToDelete)
		if err != nil {
			logger.Error(err, "failed to delete release", "release", releaseToDelete.Name)
			return err
		}
	}

	logger.Info("deleted excess releases", "count", len(releasesToDelete))
	return nil
}
