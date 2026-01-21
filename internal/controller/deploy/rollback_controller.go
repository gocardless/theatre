package deploy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/cicd"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	pkgerrors "github.com/pkg/errors"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// Duration to wait before rechecking a deployment
	RequeueAfter = 15 * time.Second

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

	// Index releases by their active condition status for efficient lookups
	err := mgr.GetFieldIndexer().IndexField(
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
	if rollback.IsCompleted() {
		logger.Info("rollback already complete, skipping")
		return ctrl.Result{}, nil
	}

	// Fetch the target release to get revision information
	toRelease := &deployv1alpha1.Release{}
	if err := r.Get(ctx, client.ObjectKey{
		Namespace: rollback.Namespace,
		Name:      rollback.Spec.ToReleaseRef.Name,
	}, toRelease); err != nil {
		logger.Error(err, "failed to fetch target release", "toRelease", rollback.Spec.ToReleaseRef.Name)
		if apierrors.IsNotFound(err) {
			return r.markRollbackFailed(ctx, logger, rollback, fmt.Sprintf("target release %q not found", rollback.Spec.ToReleaseRef.Name))
		}
		return ctrl.Result{}, err
	}

	// Determine the current release (fromRelease) if not already set
	if rollback.Status.FromReleaseRef == (deployv1alpha1.ReleaseReference{}) {
		fromRelease, err := r.findActiveRelease(ctx, toRelease.ReleaseConfig.TargetName, rollback.Namespace)
		if err != nil {
			logger.Info("failed to find active release, continuing without fromRelease", "error", err)
		} else if fromRelease != nil {
			rollback.Status.FromReleaseRef = deployv1alpha1.ReleaseReference{Name: fromRelease.Name}
		}
	}

	if !meta.IsStatusConditionTrue(rollback.Status.Conditions, deployv1alpha1.RollbackConditionInProgress) {
		return r.triggerDeployment(ctx, logger, rollback, toRelease)
	}

	return r.pollDeploymentStatus(ctx, logger, rollback, toRelease)
}

func (r *RollbackReconciler) findActiveRelease(ctx context.Context, targetName, namespace string) (*deployv1alpha1.Release, error) {
	releaseList := &deployv1alpha1.ReleaseList{}
	if err := r.List(ctx, releaseList,
		client.InNamespace(namespace),
		client.MatchingFields{"status.conditions.active": string(metav1.ConditionTrue)},
	); err != nil {
		return nil, err
	}

	for _, release := range releaseList.Items {
		if release.ReleaseConfig.TargetName == targetName {
			return &release, nil
		}
	}
	return nil, nil
}

func (r *RollbackReconciler) statusUpdate(ctx context.Context, logger logr.Logger, rollback *deployv1alpha1.Rollback) error {
	outcome, err := recutil.CreateOrUpdate(ctx, r.Client, rollback, recutil.StatusDiff)
	if err != nil {
		return pkgerrors.Wrap(err, "failed to update rollback status")
	}
	if outcome == recutil.StatusUpdate {
		logger.Info("rollback status updated", "event", "SuccessfulUpdate")
	}
	return nil
}

func (r *RollbackReconciler) triggerDeployment(ctx context.Context, logger logr.Logger, rollback *deployv1alpha1.Rollback, toRelease *deployv1alpha1.Release) (ctrl.Result, error) {
	logger.Info("triggering deployment", "deployer", r.Deployer.Name(), "toRelease", toRelease.Name)

	if rollback.Status.AttemptCount >= MaxRetryAttempts {
		logger.Info("max retry attempts exceeded", "attempts", rollback.Status.AttemptCount)
		return r.markRollbackFailed(ctx, logger, rollback, "max retry attempts exceeded")
	}

	deployReq := cicd.DeploymentRequest{
		Rollback:  rollback,
		ToRelease: toRelease,
		Options:   rollback.Spec.DeploymentOptions,
	}

	// Update attempt tracking
	now := metav1.Now()
	rollback.Status.AttemptCount++

	if rollback.Status.StartTime == nil {
		rollback.Status.StartTime = &now
	}

	resp, err := r.Deployer.TriggerDeployment(ctx, deployReq)
	if err != nil {
		logger.Error(err, "failed to trigger deployment")

		// Check if error is retryable
		var deployerErr *cicd.DeployerError
		if errors.As(err, &deployerErr) && deployerErr.Retryable {
			rollback.Status.Message = fmt.Sprintf("deployment trigger failed (attempt %d/%d): %v", rollback.Status.AttemptCount, MaxRetryAttempts, err)
			if updateErr := r.statusUpdate(ctx, logger, rollback); updateErr != nil {
				return ctrl.Result{}, updateErr
			}
			return ctrl.Result{RequeueAfter: RequeueAfter}, nil
		}

		// Non-retryable error
		return r.markRollbackFailed(ctx, logger, rollback, fmt.Sprintf("deployment trigger failed: %v", err))
	}

	// Update status with deployment info
	rollback.Status.DeploymentID = resp.ID
	rollback.Status.DeploymentURL = resp.URL
	rollback.Status.Message = fmt.Sprintf("deployment triggered via %s", r.Deployer.Name())

	// Update InProgress condition to reflect successful trigger
	meta.SetStatusCondition(&rollback.Status.Conditions, metav1.Condition{
		Type:    deployv1alpha1.RollbackConditionInProgress,
		Status:  metav1.ConditionTrue,
		Reason:  "DeploymentTriggered",
		Message: fmt.Sprintf("Deployment %s triggered via %s", resp.ID, r.Deployer.Name()),
	})

	if err := r.statusUpdate(ctx, logger, rollback); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("deployment triggered successfully", "deploymentID", resp.ID, "url", resp.URL)
	return ctrl.Result{RequeueAfter: RequeueAfter}, nil
}

func (r *RollbackReconciler) pollDeploymentStatus(ctx context.Context, logger logr.Logger, rollback *deployv1alpha1.Rollback, toRelease *deployv1alpha1.Release) (ctrl.Result, error) {
	statusResp, err := r.Deployer.GetDeploymentStatus(ctx, rollback.Status.DeploymentID)
	if err != nil {
		logger.Error(err, "failed to get deployment status")
		// Continue polling on transient errors
		if deployerErr, ok := err.(*cicd.DeployerError); ok && deployerErr.Retryable {
			return ctrl.Result{RequeueAfter: RequeueAfter}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("deployment status", "status", statusResp.Status, "message", statusResp.Message)

	switch statusResp.Status {
	case cicd.DeploymentStatusSucceeded:
		return r.markRollbackSucceeded(ctx, logger, rollback, statusResp.Message)

	case cicd.DeploymentStatusFailed:
		// Check if we should retry
		if rollback.Status.AttemptCount < MaxRetryAttempts {
			logger.Info("deployment failed, retrying", "attempt", rollback.Status.AttemptCount, "maxAttempts", MaxRetryAttempts)
			rollback.Status.Message = fmt.Sprintf("deployment failed (attempt %d/%d): %s", rollback.Status.AttemptCount, MaxRetryAttempts, statusResp.Message)
			if err := r.statusUpdate(ctx, logger, rollback); err != nil {
				return ctrl.Result{}, err
			}
			return r.triggerDeployment(ctx, logger, rollback, toRelease)
		}
		// Max retries exceeded - terminal failure
		return r.markRollbackFailed(ctx, logger, rollback, statusResp.Message)

	case cicd.DeploymentStatusPending, cicd.DeploymentStatusInProgress:
		// Update message and continue polling
		rollback.Status.Message = statusResp.Message
		if err := r.statusUpdate(ctx, logger, rollback); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: RequeueAfter}, nil

	default:
		logger.Info("unknown deployment status, continuing to poll", "status", statusResp.Status)
		return ctrl.Result{RequeueAfter: RequeueAfter}, nil
	}
}

func (r *RollbackReconciler) markRollbackSucceeded(ctx context.Context, logger logr.Logger, rollback *deployv1alpha1.Rollback, message string) (ctrl.Result, error) {
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

	if err := r.statusUpdate(ctx, logger, rollback); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("rollback succeeded")
	return ctrl.Result{}, nil
}

// markRollbackFailed marks the rollback as terminally failed
func (r *RollbackReconciler) markRollbackFailed(ctx context.Context, logger logr.Logger, rollback *deployv1alpha1.Rollback, message string) (ctrl.Result, error) {
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

	if err := r.statusUpdate(ctx, logger, rollback); err != nil {
		return ctrl.Result{}, err
	}
	logger.Info("rollback failed", "message", message)
	return ctrl.Result{}, nil
}
