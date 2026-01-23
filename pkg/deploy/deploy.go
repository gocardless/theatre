package deploy

import (
	"crypto/sha256"
	"fmt"
	"strings"

	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// validateTargetName validates that the target name is within the allowed length
// and follows DNS subdomain rules, with some buffer for suffixes.
// The target name must be at most (DNS1123SubdomainMaxLength - 8) characters to allow
// for suffixes like "-18c1c1d" to be appended safely.
func validateTargetName(targetName string) error {
	if len(targetName) > (validation.DNS1123SubdomainMaxLength - 8) {
		return fmt.Errorf("target name too long: %d characters (max %d)", len(targetName), validation.DNS1123SubdomainMaxLength)
	}

	errors := validation.IsDNS1123Subdomain(targetName)
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

func hashString(b []byte) string {
	hash := sha256.Sum256(b)
	return fmt.Sprintf("%x", hash)[:7]
}

// Generates a name for a release based on its target name and revision IDs.
// The generated name hash the format of `{targetName}-{hash}`, where hash is a
// SHA-256 hash of the appended revision Names and IDs.
func GenerateReleaseName(release deployv1alpha1.Release) (string, error) {
	// Sort revision IDs to ensure consistent ordering
	targetName := release.ReleaseConfig.TargetName
	if err := validateTargetName(targetName); err != nil {
		return "", err
	}

	revisions := release.ReleaseConfig.DeepCopy().Revisions
	if err := validateRevisions(revisions); err != nil {
		return "", err
	}

	releaseHash := hashString(release.Serialise())

	return fmt.Sprintf("%s-%s", targetName, releaseHash), nil
}

// GenerateAnalysisRunName generates a name for an AnalysisRun by concatenating the
// release name and template. If the result would be too long, parts are trimmed
// to 27 characters and a hash is appended.
// 27 char maximum is to ensure final name doesn't exceed 64 characters:
// <release>-<template>-<hash>
func GenerateAnalysisRunName(releaseName, templateName string) string {
	parts := []string{releaseName, templateName}
	candidate := strings.Join(parts, "-")

	if len(candidate) <= 64 {
		return candidate
	}

	hash := hashString([]byte(releaseName + templateName))

	for i, v := range parts {
		if len(v) > 27 {
			parts[i] = v[:27]
		}
	}

	parts = append(parts, hash)

	return strings.Join(parts, "-")
}
