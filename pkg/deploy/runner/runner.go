package runner

import (
	"context"
	"errors"
	"fmt"
	"sort"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Runner provides operations for managing deploy resources
type Runner struct {
	client client.Client
}

// New builds a runner from a Kubernetes rest config
func New(cfg *rest.Config) (*Runner, error) {
	scheme := runtime.NewScheme()
	if err := deployv1alpha1.AddToScheme(scheme); err != nil {
		return nil, err
	}

	cl, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return nil, err
	}

	return &Runner{client: cl}, nil
}

type CreateRollbackOptions struct {
	Name               string
	GenerateNamePrefix string
	Namespace          string
	Labels             map[string]string

	Reason          string
	ToReleaseTarget string
	ToReleaseName   string

	InitiatedByPrincipal string
	InitiatedByType      string

	DeploymentOptions map[string]apiextv1.JSON
}

func (r *Runner) CreateRollback(ctx context.Context, opts CreateRollbackOptions) (*deployv1alpha1.Rollback, error) {
	if opts.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}
	if opts.Reason == "" {
		return nil, fmt.Errorf("reason is required")
	}
	if opts.ToReleaseTarget == "" {
		return nil, fmt.Errorf("toReleaseTarget is required")
	}
	if opts.Name != "" && opts.GenerateNamePrefix != "" {
		return nil, fmt.Errorf("only one of name or generateNamePrefix may be set")
	}

	spec := deployv1alpha1.RollbackSpec{
		ToReleaseRef: deployv1alpha1.ReleaseReference{
			Target: opts.ToReleaseTarget,
			Name:   opts.ToReleaseName,
		},
		Reason: opts.Reason,
		InitiatedBy: deployv1alpha1.RollbackInitiator{
			Principal: opts.InitiatedByPrincipal,
			Type:      opts.InitiatedByType,
		},
		DeploymentOptions: opts.DeploymentOptions,
	}

	rb := &deployv1alpha1.Rollback{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: opts.Namespace,
			Labels:    opts.Labels,
		},
		Spec: spec,
	}

	if opts.Name != "" {
		rb.Name = opts.Name
	} else {
		rb.GenerateName = opts.GenerateNamePrefix
	}

	if err := r.client.Create(ctx, rb); err != nil {
		return nil, err
	}

	return rb, nil
}

// ListRollbacksOptions defines parameters for listing rollbacks
type ListRollbacksOptions struct {
	Namespace string
	Target    string
	Limit     int
}

// ListRollbacks fetches rollbacks for a specific target in a namespace, sorted by completion time (most recent first)
func (r *Runner) ListRollbacks(ctx context.Context, opts ListRollbacksOptions) ([]deployv1alpha1.Rollback, error) {
	var rollbackList deployv1alpha1.RollbackList
	if err := r.client.List(ctx, &rollbackList, client.InNamespace(opts.Namespace)); err != nil {
		return nil, err
	}

	rollbacks := rollbackList.Items

	if opts.Target != "" {
		var filtered []deployv1alpha1.Rollback
		for _, rollback := range rollbacks {
			if rollback.Spec.ToReleaseRef.Target == opts.Target {
				filtered = append(filtered, rollback)
			}
		}
		rollbacks = filtered
	}

	sortRollbacksByEffectiveTime(rollbacks)

	if opts.Limit > 0 && len(rollbacks) > opts.Limit {
		rollbacks = rollbacks[:opts.Limit]
	}

	return rollbacks, nil
}

// Sorts rollbacks by effective time (most recent first)
func sortRollbacksByEffectiveTime(rollbacks []deployv1alpha1.Rollback) {
	sort.Slice(rollbacks, func(i, j int) bool {
		return rollbacks[i].GetEffectiveTime().After(rollbacks[j].GetEffectiveTime())
	})
}

// ListReleasesOptions defines parameters for listing releases
type ListReleasesOptions struct {
	Namespace string
	Target    string
	Limit     int
}

// ListReleases fetches releases for a specific target in a namespace, sorted by deployment end time (most recent first)
func (r *Runner) ListReleases(ctx context.Context, opts ListReleasesOptions) ([]deployv1alpha1.Release, error) {
	var releaseList deployv1alpha1.ReleaseList
	if err := r.client.List(ctx, &releaseList, client.InNamespace(opts.Namespace)); err != nil {
		return nil, err
	}

	releases := releaseList.Items

	if opts.Target != "" {
		var filtered []deployv1alpha1.Release
		for _, release := range releases {
			if release.ReleaseConfig.TargetName == opts.Target {
				filtered = append(filtered, release)
			}
		}
		releases = filtered
	}

	sortReleasesByEndTime(releases)

	if opts.Limit > 0 && len(releases) > opts.Limit {
		releases = releases[:opts.Limit]
	}

	return releases, nil
}

// Sorts releases by DeploymentEndTime in descending order (most recent first)
func sortReleasesByEndTime(releases []deployv1alpha1.Release) {
	sort.Slice(releases, func(i, j int) bool {
		return releases[i].Status.DeploymentEndTime.After(releases[j].Status.DeploymentEndTime.Time)
	})
}

type GetReleaseOptions struct {
	Namespace string
	Name      string
}

func (r *Runner) GetRelease(ctx context.Context, opts GetReleaseOptions) (*deployv1alpha1.Release, error) {
	var release deployv1alpha1.Release
	if err := r.client.Get(ctx, client.ObjectKey{Namespace: opts.Namespace, Name: opts.Name}, &release); err != nil {
		return nil, err
	}
	return &release, nil
}

func HasRevision(release deployv1alpha1.Release, revisionName string) bool {
	for _, revision := range release.ReleaseConfig.Revisions {
		if revision.Name == revisionName {
			return true
		}
	}
	return false
}

var ErrAutomatedRollbackPolicyNotFound = errors.New("automated rollback policy not found")

type GetAutomatedRollbackPolicyOptions struct {
	Namespace  string
	TargetName string
}

// GetAutomatedRollbackPolicyByTarget retrieves an automated rollback policy by target name
func (r *Runner) GetAutomatedRollbackPolicyByTarget(ctx context.Context, opts GetAutomatedRollbackPolicyOptions) (*deployv1alpha1.AutomatedRollbackPolicy, error) {
	if opts.Namespace == "" {
		return nil, fmt.Errorf("namespace is required")
	}

	if opts.TargetName == "" {
		return nil, fmt.Errorf("targetName is required")
	}

	var policyList deployv1alpha1.AutomatedRollbackPolicyList

	if err := r.client.List(ctx, &policyList,
		client.InNamespace(opts.Namespace),
	); err != nil {
		return nil, err
	}
	for _, policy := range policyList.Items {
		if policy.Spec.TargetName == opts.TargetName {
			return &policy, nil
		}
	}
	return nil, ErrAutomatedRollbackPolicyNotFound
}

type UpdateAutomatedRollbackPolicyOptions struct {
	Namespace  string
	TargetName string
	Enabled    bool
}

// UpdateAutomatedRollbackPolicy updates the enabled status of an automated rollback policy
func (r *Runner) UpdateAutomatedRollbackPolicy(ctx context.Context, opts UpdateAutomatedRollbackPolicyOptions) error {
	policy, err := r.GetAutomatedRollbackPolicyByTarget(ctx, GetAutomatedRollbackPolicyOptions{
		Namespace:  opts.Namespace,
		TargetName: opts.TargetName,
	})
	if err != nil {
		return err
	}
	policy.Spec.Enabled = opts.Enabled
	return r.client.Update(ctx, policy)
}
