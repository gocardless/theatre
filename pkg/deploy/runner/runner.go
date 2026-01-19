package runner

import (
	"context"
	"sort"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
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
