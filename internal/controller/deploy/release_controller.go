package deploy

import (
	"context"
	"fmt"
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

// var ErrCannotSupersedeNewRelease = errors.New("cannot supersede a new release that has no phase set")

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

// func (r *ReleaseReconciler) trimExtraReleases(logger logr.Logger, ctx context.Context, namespace string, target string) error {
// 	releases := &deployv1alpha1.ReleaseList{}
// 	err := r.List(ctx, releases,
// 		client.InNamespace(namespace),
// 		client.MatchingFields(map[string]string{
// 			"config.targetName": target,
// 		}),
// 	)

// 	if err != nil {
// 		return err
// 	}

// 	if len(releases.Items) <= r.MaxReleasesPerTarget {
// 		return nil
// 	}

// 	sort.Slice(releases.Items, func(i, j int) bool {
// 		iActive := releases.Items[i].Status.Phase == deployv1alpha1.PhaseActive
// 		jActive := releases.Items[j].Status.Phase == deployv1alpha1.PhaseActive

// 		// If phases differ, Active comes first
// 		if iActive != jActive {
// 			return iActive
// 		}

// 		// If both have same phase, sort by LastAppliedTime descending (newer first)
// 		return releases.Items[j].Status.DeploymentEndTime.Before(&releases.Items[i].Status.DeploymentEndTime)
// 	})

// 	// trim releases to max
// 	releasesToDelete := releases.Items[r.MaxReleasesPerTarget:]

// 	for _, releaseToDelete := range releasesToDelete {
// 		logger.Info("deleting release", "release", releaseToDelete.Name)
// 		err := r.Delete(ctx, &releaseToDelete)
// 		if err != nil {
// 			logger.Error(err, "failed to delete release", "release", releaseToDelete.Name)
// 			return err
// 		}
// 	}

// 	return nil
// }

// func isNewRelease(phase deployv1alpha1.Phase) bool {
// 	return phase == ""
// }

// func (r *ReleaseReconciler) prependToHistory(release *deployv1alpha1.Release) {
// 	entry := deployv1alpha1.ReleaseStatusEntry{
// 		Phase:               release.Status.Phase,
// 		Message:             release.Status.Message,
// 		DeploymentStartTime: release.Status.DeploymentStartTime,
// 		DeploymentEndTime:   release.Status.DeploymentEndTime,
// 		SupersededBy:        release.Status.SupersededBy,
// 		SupersededTime:      release.Status.SupersededTime,
// 	}

// 	release.Status.History = append([]deployv1alpha1.ReleaseStatusEntry{entry}, release.Status.History...)

// 	if len(release.Status.History) > r.MaxHistoryLimit {
// 		release.Status.History = release.Status.History[len(release.Status.History)-r.MaxHistoryLimit:]
// 	}
// }

// func (r *ReleaseReconciler) markReleaseSuperseded(ctx context.Context, release *deployv1alpha1.Release, supersededBy string) error {
// 	release.Status.SupersededBy = supersededBy
// 	release.Status.SupersededTime = metav1.Now()
// 	release.Status.Phase = deployv1alpha1.PhaseInactive
// 	release.Status.Message = fmt.Sprintf(MessageReleaseSuperseded, supersededBy)
// 	return r.Status().Update(ctx, release)
// }

// func (r *ReleaseReconciler) supersedePreviousReleases(ctx context.Context, activeRelease *deployv1alpha1.Release) error {
// 	// Mark all other releases as inactive
// 	var releaseList deployv1alpha1.ReleaseList
// 	err := r.List(ctx, &releaseList,
// 		client.InNamespace(activeRelease.Namespace),
// 		client.MatchingFields(map[string]string{
// 			"config.targetName": activeRelease.ReleaseConfig.TargetName,
// 			"status.phase":      string(deployv1alpha1.PhaseActive),
// 		}),
// 	)

// 	if err != nil {
// 		return err
// 	}

// 	for _, otherRelease := range releaseList.Items {
// 		if otherRelease.Name != activeRelease.Name {
// 			if otherRelease.Status.Phase == deployv1alpha1.PhaseActive {
// 				err := r.markReleaseSuperseded(ctx, &otherRelease, activeRelease.Name)
// 				if err != nil {
// 					return err
// 				}
// 			}
// 		}
// 	}

// 	return nil
// }

// func (r *ReleaseReconciler) initRelease(ctx context.Context, release *deployv1alpha1.Release) error {
// 	release.Status.Phase = deployv1alpha1.PhaseInactive
// 	release.Status.Message = ""
// 	release.Status.HealthStatus = deployv1alpha1.HealthStatusUnknown

// 	return r.Status().Update(ctx, release)
// }

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

func (r *ReleaseReconciler) handleAnnotations(ctx context.Context, logger logr.Logger, release *deployv1alpha1.Release) error {
	modified := false

	logger.Info("handling annotations")

	if release.AnnotatedWithActivate() {
		logger.Info("activating release", "release", release.Name)
		activeRelease, err := r.findActiveRelease(ctx, release.Namespace, release.ReleaseConfig.TargetName)
		if err != nil {
			return err
		}
		if activeRelease != nil {
			messageSuperseded := fmt.Sprintf(MessageReleaseSuperseded, release.Name)
			activeRelease.Deactivate(messageSuperseded, *release)
			err = r.updateReleaseStatus(ctx, activeRelease)
			if err != nil {
				return err
			}
		}

		release.Activate(MessageReleaseActive, activeRelease)
		delete(release.Annotations, deployv1alpha1.AnnotationKeyReleaseActivate)

		if err != nil {
			return err
		}

		modified = true
	}

	if release.AnnotatedWithSetDeploymentStartTime() {
		startTime, err := time.Parse(time.RFC3339, release.Annotations[deployv1alpha1.AnnotationKeyReleaseSetDeploymentStartTime])
		if err != nil {
			return err
		}
		release.Status.DeploymentStartTime = metav1.NewTime(startTime)
		delete(release.Annotations, deployv1alpha1.AnnotationKeyReleaseSetDeploymentStartTime)
		modified = true
	}

	if release.AnnotatedWithSetDeploymentEndTime() {
		endTime, err := time.Parse(time.RFC3339, release.Annotations[deployv1alpha1.AnnotationKeyReleaseSetDeploymentEndTime])
		if err != nil {
			return err
		}
		release.Status.DeploymentEndTime = metav1.NewTime(endTime)
		delete(release.Annotations, deployv1alpha1.AnnotationKeyReleaseSetDeploymentEndTime)
		modified = true
	}

	if modified {
		releaseStatus := release.Status

		err := r.Update(ctx, release)
		if err != nil {
			logger.Error(err, "failed to update resource")
			return err
		}

		return retry.RetryOnConflict(retry.DefaultRetry, func() error {
			updatedRelease := &deployv1alpha1.Release{}
			err = r.Get(ctx, client.ObjectKeyFromObject(release), updatedRelease)
			if err != nil {
				logger.Error(err, "failed to retrieve object from API")
				return err
			}

			updatedRelease.Status = releaseStatus

			return r.updateReleaseStatus(ctx, updatedRelease)
		})
	}

	return nil
}

func (r *ReleaseReconciler) initialiseReleaseStatus(ctx context.Context, release *deployv1alpha1.Release) error {
	release.InitialiseStatus(MessageReleaseCreated)
	return r.updateReleaseStatus(ctx, release)
}

func (r *ReleaseReconciler) updateReleaseStatus(ctx context.Context, release *deployv1alpha1.Release) error {
	release.UpdateObservedGeneration()
	return r.Status().Update(ctx, release)
}

func (r *ReleaseReconciler) Reconcile(ctx context.Context, logger logr.Logger, req ctrl.Request, release *deployv1alpha1.Release) (ctrl.Result, error) {
	logger = logger.WithValues("namespace", req.Namespace, "release", release.Name)
	logger.Info("reconciling release")

	if release.DeletionTimestamp != nil {
		logger.Info("release marked for deletion, skipping reconciliation")
		return ctrl.Result{}, nil
	}

	if !release.IsStatusInitialised() {
		logger.Info("release is new, will initialise")

		err := r.initialiseReleaseStatus(ctx, release)
		if err != nil {
			logger.Error(err, "failed to initialise release")
			return ctrl.Result{}, err
		}
	}

	err := r.handleAnnotations(ctx, logger, release)

	if err != nil {
		logger.Error(err, "failed to update status field of release")
	}

	// if isNewRelease(release.Status.Phase) {
	// 	err := r.markReleaseActive(ctx, release)
	// 	if err != nil {
	// 		logger.Error(err, "failed to mark release active")
	// 		return ctrl.Result{}, err
	// 	}

	// 	err = r.supersedePreviousReleases(ctx, release)
	// 	if err != nil {
	// 		logger.Error(err, "failed to supersede previous releases")
	// 		return ctrl.Result{}, err
	// 	}

	// err = r.trimExtraReleases(logger, ctx, req.Namespace, release.ReleaseConfig.TargetName)
	// if err != nil {
	// 	logger.Error(err, "failed to trim extra releases")
	// 	return ctrl.Result{}, err
	// }

	return ctrl.Result{}, nil
}
