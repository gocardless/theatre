package deploy

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
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
		// Named("automated_rollback").
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

	return ctrlBuilder.Complete(
		recutil.ResolveAndReconcile(
			ctx, logger, mgr, &deployv1alpha1.AutomatedRollbackPolicy{},
			func(logger logr.Logger, request ctrl.Request, obj runtime.Object) (ctrl.Result, error) {
				return r.Reconcile(ctx, logger, request, obj.(*deployv1alpha1.AutomatedRollbackPolicy))
			},
		),
	)
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

func (r *AutomatedRollbackReconciler) Reconcile(ctx context.Context, logger logr.Logger, request ctrl.Request, policy *deployv1alpha1.AutomatedRollbackPolicy) (ctrl.Result, error) {
	logger.Info("Reconciling automated rollback policy", "policy", policy.Name)

	release, err := r.getActiveReleaseForTarget(ctx, policy)
	if err != nil {
		logger.Info("Failed to get active release", "error", err)
		// If we didn't find an active release, we will only reconcile the policy, without rollbacks
		if !errors.Is(err, ErrNoActiveReleaseFound) {
			return ctrl.Result{}, err
		}
	}

	if hasRollback, err := r.hasRollback(ctx, release); err != nil {
		return ctrl.Result{}, err
	} else if hasRollback {
		logger.Info("release already has a rollback, nothing to do")
		// release already has a rollback, exit early
		return ctrl.Result{}, nil
	}

	if !policy.IsStatusInitialised() {
		logger.Info("initialising rollback policy status")
		policy.InitialiseStatus()
		if err := r.Status().Update(ctx, policy); err != nil {
			return ctrl.Result{}, err
		}
	}

	allowed, reason := rollbackAllowed(policy)
	if !allowed {
		logger.Info("rollback is not allowed, nothing to do", "reason", reason)

		changed := meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
			Type:    deployv1alpha1.AutomatedRollbackPolicyConditionActive,
			Status:  metav1.ConditionFalse,
			Reason:  deployv1alpha1.AutomatedRollbackPolicyDisabledByController,
			Message: reason,
		})

		if changed {
			if err := r.Status().Update(ctx, policy); err != nil {
				return ctrl.Result{}, err
			}
		}
		// nothing to do, exit early
		return ctrl.Result{}, nil
	}

	triggerConditionType := policy.Spec.Trigger.ConditionType
	triggerConditionStatus := policy.Spec.Trigger.ConditionStatus
	if !meta.IsStatusConditionPresentAndEqual(release.Status.Conditions, triggerConditionType, triggerConditionStatus) {
		logger.Info("trigger condition is not met, nothing to do")
		// nothing to do, exit early
		return ctrl.Result{}, nil
	}

	var rollback *deployv1alpha1.Rollback
	if rollback, err = r.createRollback(ctx, release, policy); err != nil {
		return ctrl.Result{}, err
	}

	logger.Info("trigger condition is met, automated rollback created", "rollback", rollback.Name, "event", EventAutomatedRollbackTriggered)

	if err := r.Update(ctx, release); err != nil {
		return ctrl.Result{}, err
	}

	// Update policy
	rollbackTime := metav1.NewTime(rollback.CreationTimestamp.Time)
	policy.Status.LastAutomatedRollbackTime = &rollbackTime
	if policy.Status.WindowStartTime == nil || policy.Spec.ResetPeriod != nil && policy.Status.WindowStartTime.Time.Add(policy.Spec.ResetPeriod.Duration).Before(time.Now()) {
		policy.Status.WindowStartTime = &metav1.Time{Time: time.Now()}
		policy.Status.ConsecutiveCount = 0
	}
	policy.Status.ConsecutiveCount++
	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:    deployv1alpha1.AutomatedRollbackPolicyConditionActive,
		Status:  metav1.ConditionTrue,
		Reason:  deployv1alpha1.AutomatedRollbackPolicySetByUser,
		Message: "Enabled by user and within reset period",
	})

	if err := r.Status().Update(ctx, policy); err != nil {
		return ctrl.Result{}, err
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

func rollbackAllowed(policy *deployv1alpha1.AutomatedRollbackPolicy) (allowed bool, reason string) {
	if !policy.Spec.Enabled {
		return false, "Automated rollback policy is disabled"
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
			return false, fmt.Sprintf("Max consecutive rollbacks (%d) reached within reset period", *policy.Spec.MaxConsecutiveRollbacks)
		}
	}

	// Check minimum interval between rollbacks
	if policy.Spec.MinInterval != nil &&
		policy.Status.LastAutomatedRollbackTime != nil &&
		policy.Status.LastAutomatedRollbackTime.Add(policy.Spec.MinInterval.Duration).After(time.Now()) {
		return false, fmt.Sprintf("Min interval (%s) between rollbacks not met", policy.Spec.MinInterval.Duration)
	}

	return true, ""
}

func (r *AutomatedRollbackReconciler) createRollback(ctx context.Context, release *deployv1alpha1.Release, policy *deployv1alpha1.AutomatedRollbackPolicy) (*deployv1alpha1.Rollback, error) {
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
				Principal: r.ServiceAccountName,
				Type:      "system",
			},
			DeploymentOptions: policy.Spec.DeploymentOptions,
		},
	}

	if err := r.Create(ctx, rb); err != nil {
		return nil, err
	}

	return rb, nil
}

func (r *AutomatedRollbackReconciler) hasRollback(ctx context.Context, release *deployv1alpha1.Release) (bool, error) {
	// rollbackList := &deployv1alpha1.RollbackList{}
	// if err := r.List(ctx, rollbackList, client.InNamespace(release.Namespace), client.MatchingFields(map[string]string{IndexFieldOwner: release.Name})); err != nil {
	// 	return false, fmt.Errorf("failed to list rollbacks: %w", err)
	// }

	// return len(rollbackList.Items) > 0, nil
	// TODO: temporary check for label instead
	_, ok := release.Labels["rollback"]
	return ok, nil
}

func (r *AutomatedRollbackReconciler) getRollbackPolicy(ctx context.Context, release *deployv1alpha1.Release) (*deployv1alpha1.AutomatedRollbackPolicy, error) {
	target := release.ReleaseConfig.TargetName

	policyList := &deployv1alpha1.AutomatedRollbackPolicyList{}
	matchFields := client.MatchingFields(map[string]string{IndexFieldPolicyTargetName: target})
	if err := r.List(ctx, policyList, client.InNamespace(release.Namespace), matchFields); err != nil {
		return nil, fmt.Errorf("failed to list rollback policies: %w", err)
	}

	if len(policyList.Items) == 0 {
		return nil, ErrNoRollbackPolicyFound
	}

	if len(policyList.Items) > 1 {
		return nil, fmt.Errorf("multiple rollback policies found for target: %s", target)
	}

	return &policyList.Items[0], nil
}
