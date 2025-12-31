package deploy

import (
	"context"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/cicd"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Duration to wait before rechecking a pending deployment
	RequeueAfterPending = 10 * time.Second

	// Duration to wait before rechecking an in-progress deployment
	RequeueAfterInProgress = 15 * time.Second

	// Maximum number of times to retry triggering a deployment
	MaxRetryAttempts = 3
)

type RollbackReconciler struct {
	client.Client
	Log                  logr.Logger
	Scheme               *runtime.Scheme
	RollbackHistoryLimit int
	Deployer             cicd.Deployer
}

func (r *RollbackReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := r.Log.WithValues("component", "Rollback")

	return ctrl.NewControllerManagedBy(mgr).
		For(&deployv1alpha1.Rollback{}).
		Complete(
			recutil.ResolveAndReconcile(
				ctx, logger, mgr, &deployv1alpha1.Rollback{},
				func(logger logr.Logger, request ctrl.Request, obj runtime.Object) (ctrl.Result, error) {
					return r.Reconcile(ctx, logger, request, obj.(*deployv1alpha1.Rollback))
				},
			),
		)
}

func (r *RollbackReconciler) Reconcile(ctx context.Context, logger logr.Logger, req ctrl.Request, rollback *deployv1alpha1.Rollback) (ctrl.Result, error) {
	logger = logger.WithValues("namespace", req.Namespace, "rollback", rollback.Name)
	logger.Info("reconciling rollback")

	// Check if rollback has already completed (succeeded or failed terminally)
	if r.isRollbackComplete(rollback) {
		logger.Info("rollback already complete, skipping")
		return ctrl.Result{}, nil
	}

	// Fetch the target release to get revision information
	toRelease := &deployv1alpha1.Release{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: rollback.Namespace,
		Name:      rollback.Spec.ToReleaseName,
	}, toRelease); err != nil {
		logger.Error(err, "failed to fetch target release", "toRelease", rollback.Spec.ToReleaseName)
		return ctrl.Result{}, err
	}

	// Determine the current release (fromRelease) if not already set
	if rollback.Status.FromReleaseName == "" {
		fromRelease, err := r.findActiveRelease(ctx, toRelease.ReleaseConfig.TargetName, rollback.Namespace)
		if err != nil {
			logger.Error(err, "failed to find active release")
			return ctrl.Result{}, err
		}
		if fromRelease != nil {
			rollback.Status.FromReleaseName = fromRelease.Name
		}
	}

	inProgressCondition := meta.FindStatusCondition(rollback.Status.Conditions, deployv1alpha1.RollbackConditionInProgress)

	if inProgressCondition == nil || inProgressCondition.Status == metav1.ConditionFalse {
		// Deployment not yet triggered - trigger it
		return r.triggerDeployment(ctx, logger, rollback, toRelease)
	}

	// Deployment is in progress - poll for status
	return r.pollDeploymentStatus(ctx, logger, rollback)
}

func (r *RollbackReconciler) isRollbackComplete(rollback *deployv1alpha1.Rollback) bool {
	succeededCondition := meta.FindStatusCondition(rollback.Status.Conditions, deployv1alpha1.RollbackConditionSucceded)
	if succeededCondition == nil {
		return false
	}
	// Terminal if explicitly succeeded or failed (with max retries exceeded)
	if succeededCondition.Status == metav1.ConditionTrue {
		return true
	}
	// Check if we've exceeded max retries
	if rollback.Status.AttemptCount >= MaxRetryAttempts && succeededCondition.Status == metav1.ConditionFalse {
		return true
	}
	return false
}

// TODO: move this into api/deploy/v1alpha1/release_helpers.go once release reconciler PR is merged
func (r *RollbackReconciler) findActiveRelease(ctx context.Context, targetName, namespace string) (*deployv1alpha1.Release, error) {
	releaseList := &deployv1alpha1.ReleaseList{}
	if err := r.List(ctx, releaseList, client.InNamespace(namespace)); err != nil {
		return nil, err
	}

	for _, release := range releaseList.Items {
		if release.ReleaseConfig.TargetName != targetName {
			continue
		}
		activeCondition := meta.FindStatusCondition(release.Status.Conditions, deployv1alpha1.ReleaseConditionActive)
		if activeCondition != nil && activeCondition.Status == metav1.ConditionTrue {
			return &release, nil
		}
	}
	return nil, nil
}

func (r *RollbackReconciler) triggerDeployment(ctx context.Context, logger logr.Logger, rollback *deployv1alpha1.Rollback, toRelease *deployv1alpha1.Release) (ctrl.Result, error) {
	logger.Info("triggering deployment", "deployer", r.Deployer.Name(), "toRelease", toRelease.Name)

	if rollback.Status.AttemptCount >= MaxRetryAttempts {
		logger.Info("max retry attempts exceeded", "attempts", rollback.Status.AttemptCount)
		return r.markRollbackFailed(ctx, rollback, "max retry attempts exceeded")
	}

	deployReq := cicd.DeploymentRequest{
		Rollback:  rollback,
		ToRelease: toRelease,
		Options:   rollback.Spec.DeploymentOptions,
	}

	// Update attempt tracking
	now := metav1.Now()
	rollback.Status.AttemptCount++
	rollback.Status.LastHTTPCallTime = &now

	if rollback.Status.StartTime == nil {
		rollback.Status.StartTime = &now
	}

	resp, err := r.Deployer.TriggerDeployment(ctx, deployReq)
	if err != nil {
		logger.Error(err, "failed to trigger deployment")

		// Check if error is retryable
		if deployerErr, ok := err.(*cicd.DeployerError); ok && deployerErr.Retryable {
			rollback.Status.Message = fmt.Sprintf("deployment trigger failed (attempt %d/%d): %v", rollback.Status.AttemptCount, MaxRetryAttempts, err)
			if updateErr := r.Status().Update(ctx, rollback); updateErr != nil {
				logger.Error(updateErr, "failed to update rollback status")
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{RequeueAfter: RequeueAfterPending}, nil
		}

		// Non-retryable error
		return r.markRollbackFailed(ctx, rollback, fmt.Sprintf("deployment trigger failed: %v", err))
	}

	// Update status with deployment info
	rollback.Status.DeploymentID = resp.ID
	rollback.Status.DeploymentURL = resp.URL
	rollback.Status.Message = fmt.Sprintf("deployment triggered via %s", r.Deployer.Name())

	// Set InProgress condition
	meta.SetStatusCondition(&rollback.Status.Conditions, metav1.Condition{
		Type:               deployv1alpha1.RollbackConditionInProgress,
		Status:             metav1.ConditionTrue,
		Reason:             "DeploymentTriggered",
		Message:            fmt.Sprintf("Deployment %s triggered via %s", resp.ID, r.Deployer.Name()),
		LastTransitionTime: now,
	})

	if err := r.Status().Update(ctx, rollback); err != nil {
		logger.Error(err, "failed to update rollback status")
		return ctrl.Result{}, err
	}

	logger.Info("deployment triggered successfully", "deploymentID", resp.ID, "url", resp.URL)
	return ctrl.Result{RequeueAfter: RequeueAfterInProgress}, nil
}

func (r *RollbackReconciler) pollDeploymentStatus(ctx context.Context, logger logr.Logger, rollback *deployv1alpha1.Rollback) (ctrl.Result, error) {
	statusResp, err := r.Deployer.GetDeploymentStatus(ctx, rollback.Status.DeploymentID)
	if err != nil {
		logger.Error(err, "failed to get deployment status")
		// Continue polling on transient errors
		if deployerErr, ok := err.(*cicd.DeployerError); ok && deployerErr.Retryable {
			return ctrl.Result{RequeueAfter: RequeueAfterInProgress}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("deployment status", "status", statusResp.Status, "message", statusResp.Message)

	now := metav1.Now()

	switch statusResp.Status {
	case cicd.DeploymentStatusSucceeded:
		return r.markRollbackSucceeded(ctx, rollback, statusResp.Message)

	case cicd.DeploymentStatusFailed:
		return r.markRollbackFailed(ctx, rollback, statusResp.Message)

	case cicd.DeploymentStatusPending, cicd.DeploymentStatusInProgress:
		// Update message and continue polling
		rollback.Status.Message = statusResp.Message
		rollback.Status.LastHTTPCallTime = &now
		if err := r.Status().Update(ctx, rollback); err != nil {
			logger.Error(err, "failed to update rollback status")
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: RequeueAfterInProgress}, nil

	default:
		logger.Info("unknown deployment status, continuing to poll", "status", statusResp.Status)
		return ctrl.Result{RequeueAfter: RequeueAfterInProgress}, nil
	}
}

func (r *RollbackReconciler) markRollbackSucceeded(ctx context.Context, rollback *deployv1alpha1.Rollback, message string) (ctrl.Result, error) {
	now := metav1.Now()
	rollback.Status.CompletionTime = &now
	rollback.Status.Message = message

	meta.SetStatusCondition(&rollback.Status.Conditions, metav1.Condition{
		Type:               deployv1alpha1.RollbackConditionInProgress,
		Status:             metav1.ConditionFalse,
		Reason:             "Completed",
		Message:            "Rollback deployment completed",
		LastTransitionTime: now,
	})

	meta.SetStatusCondition(&rollback.Status.Conditions, metav1.Condition{
		Type:               deployv1alpha1.RollbackConditionSucceded,
		Status:             metav1.ConditionTrue,
		Reason:             "DeploymentSucceeded",
		Message:            message,
		LastTransitionTime: now,
	})

	if err := r.Status().Update(ctx, rollback); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *RollbackReconciler) markRollbackFailed(ctx context.Context, rollback *deployv1alpha1.Rollback, message string) (ctrl.Result, error) {
	now := metav1.Now()
	rollback.Status.CompletionTime = &now
	rollback.Status.Message = message

	meta.SetStatusCondition(&rollback.Status.Conditions, metav1.Condition{
		Type:               deployv1alpha1.RollbackConditionInProgress,
		Status:             metav1.ConditionFalse,
		Reason:             "Failed",
		Message:            "Rollback deployment failed",
		LastTransitionTime: now,
	})

	meta.SetStatusCondition(&rollback.Status.Conditions, metav1.Condition{
		Type:               deployv1alpha1.RollbackConditionSucceded,
		Status:             metav1.ConditionFalse,
		Reason:             "DeploymentFailed",
		Message:            message,
		LastTransitionTime: now,
	})

	if err := r.Status().Update(ctx, rollback); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}
