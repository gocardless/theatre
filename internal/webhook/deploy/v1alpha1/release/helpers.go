package release

import (
	"crypto/sha256"
	"fmt"
	"sort"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// validateTargetName validates that the target name is within the allowed length
// and follows DNS subdomain rules, with some buffer for suffixes.
// The target name must be at most (DNS1123SubdomainMaxLength - 7) characters to allow
// for suffixes like "-release" to be appended safely.
func validateTargetName(targetName string) error {
	if len(targetName) > (validation.DNS1123SubdomainMaxLength - 7) {
		return fmt.Errorf("target name too long: %d characters (max %d)", len(targetName), validation.DNS1123SubdomainMaxLength)
	}

	errors := validation.IsDNS1123Label(targetName)
	if len(errors) > 0 {
		return fmt.Errorf("invalid target name: %s", errors[0])
	}

	return nil
}

func validateRevisionID(revisionID string) error {
	if revisionID == "" {
		return fmt.Errorf("revision ID cannot be empty")
	}
	return nil
}

// Ensures there are no repeated revision names and revision IDs are valid
func validateRevisions(revisions []deployv1alpha1.Revision) error {
	revisionNames := make(map[string]bool)

	if len(revisions) == 0 {
		return fmt.Errorf("revisions cannot be empty")
	}

	for _, revision := range revisions {
		if revisionNames[revision.Name] {
			return fmt.Errorf("duplicate revision name: %s", revision.Name)
		}
		revisionNames[revision.Name] = true

		if err := validateRevisionID(revision.ID); err != nil {
			return err
		}
	}
	return nil
}

func hashString(s string) string {
	// Placeholder implementation - will be replaced with actual hashing
	hash := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", hash)[:7]
}

func generateReleaseName(release deployv1alpha1.Release) (string, error) {
	// Sort revision IDs to ensure consistent ordering
	targetName := release.ReleaseConfig.TargetName
	if err := validateTargetName(targetName); err != nil {
		return "", err
	}

	revisions := release.ReleaseConfig.DeepCopy().Revisions
	if err := validateRevisions(revisions); err != nil {
		return "", err
	}

	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].Name < revisions[j].Name
	})

	uniqueReleaseIdentifier := ""
	for _, revision := range revisions {
		uniqueReleaseIdentifier += revision.Name
		uniqueReleaseIdentifier += revision.ID
	}

	releaseHash := hashString(uniqueReleaseIdentifier)

	return fmt.Sprintf("%s-%s", targetName, releaseHash), nil
}
