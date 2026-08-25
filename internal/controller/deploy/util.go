package deploy

import (
	"context"
	"fmt"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GetReleasesForTarget retrieves all releases for a given target
func GetReleasesForTarget(ctx context.Context, c client.Client, namespace, targetName string) (*deployv1alpha1.ReleaseList, error) {
	releaseList := &deployv1alpha1.ReleaseList{}
	if err := c.List(ctx, releaseList,
		client.InNamespace(namespace),
		client.MatchingFields{IndexFieldReleaseTarget: targetName},
	); err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	return releaseList, nil
}
