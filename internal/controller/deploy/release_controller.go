package deploy

import (
	"context"
	"time"

	analysisv1alpha1 "github.com/akuity/kargo/api/stubs/rollouts/v1alpha1"
	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/logging"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	"k8s.io/apimachinery/pkg/runtime"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
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

	outcome, err := recutil.CreateOrUpdate(ctx, r.Client, release, recutil.StatusDiff)
	if err != nil {
		logger.Error(err, "failed to update release status")
		return ctrl.Result{}, err
	}

	switch outcome {
	case recutil.StatusUpdate:
		logger.Info("Updated release status", "event", EventSuccessfulStatusUpdate)
	case recutil.None:
		logging.WithNoRecord(logger).Info("No status update needed", "event", EventNoStatusUpdate)
	default:
		logger.Info("Unexpected outcome from CreateOrUpdate", "outcome", outcome)
	}

	return ctrl.Result{}, nil
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
