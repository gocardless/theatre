package deploy

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	"k8s.io/apimachinery/pkg/runtime"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
					return r.Reconcile(ctx, logger, request, obj.(*deployv1alpha1.Release))
				},
			),
		)
}

func (r *ReleaseReconciler) Reconcile(ctx context.Context, logger logr.Logger, req ctrl.Request, release *deployv1alpha1.Release) (ctrl.Result, error) {
	logger = logger.WithValues("namespace", req.Namespace, "release", release.Name)
	logger.Info("reconciling release")

	if !release.IsStatusInitialised() {
		logger.Info("release is new, will initialise")
		release.InitialiseStatus(MessageReleaseCreated)
		err := r.Status().Update(ctx, release)
		if err != nil {
			logger.Error(err, "failed to initialise release")
			return ctrl.Result{}, err
		}

		// refetch the release
		err = r.Client.Get(ctx, client.ObjectKey{Namespace: req.Namespace, Name: release.Name}, release)
		if err != nil {
			logger.Error(err, "failed to refetch release")
			return ctrl.Result{}, err
		}
	}

	err := r.handleAnnotations(ctx, logger, release)
	if err != nil {
		logger.Error(err, "failed to update status field of release")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// The current way to active releases is by setting the deployment end time. The
// release controller will activate the release with the latest deployment end
// time.
func (r *ReleaseReconciler) handleAnnotations(ctx context.Context, logger logr.Logger, release *deployv1alpha1.Release) error {
	logger.Info("handling annotations for release", "release", release.Name)
	modified := false

	// Handle theatre.gocardless.com/release-set-deploy-start-time annotation
	startTimeString, found := release.Annotations[deployv1alpha1.AnnotationKeyReleaseDeploymentStartTime]
	if !found || startTimeString == "" {
		if !release.Status.DeploymentStartTime.IsZero() {
			release.SetDeploymentStartTime(metav1.Time{})
			modified = true
		}
	} else {
		startTime, err := time.Parse(time.RFC3339, startTimeString)
		if err != nil {
			// When the annotation is set to an invalid time, we will not update the status
			logger.Error(err, "failed to parse deployment start time annotation", "annotation", release.Annotations[deployv1alpha1.AnnotationKeyReleaseDeploymentStartTime])
		} else if !release.Status.DeploymentStartTime.Time.UTC().Equal(startTime.UTC()) {
			release.SetDeploymentStartTime(metav1.NewTime(startTime))
			modified = true
		}
	}

	logger.Info("modified after start time parsing", "modified", modified)

	// Handle theatre.gocardless.com/release-set-deploy-end-time
	endTimeString, found := release.Annotations[deployv1alpha1.AnnotationKeyReleaseDeploymentEndTime]
	if !found || endTimeString == "" {
		if !release.Status.DeploymentEndTime.IsZero() {
			release.SetDeploymentEndTime(metav1.Time{})
			modified = true
		}
	} else {
		endTime, err := time.Parse(time.RFC3339, endTimeString)
		if err != nil {
			// When the annotation is set to an invalid time, we will not update the status
			logger.Error(err, "failed to parse deployment end time annotation", "annotation", release.Annotations[deployv1alpha1.AnnotationKeyReleaseDeploymentEndTime])
		} else if !release.Status.DeploymentEndTime.Time.UTC().Equal(endTime.UTC()) {
			release.SetDeploymentEndTime(metav1.NewTime(endTime))
			modified = true
		}
	}

	// Handle theatre.gocardless.com/release-active annotation
	activate, found := release.Annotations[deployv1alpha1.AnnotationKeyReleaseActivate]
	desiredActive := found && activate == deployv1alpha1.AnnotationValueReleaseActivateTrue
	if desiredActive != release.IsConditionActive() {
		if desiredActive {
			release.Activate("Release activated")
		} else {
			release.Deactivate("Release deactivated")
		}
		modified = true
	}

	// Handle theatre.gocardless.com/release-set-previous-release
	previousRelease, found := release.Annotations[deployv1alpha1.AnnotationKeyReleasePreviousRelease]
	if found {
		if previousRelease != release.GetPreviousRelease() {
			release.SetPreviousRelease(previousRelease)
			modified = true
		}
	} else if release.GetPreviousRelease() != "" {
		release.SetPreviousRelease("")
		modified = true
	}

	if modified {
		return r.Status().Update(ctx, release)
	}

	return nil
}
