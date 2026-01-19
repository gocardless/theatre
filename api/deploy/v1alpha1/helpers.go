package v1alpha1

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	SignatureLength = 10
)

func (r *Rollback) IsCompleted() bool {
	succeededCondition := meta.FindStatusCondition(r.Status.Conditions, RollbackConditionSucceded)
	return succeededCondition != nil && succeededCondition.Status != metav1.ConditionUnknown
}

func (rc *ReleaseConfig) Equals(other *ReleaseConfig) bool {
	return bytes.Equal(rc.Serialise(), other.Serialise())
}

// The serialisation is used to determine if a release has changed.
// For release uniqueness we only take into consideration the target name,
// revision.name and revision.id.
func (rc *ReleaseConfig) Serialise() []byte {
	canonical := ReleaseConfig{
		TargetName: rc.TargetName,
		Revisions:  rc.Revisions,
	}

	for _, revision := range canonical.Revisions {
		var canonicalRevision Revision
		canonicalRevision.Name = revision.Name
		canonicalRevision.ID = revision.ID

		canonical.Revisions = append(canonical.Revisions, canonicalRevision)
	}

	sort.Slice(canonical.Revisions, func(i, j int) bool {
		return canonical.Revisions[i].Name < canonical.Revisions[j].Name
	})

	bytes, _ := json.Marshal(canonical)

	return bytes
}

func (r *Release) IsStatusInitialised() bool {
	return len(r.Status.Conditions) > 0
}

func (r *Release) generateSignature() string {
	return fmt.Sprintf("%x", sha256.Sum256(r.ReleaseConfig.Serialise()))
}

func (r *Release) InitialiseStatus(message string) {
	if message == "" {
		message = "Release initialised successfully"
	}
	r.Status.Message = message
	r.Status.Signature = r.generateSignature()[:SignatureLength]

	r.setConditionActive(metav1.ConditionUnknown, ReasonInitialised, message)
	r.setConditionHealthy(metav1.ConditionUnknown, ReasonInitialised, message)
}

func (r *Release) SetDeploymentStartTime(timestamp metav1.Time) {
	r.Status.DeploymentStartTime = timestamp
}

func (r *Release) SetDeploymentEndTime(timestamp metav1.Time) {
	r.Status.DeploymentEndTime = timestamp
}

func (r *Release) Activate(message string) {
	r.Status.Message = message
	r.setConditionActive(metav1.ConditionTrue, ReasonDeployed, message)
}

func (r *Release) Deactivate(message string) {
	r.Status.Message = message
	r.setConditionActive(metav1.ConditionFalse, ReasonSuperseded, message)
}

func (r *Release) IsConditionActive() bool {
	return meta.IsStatusConditionTrue(r.Status.Conditions, ReleaseConditionActive)
}

func (r *Release) setConditionActive(status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{
		Type:    ReleaseConditionActive,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func (r *Release) setConditionHealthy(status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{
		Type:    ReleaseConditionHealthy,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func (r *Release) GetPreviousRelease() string {
	return r.Status.PreviousRelease.ReleaseRef
}

func (r *Release) SetPreviousRelease(previousRelease string) {
	r.Status.PreviousRelease.ReleaseRef = previousRelease
	if previousRelease != "" {
		r.Status.PreviousRelease.TransitionTime = metav1.Now()
	} else {
		r.Status.PreviousRelease.TransitionTime = metav1.Time{}
	}
}
