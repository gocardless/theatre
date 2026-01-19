package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *Release) IsCompleted() bool {
	activeCondition := meta.FindStatusCondition(r.Status.Conditions, ReleaseConditionActive)
	return activeCondition != nil && activeCondition.Status != metav1.ConditionUnknown
}

func FindActiveRelease(releaseList *ReleaseList) *Release {
	for _, release := range releaseList.Items {
		if meta.IsStatusConditionTrue(release.Status.Conditions, ReleaseConditionActive) {
			return &release
		}
	}
	return nil
}

// FindLastHealthyRelease walks back from the active release using PreviousRelease
// to find the most recent healthy release that is not the active release itself
func FindLastHealthyRelease(releaseList *ReleaseList) *Release {
	activeRelease := FindActiveRelease(releaseList)

	releaseMap := make(map[string]*Release)
	for i := range releaseList.Items {
		release := &releaseList.Items[i]
		releaseMap[release.Name] = release
	}

	// Start from the previous release of the active one
	prevRef := activeRelease.Status.PreviousRelease.ReleaseRef
	if prevRef == "" {
		return nil
	}

	// Walk back through the release chain
	visited := make(map[string]bool)
	currentRef := prevRef

	for currentRef != "" && !visited[currentRef] {
		visited[currentRef] = true

		release, ok := releaseMap[currentRef]
		if !ok {
			// Release not found, stop walking
			break
		}

		// Check if this release is healthy
		if meta.IsStatusConditionTrue(release.Status.Conditions, ReleaseConditionHealthy) {
			return release
		}

		// Move to the previous release
		currentRef = release.Status.PreviousRelease.ReleaseRef
	}

	return nil
}
