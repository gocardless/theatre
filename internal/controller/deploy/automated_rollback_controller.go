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
			rollback := rawObj.(*deployv1alpha1.Rollback)
			owner := metav1.GetControllerOf(rollback)
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

func (r *AutomatedRollbackReconciler) onReleaseConditionsChangedPredicate(ctx context.Context, logger logr.Logger) predicate.Predicate {
	return predicate.Funcs{
		// All logs are at V(1) level, so we need to enable debug logging to see them
		UpdateFunc: func(e event.UpdateEvent) bool {
			if _, isPolicy := e.ObjectNew.(*deployv1alpha1.AutomatedRollbackPolicy); isPolicy {
				return true
			}

			if release, isRelease := e.ObjectNew.(*deployv1alpha1.Release); isRelease {
				policyList := &deployv1alpha1.AutomatedRollbackPolicyList{}
				if err := r.List(ctx, policyList,
					client.InNamespace(release.Namespace),
					client.MatchingFields(map[string]string{IndexFieldPolicyTargetName: release.TargetName}),
				); err != nil {
					logger.V(1).Info("Failed to list policies for target, skipping", "target", release.TargetName, "error", err)
					return false
				}

				if len(policyList.Items) != 1 {
					logger.V(1).Info("Expected exactly one policy for target, skipping", "target", release.TargetName)
					return false
				}
				policy := policyList.Items[0]

				oldRelease := e.ObjectOld.(*deployv1alpha1.Release)
				triggerTransitioned := recutil.HasConditionTransitioned(oldRelease.Status.Conditions, release.Status.Conditions, policy.Spec.Trigger.ConditionType)

				logger.V(1).Info("Release conditions changed, checking if reconciliation should be triggered",
					"release", release.Name,
					"triggerTransitioned", triggerTransitioned,
					"isActive", release.IsConditionActiveTrue())

				return release.IsConditionActiveTrue() && triggerTransitioned
			}

			return false
		},
		CreateFunc: func(e event.CreateEvent) bool {
			_, isPolicy := e.Object.(*deployv1alpha1.AutomatedRollbackPolicy)
			return isPolicy
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			_, isPolicy := e.Object.(*deployv1alpha1.AutomatedRollbackPolicy)
			return isPolicy
		},
	}
}

func (r *AutomatedRollbackReconciler) mapReleaseToPolicy(mgr ctrl.Manager) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		release, isRelease := obj.(*deployv1alpha1.Release)
		if !isRelease {
			return nil
		}

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

func (r *AutomatedRollbackReconciler) Reconcile(ctx context.Context, logger logr.Logger, request ctrl.Request, policy *deployv1alpha1.AutomatedRollbackPolicy) (ctrl.Result, error) {
	logger = logger.WithValues("namespace", request.Namespace, "target", policy.Spec.TargetName, "policy", policy.Name)
	logger.Info("Reconcile")

	// Find active release
	release, err := r.getActiveReleaseForPolicy(ctx, policy)
	// If no active release is found, we want to continue and
	// evaluate the policy constraints and update its status
	if err != nil && !errors.Is(err, ErrNoActiveReleaseFound) {
		return ctrl.Result{}, err
	}

	evaluation := evaluateAndUpdatePolicyStatus(policy, release)
	if !evaluation.Allowed {
		logger.Info("rollback is not allowed, nothing to do", "reason", evaluation.Reason)
		err := r.createOrUpdate(ctx, logger, policy, "policy")
		return ctrl.Result{}, err
	}

	// Check if rollback should be triggered
	shouldTrigger, err := r.shouldTriggerRollback(ctx, logger, policy, release)
	if err != nil {
		return ctrl.Result{}, err
	}

	if shouldTrigger {
		// Create rollback and update policy
		if err := r.createRollback(ctx, logger, policy, release); err != nil {
			return ctrl.Result{}, err
		}

		disableAutomationAfterRollback(policy)
	}

	err = r.createOrUpdate(ctx, logger, policy, "policy")
	return ctrl.Result{}, err
}

// createOrUpdate creates or updates (spec or status) the given object, logging the outcome.
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

// shouldTriggerRollback checks if a rollback should be triggered for the given policy.
// Rollback is triggered when the release is active, there isn't a prior rollback and
// the trigger condition is met.
func (r *AutomatedRollbackReconciler) shouldTriggerRollback(ctx context.Context, logger logr.Logger, policy *deployv1alpha1.AutomatedRollbackPolicy, release *deployv1alpha1.Release) (bool, error) {
	if release == nil {
		logger.Info("no active release found, nothing to do")
		return false, nil
	}

	// Check if release already has a rollback
	hasRollback, err := r.hasRollback(ctx, release)
	if err != nil {
		return false, err
	}

	if hasRollback {
		logger.Info("release already has a rollback, nothing to do", "release", release.Name)
		return false, nil
	}

	// Check trigger condition
	triggerConditionType := policy.Spec.Trigger.ConditionType
	triggerConditionStatus := policy.Spec.Trigger.ConditionStatus
	if !meta.IsStatusConditionPresentAndEqual(release.Status.Conditions, triggerConditionType, triggerConditionStatus) {
		logger.Info("trigger condition not met, nothing to do", "release", release.Name)
		return false, nil
	}

	return true, nil
}

func (r *AutomatedRollbackReconciler) createRollback(ctx context.Context, logger logr.Logger, policy *deployv1alpha1.AutomatedRollbackPolicy, release *deployv1alpha1.Release) error {
	reason := fmt.Sprintf("Release %s status condition %s is %s", release.Name, policy.Spec.Trigger.ConditionType, policy.Spec.Trigger.ConditionStatus)
	rollback := &deployv1alpha1.Rollback{
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

	if err := r.createOrUpdate(ctx, logger, rollback, "rollback"); err != nil {
		logger.Error(err, "failed to create rollback")
		return err
	}

	return nil
}

// disableAutomationAfterRollback disables the automated rollback policy after a rollback has been performed
// by setting the LastAutomatedRollbackTime and updating the condition
func disableAutomationAfterRollback(policy *deployv1alpha1.AutomatedRollbackPolicy) {
	now := metav1.NewTime(time.Now())
	policy.Status.LastAutomatedRollbackTime = &now

	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:    deployv1alpha1.AutomatedRollbackPolicyConditionActive,
		Status:  metav1.ConditionFalse,
		Reason:  deployv1alpha1.AutomatedRollbackPolicyReasonDisabledByController,
		Message: "Automation disabled after performing a rollback. Will be enabled after the next healthy release.",
	})
}

func (r *AutomatedRollbackReconciler) getActiveReleaseForPolicy(ctx context.Context, policy *deployv1alpha1.AutomatedRollbackPolicy) (*deployv1alpha1.Release, error) {
	releaseList, err := GetReleasesForTarget(ctx, r.Client, policy.Namespace, policy.Spec.TargetName)
	if err != nil {
		return nil, err
	}

	activeRelease := deployv1alpha1.FindActiveRelease(releaseList)
	if activeRelease == nil {
		return nil, ErrNoActiveReleaseFound
	}

	return activeRelease, nil
}

func (r *AutomatedRollbackReconciler) hasRollback(ctx context.Context, release *deployv1alpha1.Release) (bool, error) {
	rollbackList := &deployv1alpha1.RollbackList{}
	if err := r.List(ctx, rollbackList,
		client.InNamespace(release.Namespace),
		client.MatchingFields(map[string]string{IndexFieldOwner: release.Name})); err != nil {
		return false, fmt.Errorf("failed to list rollbacks: %w", err)
	}

	return len(rollbackList.Items) > 0, nil
}

// evaluateAndUpdatePolicyStatus evaluates the policy constraints
// and updates the policy status conditions accordingly
func evaluateAndUpdatePolicyStatus(policy *deployv1alpha1.AutomatedRollbackPolicy, release *deployv1alpha1.Release) deployv1alpha1.PolicyEvaluation {
	result := policy.EvaluatePolicyConstraints(release)

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
