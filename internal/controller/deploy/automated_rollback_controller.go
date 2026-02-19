package deploy

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-logr/logr"
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/gocardless/theatre/v5/pkg/recutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

const (
	ErrNoRollbackPolicyFound = errors.New("no rollback policies found for target")

	IndexFieldSpecTargetName = ".spec.targetName"
)

type AutomatedRollbackReconciler struct {
	client.Client
	Log    logr.Logger
	Scheme *runtime.Scheme
}

func (r *AutomatedRollbackReconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logger := r.Log.WithValues("component", "AutomatedRollback")

	ctrlBuilder := ctrl.NewControllerManagedBy(mgr).
		Named("automated_rollback").
		For(&deployv1alpha1.Release{}).
		// Owns(&deployv1alpha1.Rollback{}).
		WithEventFilter(r.onReleaseConditionsChangedPredicate())

	err := mgr.GetFieldIndexer().IndexField(
		ctx,
		&deployv1alpha1.AutomatedRollbackPolicy{},
		IndexFieldSpecTargetName,
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
			ctx, logger, mgr, &deployv1alpha1.Release{},
			func(logger logr.Logger, request ctrl.Request, obj runtime.Object) (ctrl.Result, error) {
				return r.Reconcile(ctx, logger, request, obj.(*deployv1alpha1.Release))
			},
		),
	)
}

func (r *AutomatedRollbackReconciler) Reconcile(ctx context.Context, logger logr.Logger, request ctrl.Request, release *deployv1alpha1.Release) (ctrl.Result, error) {
	logger.Info("Automated rollback reconciler for release ", "release", release.Name)
	if !release.IsConditionActiveTrue() {
		// nothing to do, exit early
		return ctrl.Result{}, nil
	}

	if hasRollback, err := r.hasRollback(ctx, release); err != nil {
		return ctrl.Result{}, err
	} else if hasRollback {
		// nothing to do, exit early
		return ctrl.Result{}, nil
	}

	// fetch the rollback policy for that target name
	var err error
	var policy *deployv1alpha1.AutomatedRollbackPolicy
	if policy, err = r.getRollbackPolicy(ctx, release); err != nil {
		if err == ErrNoRollbackPolicyFound {
			logger.Info("no rollback policy found for target", "target", release.TargetName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if !rollbackAllowed(policy) {
		// nothing to do, exit early
		return ctrl.Result{}, nil
	}

	var rollback *deployv1alpha1.Rollback
	if rollback, err = r.createRollback(ctx, release, policy); err != nil {
		return ctrl.Result{}, err
	}

	// Update policy
	rollbackTime := metav1.NewTime(rollback.CreationTimestamp.Time)
	policy.Status.LastAutomatedRollbackTime = &rollbackTime
	if err := r.Status().Update(ctx, policy); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *AutomatedRollbackReconciler) onReleaseConditionsChangedPredicate() predicate.Predicate {
	return predicate.Funcs{
		// TODO: filter out only releases that are active and require rollback
		UpdateFunc: func(e event.UpdateEvent) bool {
			return e.ObjectNew.(*deployv1alpha1.Release).IsConditionActiveTrue()
		},
		CreateFunc: func(e event.CreateEvent) bool {
			return e.Object.(*deployv1alpha1.Release).IsConditionActiveTrue()
		},
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}
}

func rollbackAllowed(rollbackPolicy *deployv1alpha1.AutomatedRollbackPolicy) bool {
	// TODO: expand this to handle the rest of the fields in the spec
	return rollbackPolicy.Spec.Enabled
}

func (r *AutomatedRollbackReconciler) createRollback(ctx context.Context, release *deployv1alpha1.Release, policy *deployv1alpha1.AutomatedRollbackPolicy) (*deployv1alpha1.Rollback, error) {
	spec := deployv1alpha1.RollbackSpec{
		Reason: "automated rollback",
		InitiatedBy: deployv1alpha1.RollbackInitiator{
			Principal: "system",
			Type:      "system",
		},
		DeploymentOptions: policy.Spec.DeploymentOptions,
	}

	rb := &deployv1alpha1.Rollback{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: fmt.Sprintf("%s-", release.TargetName),
			Namespace:    release.Namespace,
			Labels:       release.Labels,
		},
		Spec: spec,
	}

	if err := r.Create(ctx, rb); err != nil {
		return nil, err
	}

	return rb, nil
}

func (r *AutomatedRollbackReconciler) hasRollback(ctx context.Context, release *deployv1alpha1.Release) (bool, error) {
	rollbackList := &deployv1alpha1.RollbackList{}
	if err := r.List(ctx, rollbackList, client.InNamespace(release.Namespace), client.MatchingFields(map[string]string{IndexFieldOwner: release.Name})); err != nil {
		return false, fmt.Errorf("failed to list rollbacks: %w", err)
	}

	return len(rollbackList.Items) > 0, nil
}

func (r *AutomatedRollbackReconciler) getRollbackPolicy(ctx context.Context, release *deployv1alpha1.Release) (*deployv1alpha1.AutomatedRollbackPolicy, error) {
	target := release.ReleaseConfig.TargetName

	policyList := &deployv1alpha1.AutomatedRollbackPolicyList{}
	matchFields := client.MatchingFields(map[string]string{IndexFieldSpecTargetName: target})
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
