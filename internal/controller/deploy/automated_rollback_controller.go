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

type AutomatedRollbackReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func (r *AutomatedRollbackReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := r.Log.WithValues("component", "AutomatedRollback")

	ctrlBuilder := ctrl.NewControllerManagedBy(mgr).
		For(&deployv1alpha1.AutomatedRollbackPolicy{}).
		Watches(&deployv1alpha1.Release{},
			handler.EnqueueRequestsFromMapFunc(r.mapReleaseToPolicy(mgr))).
		WithEventFilter(r.onReleaseConditionsChangedPredicate(ctx, logger))

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
	evaluation := evaluateAndUpdatePolicyStatus(policy)
	if !evaluation.Allowed {
		logger.Info("rollback is not allowed, nothing to do", "reason", evaluation.Reason)
		return r.updatePolicyAndReturn(ctx, logger, policy, evaluation.RequeueAfter)
	}

	// Check if rollback should be triggered
	shouldTrigger, release, err := r.shouldTriggerRollback(ctx, logger, policy)
	if err != nil {
		return ctrl.Result{}, err
	}

	if shouldTrigger {
		// Create rollback and update policy
		if err := r.performRollback(ctx, logger, policy, release); err != nil {
			return ctrl.Result{}, err
		}
	}

	return r.updatePolicyAndReturn(ctx, logger, policy, nil)
}

// TODO: make this generic function that can be reused when culling, rollbacks and here
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

func (r *AutomatedRollbackReconciler) onReleaseConditionsChangedPredicate(ctx context.Context, logger logr.Logger) predicate.Predicate {
	return predicate.Funcs{
		// All logs are at V(1) level, so we need to enable debug logging to see them
		UpdateFunc: func(e event.UpdateEvent) bool {
			if _, isPolicy := e.ObjectNew.(*deployv1alpha1.AutomatedRollbackPolicy); isPolicy {
				return true
			}

			if release, isRelease := e.ObjectNew.(*deployv1alpha1.Release); isRelease {
				if release.TargetName == "" {
					logger.V(1).Info("Release has no target name, skipping", "release", release.Name)
					return false
				}

				policyList := &deployv1alpha1.AutomatedRollbackPolicyList{}
				if err := r.List(ctx, policyList, client.InNamespace(release.Namespace), client.MatchingFields(map[string]string{IndexFieldPolicyTargetName: release.TargetName})); err != nil {
					logger.V(1).Info("Failed to list policies for target, skipping", "target", release.TargetName, "error", err)
					return false
				}

				if len(policyList.Items) != 1 {
					logger.V(1).Info("Expected exactly one policy for target, skipping", "target", release.TargetName)
					return false
				}

				policy := policyList.Items[0]

				oldRelease := e.ObjectOld.(*deployv1alpha1.Release)
				oldHealthCond := meta.FindStatusCondition(oldRelease.Status.Conditions, policy.Spec.Trigger.ConditionType)
				newHealthCond := meta.FindStatusCondition(release.Status.Conditions, policy.Spec.Trigger.ConditionType)
				healthTransitioned := oldHealthCond == nil && newHealthCond != nil ||
					(oldHealthCond != nil && newHealthCond != nil && oldHealthCond.Status != newHealthCond.Status)

				oldTriggerCond := meta.FindStatusCondition(oldRelease.Status.Conditions, policy.Spec.Trigger.ConditionType)
				newTriggerCond := meta.FindStatusCondition(release.Status.Conditions, policy.Spec.Trigger.ConditionType)
				triggerTransitioned := oldTriggerCond == nil && newTriggerCond != nil ||
					(oldTriggerCond != nil && newTriggerCond != nil && oldTriggerCond.Status != newTriggerCond.Status)

				logger.V(1).Info("Release conditions changed, checking if rollback should be triggered",
					"release", release.Name,
					"healthTransitioned", healthTransitioned,
					"triggerTransitioned", triggerTransitioned,
					"isActive", release.IsConditionActiveTrue())

				return release.IsConditionActiveTrue() && (healthTransitioned || triggerTransitioned)
			}

			return false
		},
		CreateFunc: func(e event.CreateEvent) bool {
			if _, isPolicy := e.Object.(*deployv1alpha1.AutomatedRollbackPolicy); isPolicy {
				return true
			}

			if release, isRelease := e.Object.(*deployv1alpha1.Release); isRelease {
				return release.IsConditionActiveTrue()
			}

			return false
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, isPolicy := e.Object.(*deployv1alpha1.AutomatedRollbackPolicy)
			return isPolicy
		},
	}
}

func (r *AutomatedRollbackReconciler) mapReleaseToPolicy(mgr ctrl.Manager) handler.MapFunc {
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
	rollback := createRollback(release, policy)
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

	// TODO: Move to a helper
	windowExpired := policy.Status.WindowStartTime == nil ||
		(policy.Spec.ResetPeriod != nil &&
			policy.Status.WindowStartTime.Add(policy.Spec.ResetPeriod.Duration).Before(time.Now()))

	if windowExpired {
		policy.Status.WindowStartTime = &now
		policy.Status.ConsecutiveCount = 0
	}
	policy.Status.ConsecutiveCount++
}

func (r *AutomatedRollbackReconciler) hasRollback(ctx context.Context, release *deployv1alpha1.Release) (bool, error) {
	rollbackList := &deployv1alpha1.RollbackList{}
	if err := r.List(ctx, rollbackList, client.InNamespace(release.Namespace), client.MatchingFields(map[string]string{IndexFieldOwner: release.Name})); err != nil {
		return false, fmt.Errorf("failed to list rollbacks: %w", err)
	}

	return len(rollbackList.Items) > 0, nil
}

// evaluateAndUpdatePolicyStatus evaluates the status of the automated rollback policy
// and updates the policy status conditions accordingly
func evaluateAndUpdatePolicyStatus(policy *deployv1alpha1.AutomatedRollbackPolicy) deployv1alpha1.PolicyEvaluation {
	result := policy.EvaluatePolicyConstraints()

	if result.WindowExpired {
		policy.Status.WindowStartTime = nil
		policy.Status.ConsecutiveCount = 0
	}

	status := metav1.ConditionFalse
	if result.Allowed {
		status = metav1.ConditionTrue
	}
	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:    deployv1alpha1.AutomatedRollbackPolicyConditionActive,
		Status:  status,
		Reason:  result.Reason,
		Message: result.Message,
	})

	return result
}

func createRollback(release *deployv1alpha1.Release, policy *deployv1alpha1.AutomatedRollbackPolicy) *deployv1alpha1.Rollback {
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
				Principal: "automated-rollback-controller",
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
