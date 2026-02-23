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

	// TODO: Feed in the resetOnRecovery logic here

	allowed, reason, message := canPerformRollback(policy)
	condition := metav1.ConditionFalse
	if allowed {
		condition = metav1.ConditionTrue
	}

	meta.SetStatusCondition(&policy.Status.Conditions, metav1.Condition{
		Type:    deployv1alpha1.AutomatedRollbackPolicyConditionActive,
		Status:  condition,
		Reason:  reason,
		Message: message,
	})

	if !allowed {
		logger.Info("rollback is not allowed, nothing to do", "reason", reason)
		// nothing to do, update policy and exit early
		if err := r.createOrUpdate(ctx, logger, policy, "policy"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	release, err := r.getActiveReleaseForTarget(ctx, policy)
	if err != nil {
		if errors.Is(err, ErrNoActiveReleaseFound) {
			logger.Info("no active release found, exiting...")
			// if there is not active release, we can't perform a rollback, exiting early
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if hasRollback, err := r.hasRollback(ctx, release); err != nil {
		return ctrl.Result{}, err
	} else if hasRollback {
		logger.Info("release already has a rollback, nothing to do")
		// release already has a rollback, exit early
		return ctrl.Result{}, nil
	}

	triggerConditionType := policy.Spec.Trigger.ConditionType
	triggerConditionStatus := policy.Spec.Trigger.ConditionStatus
	if !meta.IsStatusConditionPresentAndEqual(release.Status.Conditions, triggerConditionType, triggerConditionStatus) {
		logger.Info("trigger condition is not met, nothing to do")
		// nothing to do, exit early
		return ctrl.Result{}, nil
	}

	rollback := createRollback(ctx, release, policy, r.ServiceAccountName)
	if err := r.createOrUpdate(ctx, logger, rollback, "rollback"); err != nil {
		return ctrl.Result{}, err
	}

	// Update policy
	rollbackTime := metav1.NewTime(rollback.CreationTimestamp.Time)
	policy.Status.LastAutomatedRollbackTime = &rollbackTime
	policy.Status.ConsecutiveCount++

	if err := r.createOrUpdate(ctx, logger, policy, "policy"); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
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

func (r *AutomatedRollbackReconciler) createOrUpdate(ctx context.Context, logger logr.Logger, obj recutil.ObjWithMeta, objectType string) error {
	outcome, err := recutil.CreateOrUpdate(ctx, r.Client, obj, recutil.StatusDiff)
	if err != nil {
		logger.Error(err, fmt.Sprintf("failed to update %s status", objectType))
		return err
	}

	switch outcome {
	case recutil.Create:
		logger.Info("created", objectType, obj.GetName(), "event", EventCreated)
	case recutil.StatusUpdate:
		logger.Info("status updated", objectType, obj.GetName(), "event", EventSuccessfulStatusUpdate)
	case recutil.Update:
		logger.Info("updated", objectType, obj.GetName(), "event", EventSuccessfulUpdate)
	case recutil.None:
		logging.WithNoRecord(logger).Info("No status update needed", "event", EventNoStatusUpdate)
	default:
		logger.Info("Unexpected outcome from CreateOrUpdate", "outcome", outcome)
	}

	return nil
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

// TODO: add logic to requeue policy for reconciliation
func canPerformRollback(policy *deployv1alpha1.AutomatedRollbackPolicy) (allowed bool, reason string, message string) {
	if !policy.Spec.Enabled {
		return false, deployv1alpha1.AutomatedRollbackPolicySetByUser, "Automated rollback policy is disabled"
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
			return false, deployv1alpha1.AutomatedRollbackPolicyDisabledByController, fmt.Sprintf("Max consecutive rollbacks (%d) reached within reset period", *policy.Spec.MaxConsecutiveRollbacks)
		}
	} else {
		policy.Status.ConsecutiveCount = 0
		policy.Status.WindowStartTime = nil
	}

	// Check minimum interval between rollbacks
	if policy.Spec.MinInterval != nil &&
		policy.Status.LastAutomatedRollbackTime != nil &&
		policy.Status.LastAutomatedRollbackTime.Add(policy.Spec.MinInterval.Duration).After(time.Now()) {
		return false, deployv1alpha1.AutomatedRollbackPolicyDisabledByController, fmt.Sprintf("Min interval (%s) between rollbacks not met", policy.Spec.MinInterval.Duration)
	}

	return true, deployv1alpha1.AutomatedRollbackPolicySetByUser, "Automated rollback is enabled"
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
