package deploy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/logging"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

var (
	ErrNoRollbackPolicyFound = errors.New("no rollback policies found for target")
	ErrNoActiveReleaseFound  = errors.New("no active release found for target")
)

const (
	// Events
	EventErrorGettingRollbackPolicy = "ErrorGettingRollbackPolicy"
	EventAutomatedRollbackTriggered = "AutomatedRollbackTriggered"
)

type AutomatedRollbackReconciler struct {
	client.Client
	Log                logr.Logger
	Scheme             *runtime.Scheme
	ServiceAccountName string
}

func (r *AutomatedRollbackReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := r.Log.WithValues("component", "AutomatedRollback")

	ctrlBuilder := ctrl.NewControllerManagedBy(mgr).
		For(&deployv1alpha1.AutomatedRollbackPolicy{}).
		Watches(&deployv1alpha1.Release{},
			handler.EnqueueRequestsFromMapFunc(r.mapReleaseToPolicy(ctx, mgr))).
		WithEventFilter(r.onReleaseConditionsChangedPredicate())

	err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&deployv1alpha1.AutomatedRollbackPolicy{},
		IndexFieldPolicyTargetName,
		func(rawObj client.Object) []string {
			policy := rawObj.(*deployv1alpha1.AutomatedRollbackPolicy)
			return []string{policy.Spec.TargetName}
		},
	)
	if err != nil {
		return err
	}

	err = mgr.GetFieldIndexer().IndexField(
		ctx,
		&deployv1alpha1.Rollback{},
		IndexFieldOwner,
		func(rawObj client.Object) []string {
			run := rawObj.(*deployv1alpha1.Rollback)
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

	err = mgr.GetFieldIndexer().IndexField(
		ctx,
		&deployv1alpha1.Release{},
		IndexFieldReleaseTarget,
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
			ctx, logger, mgr, &deployv1alpha1.AutomatedRollbackPolicy{},
			func(logger logr.Logger, request ctrl.Request, obj runtime.Object) (ctrl.Result, error) {
				return r.Reconcile(ctx, logger, request, obj.(*deployv1alpha1.AutomatedRollbackPolicy))
			},
		),
	)
}

func (r *AutomatedRollbackReconciler) Reconcile(ctx context.Context, logger logr.Logger, request ctrl.Request, policy *deployv1alpha1.AutomatedRollbackPolicy) (ctrl.Result, error) {
	logger = logger.WithValues("namespace", request.Namespace, "target", policy.Spec.TargetName, "policy", policy.Name)
	logger.Info("Reconcile")

	// TODO: resetOnRecovery logic will be added here
	evaluation := r.evaluatePolicyStatus(policy)
	if !evaluation.allowed {
		logger.Info("rollback is not allowed, nothing to do", "reason", evaluation.reason)
		return r.updatePolicyAndReturn(ctx, logger, policy, evaluation.requeueAfter)
	}

	// Check if rollback should be triggered
	shouldTrigger, release, err := r.shouldTriggerRollback(ctx, logger, policy)
	if err != nil {
		return ctrl.Result{}, err
	}
	if !shouldTrigger {
		return r.updatePolicyAndReturn(ctx, logger, policy, nil)
	}

	// Create rollback and update policy
	if err := r.performRollback(ctx, logger, policy, release); err != nil {
		return ctrl.Result{}, err
	}

	return r.updatePolicyAndReturn(ctx, logger, policy, nil)
}

func (r *AutomatedRollbackReconciler) getActiveReleaseForTarget(ctx context.Context, policy *deployv1alpha1.AutomatedRollbackPolicy) (*deployv1alpha1.Release, error) {
	releaseList := &deployv1alpha1.ReleaseList{}
	if err := r.List(ctx, releaseList,
		client.InNamespace(policy.Namespace),
		client.MatchingFields{IndexFieldReleaseTarget: policy.Spec.TargetName},
	); err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	activeRelease := deployv1alpha1.FindActiveRelease(releaseList)

	if activeRelease != nil {
		return activeRelease, nil
	}

	return nil, ErrNoActiveReleaseFound
}

func (r *AutomatedRollbackReconciler) createOrUpdate(ctx context.Context, logger logr.Logger, object client.Object, objectType string) error {
	outcome, err := recutil.CreateOrUpdate(ctx, r.Client, object, recutil.StatusDiff)
	if err != nil {
		logger.Error(err, fmt.Sprintf("failed to update %s status", objectType))
		return err
	}

	switch outcome {
	case recutil.Create:
		logger.Info("created", objectType, object.GetName(), "event", EventCreated)
	case recutil.StatusUpdate:
		logger.Info("status updated", objectType, object.GetName(), "event", EventSuccessfulStatusUpdate)
	case recutil.Update:
		logger.Info("updated", objectType, object.GetName(), "event", EventSuccessfulUpdate)
	case recutil.None:
		logging.WithNoRecord(logger).Info("No status update needed", "event", EventNoStatusUpdate)
	default:
		logger.Info("Unexpected outcome from CreateOrUpdate", "outcome", outcome)
	}

	return nil
}

func (r *AutomatedRollbackReconciler) updatePolicyAndReturn(ctx context.Context, logger logr.Logger, policy *deployv1alpha1.AutomatedRollbackPolicy, requeueAfter *time.Duration) (ctrl.Result, error) {
	if err := r.createOrUpdate(ctx, logger, policy, "policy"); err != nil {
		return ctrl.Result{}, err
	}

	if requeueAfter != nil {
		return ctrl.Result{RequeueAfter: *requeueAfter}, nil
	}

	return ctrl.Result{}, nil
}

func (r *AutomatedRollbackReconciler) onReleaseConditionsChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		UpdateFunc: func(e event.UpdateEvent) bool {
			if _, isPolicy := e.ObjectNew.(*deployv1alpha1.AutomatedRollbackPolicy); isPolicy {
				return true
			}

			if release, isRelease := e.ObjectNew.(*deployv1alpha1.Release); isRelease {
				return isRelease && release.IsConditionActiveTrue()
			}

			return false
		},
		CreateFunc: func(e event.CreateEvent) bool {
			if _, isPolicy := e.Object.(*deployv1alpha1.AutomatedRollbackPolicy); isPolicy {
				return true
			}

			if release, isRelease := e.Object.(*deployv1alpha1.Release); isRelease {
				return isRelease && release.IsConditionActiveTrue()
			}

			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, isPolicy := e.Object.(*deployv1alpha1.AutomatedRollbackPolicy)
			return isPolicy
		},
	}
}

func (r *AutomatedRollbackReconciler) mapReleaseToPolicy(ctx context.Context, mgr ctrl.Manager) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		release := obj.(*deployv1alpha1.Release)

		policyList := &deployv1alpha1.AutomatedRollbackPolicyList{}
		err := mgr.GetClient().List(
			ctx,
			policyList,
			client.InNamespace(release.Namespace),
			client.MatchingFields(map[string]string{IndexFieldPolicyTargetName: release.TargetName}))

		if err != nil || len(policyList.Items) != 1 {
			// Do nothing on error or when none or multiple policies for a targetName
			return nil
		}

		return []reconcile.Request{{
			NamespacedName: client.ObjectKeyFromObject(&policyList.Items[0]),
		}}
	}
}

type policyEvaluation struct {
	allowed       bool
	reason        string
	message       string
	requeueAfter  *time.Duration
	windowExpired bool
}

func (r *AutomatedRollbackReconciler) evaluatePolicyStatus(policy *deployv1alpha1.AutomatedRollbackPolicy) policyEvaluation {
	result := evaluatePolicyConstraints(policy)

	if result.windowExpired {
		policy.Status.WindowStartTime = nil
		policy.Status.ConsecutiveCount = 0
	}

	status := metav1.ConditionFalse
	if result.allowed {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:    deployv1alpha1.AutomatedRollbackPolicyConditionActive,
		Status:  status,
		Reason:  result.reason,
		Message: result.message,
	})

	return result
}

func evaluatePolicyConstraints(policy *deployv1alpha1.AutomatedRollbackPolicy) policyEvaluation {
	if !policy.Spec.Enabled {
		return policyEvaluation{
			allowed: false,
			reason:  deployv1alpha1.AutomatedRollbackPolicyReasonSetByUser,
			message: "Automated rollback policy is disabled",
		}
	}

	// Check if we're within the reset period window
	// If lastAutomatedRollbackTime + resetPeriod has passed, the counter effectively resets
	withinResetPeriod := policy.Spec.ResetPeriod != nil &&
		policy.Status.WindowStartTime != nil &&
		policy.Status.WindowStartTime.Time.Add(policy.Spec.ResetPeriod.Duration).After(time.Now())

	// Only enforce maxConsecutiveRollbacks if we're within the reset period
	if withinResetPeriod {
		isMaxConsecutiveRollbacksReached := policy.Spec.MaxConsecutiveRollbacks != nil && policy.Status.ConsecutiveCount >= *policy.Spec.MaxConsecutiveRollbacks
		if isMaxConsecutiveRollbacksReached {
			resetEndTime := policy.Status.WindowStartTime.Add(policy.Spec.ResetPeriod.Duration)
			resetAtMessage := fmt.Sprintf("Will be enabled again at %s", resetEndTime.Format(time.RFC3339))
			message := fmt.Sprintf("Max consecutive rollbacks (%d) reached within reset period. %s", *policy.Spec.MaxConsecutiveRollbacks, resetAtMessage)
			requeueAfter := time.Until(resetEndTime)
			reason := deployv1alpha1.AutomatedRollbackPolicyReasonDisabledByController

			return policyEvaluation{
				allowed:      false,
				reason:       reason,
				message:      message,
				requeueAfter: &requeueAfter,
			}
		}
	}

	// Check minimum interval between rollbacks
	if policy.Spec.MinInterval != nil &&
		policy.Status.LastAutomatedRollbackTime != nil &&
		policy.Status.LastAutomatedRollbackTime.Add(policy.Spec.MinInterval.Duration).After(time.Now()) {
		minIntervalEndTime := policy.Status.LastAutomatedRollbackTime.Add(policy.Spec.MinInterval.Duration)
		minIntervalMessage := fmt.Sprintf("Will be enabled again at %s", minIntervalEndTime.Format(time.RFC3339))

		reason := deployv1alpha1.AutomatedRollbackPolicyReasonDisabledByController
		message := fmt.Sprintf("Min interval (%s) between rollbacks not met. %s", policy.Spec.MinInterval.Duration, minIntervalMessage)
		requeueAfter := time.Until(minIntervalEndTime)

		return policyEvaluation{
			allowed:      false,
			reason:       reason,
			message:      message,
			requeueAfter: &requeueAfter,
		}
	}

	return policyEvaluation{
		allowed:       true,
		reason:        deployv1alpha1.AutomatedRollbackPolicyReasonSetByUser,
		message:       "Automated rollback is enabled",
		windowExpired: !withinResetPeriod && policy.Status.WindowStartTime != nil,
	}
}

func (r *AutomatedRollbackReconciler) shouldTriggerRollback(ctx context.Context, logger logr.Logger, policy *deployv1alpha1.AutomatedRollbackPolicy) (bool, *deployv1alpha1.Release, error) {
	// Find active release
	release, err := r.getActiveReleaseForTarget(ctx, policy)
	if err != nil {
		if errors.Is(err, ErrNoActiveReleaseFound) {
			logger.Info("no active release found, exiting...")
			return false, nil, nil
		}
		return false, nil, err
	}

	// Check if release already has a rollback
	hasRollback, err := r.hasRollback(ctx, release)
	if err != nil {
		return false, nil, err
	}

	if hasRollback {
		logger.Info("release already has a rollback, nothing to do", "release", release.Name)
		return false, nil, nil
	}

	// Check trigger condition
	triggerConditionType := policy.Spec.Trigger.ConditionType
	triggerConditionStatus := policy.Spec.Trigger.ConditionStatus
	if !meta.IsStatusConditionPresentAndEqual(release.Status.Conditions, triggerConditionType, triggerConditionStatus) {
		logger.Info("trigger condition not met, nothing to do", "release", release.Name)
		return false, nil, nil
	}

	return true, release, nil
}

func (r *AutomatedRollbackReconciler) performRollback(ctx context.Context, logger logr.Logger, policy *deployv1alpha1.AutomatedRollbackPolicy, release *deployv1alpha1.Release) error {
	// Create rollback
	rollback := createRollback(ctx, release, policy, r.ServiceAccountName)
	if err := r.createOrUpdate(ctx, logger, rollback, "rollback"); err != nil {
		logger.Error(err, "failed to create rollback")
		return err
	}

	updatePolicyStatus(policy)
	return nil
}

func updatePolicyStatus(policy *deployv1alpha1.AutomatedRollbackPolicy) {
	now := metav1.NewTime(time.Now())
	policy.Status.LastAutomatedRollbackTime = &now

	windowExpired := policy.Status.WindowStartTime == nil ||
		(policy.Spec.ResetPeriod != nil &&
			policy.Status.WindowStartTime.Add(policy.Spec.ResetPeriod.Duration).Before(time.Now()))

	if windowExpired {
		policy.Status.WindowStartTime = &now
		policy.Status.ConsecutiveCount = 0
	}
	policy.Status.ConsecutiveCount++
}

func createRollback(ctx context.Context, release *deployv1alpha1.Release, policy *deployv1alpha1.AutomatedRollbackPolicy, principal string) *deployv1alpha1.Rollback {
	reason := fmt.Sprintf("Release %s status condition %s is %s", release.Name, policy.Spec.Trigger.ConditionType, policy.Spec.Trigger.ConditionStatus)

	rb := &deployv1alpha1.Rollback{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", release.TargetName),
			Namespace:    release.Namespace,
			Labels:       release.Labels,
		},
		Spec: deployv1alpha1.RollbackSpec{
			Reason: reason,
			InitiatedBy: deployv1alpha1.RollbackInitiator{
				Principal: principal,
				Type:      "system",
			},
			ToReleaseRef: deployv1alpha1.ReleaseReference{
				Target: policy.Spec.TargetName,
			},
			DeploymentOptions: policy.Spec.DeploymentOptions,
		},
	}

	return rb
}

func (r *AutomatedRollbackReconciler) hasRollback(ctx context.Context, release *deployv1alpha1.Release) (bool, error) {
	rollbackList := &deployv1alpha1.RollbackList{}
	if err := r.List(ctx, rollbackList, client.InNamespace(release.Namespace), client.MatchingFields(map[string]string{IndexFieldOwner: release.Name})); err != nil {
		return false, fmt.Errorf("failed to list rollbacks: %w", err)
	}

	return len(rollbackList.Items) > 0, nil
}
