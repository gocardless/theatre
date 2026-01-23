package v1alpha1

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/gocardless/theatre/v5/pkg/recutil"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	SignatureLength = 10
)

// Rollback helpers

func (rollback *Rollback) IsCompleted() bool {
	return recutil.IsConditionStatusKnown(rollback.Status.Conditions, []string{RollbackConditionSucceded})
}

// Release helpers

func (releaseConfig *ReleaseConfig) Equals(other *ReleaseConfig) bool {
	return bytes.Equal(releaseConfig.Serialise(), other.Serialise())
}

// The serialisation is used to determine if a release has changed.
// For release uniqueness we only take into consideration the target name,
// revision.name and revision.id.
func (releaseConfig *ReleaseConfig) Serialise() []byte {
	canonical := ReleaseConfig{
		TargetName: releaseConfig.TargetName,
		Revisions:  releaseConfig.Revisions,
	}

	for _, revision := range canonical.Revisions {
		var canonicalRevision Revision
		canonicalRevision.Name = revision.Name
		canonicalRevision.ID = revision.ID

		canonical.Revisions = append(canonical.Revisions, canonicalRevision)
	}

	slices.SortFunc(canonical.Revisions, func(a, b Revision) int {
		return cmp.Compare(a.Name, b.Name)
	})

	bytes, _ := json.Marshal(canonical)

	return bytes
}

func (release *Release) IsStatusInitialised() bool {
	return meta.FindStatusCondition(release.Status.Conditions, ReleaseConditionActive) != nil &&
		release.Status.Signature != ""
}

func (release *Release) IsAnalysisStatusKnown() bool {
	return recutil.IsConditionStatusKnown(release.Status.Conditions, []string{
		ReleaseConditionHealthy,
		ReleaseConditionRollbackRequired,
	})
}

func (release *Release) generateSignature() string {
	return fmt.Sprintf("%x", sha256.Sum256(release.ReleaseConfig.Serialise()))
}

func (release *Release) InitialiseStatus(message string) {
	if message == "" {
		message = "Release initialised successfully"
	}
	release.Status.Message = message
	release.Status.Signature = release.generateSignature()[:SignatureLength]

	release.setConditionActive(metav1.ConditionUnknown, ReasonInitialised, message)
	release.setConditionHealthy(metav1.ConditionUnknown, ReasonInitialised, message)
}

func (release *Release) SetDeploymentStartTime(timestamp metav1.Time) {
	release.Status.DeploymentStartTime = timestamp
}

func (release *Release) SetDeploymentEndTime(timestamp metav1.Time) {
	release.Status.DeploymentEndTime = timestamp
}

func (release *Release) Activate(message string) {
	release.Status.Message = message
	release.setConditionActive(metav1.ConditionTrue, ReasonDeployed, message)
}

func (release *Release) Deactivate(message string) {
	release.Status.Message = message
	release.setConditionActive(metav1.ConditionFalse, ReasonSuperseded, message)
}

func (release *Release) IsConditionActiveTrue() bool {
	return meta.IsStatusConditionTrue(release.Status.Conditions, ReleaseConditionActive)
}

func (release *Release) setConditionActive(status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&release.Status.Conditions, metav1.Condition{
		Type:    ReleaseConditionActive,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func (release *Release) setConditionHealthy(status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&release.Status.Conditions, metav1.Condition{
		Type:    ReleaseConditionHealthy,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func (release *Release) SetPreviousRelease(previousRelease string) {
	release.Status.PreviousRelease.ReleaseRef = previousRelease
	if previousRelease != "" {
		release.Status.PreviousRelease.TransitionTime = metav1.Now()
	} else {
		release.Status.PreviousRelease.TransitionTime = metav1.Time{}
	}
}
