package v1alpha1

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	SignatureLength = 10
)

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

	r.SetConditionActive(metav1.ConditionUnknown, ReasonInitialised, message)
	r.SetConditionHealthy(metav1.ConditionUnknown, ReasonInitialised, message)
}

func (r *Release) ParseAnnotations() (changed bool, errors []error) {
	if r.AnnotatedWithSetDeploymentStartTime() {
		startTime, err := time.Parse(time.RFC3339, r.Annotations[AnnotationKeyReleaseSetDeploymentStartTime])
		if err != nil {
			errors = append(errors, err)
		} else {
			r.Status.DeploymentStartTime = metav1.NewTime(startTime)
			changed = true
		}
	}

	if r.AnnotatedWithSetDeploymentEndTime() {
		endTime, err := time.Parse(time.RFC3339, r.Annotations[AnnotationKeyReleaseSetDeploymentEndTime])
		if err != nil {
			errors = append(errors, err)
		} else {
			r.Status.DeploymentEndTime = metav1.NewTime(endTime)
			changed = true
		}
	}

	return changed, errors
}

func (r *Release) AnnotatedWithActivate() bool {
	_, ok := r.Annotations[AnnotationKeyReleaseActivate]
	return ok
}

func (r *Release) AnnotatedWithSetDeploymentStartTime() bool {
	_, ok := r.Annotations[AnnotationKeyReleaseSetDeploymentStartTime]
	return ok
}

func (r *Release) AnnotatedWithSetDeploymentEndTime() bool {
	_, ok := r.Annotations[AnnotationKeyReleaseSetDeploymentEndTime]
	return ok
}

func (r *Release) SetDeploymentStartTime(timestamp metav1.Time) {
	r.Status.DeploymentStartTime = timestamp
}

func (r *Release) SetDeploymentEndTime(timestamp metav1.Time) {
	r.Status.DeploymentEndTime = timestamp
}

func (r *Release) Activate(message string, previousRelease *Release) {
	r.Status.Message = message
	if previousRelease != nil {
		r.Status.PreviousRelease = ReleaseTransition{
			ReleaseRef:     previousRelease.Name,
			TransitionTime: metav1.Now(),
		}
	}

	// This is the current active release, so it has no next release
	r.Status.NextRelease = ReleaseTransition{}
	r.SetConditionActive(metav1.ConditionTrue, ReasonDeployed, message)
}

func (r *Release) IsConditionActive() bool {
	activeCondition := meta.FindStatusCondition(r.Status.Conditions, ReleaseConditionActive)
	if activeCondition == nil {
		return false
	}

	return activeCondition.Status == metav1.ConditionTrue
}

func (r *Release) SetConditionActive(status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{
		Type:    ReleaseConditionActive,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func (r *Release) SetConditionHealthy(status metav1.ConditionStatus, reason, message string) {
	meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{
		Type:    ReleaseConditionHealthy,
		Status:  status,
		Reason:  reason,
		Message: message,
	})
}

func (r *Release) Deactivate(message string, nextRelease *Release) {
	r.Status.Message = message

	if nextRelease != nil {
		r.Status.NextRelease = ReleaseTransition{
			ReleaseRef:     nextRelease.Name,
			TransitionTime: metav1.Now(),
		}
	}

	r.SetConditionActive(metav1.ConditionFalse, ReasonSuperseded, message)
}

// Returns the effective time of the release, which is the deployment end time,
// if set, otherwise the creation time.
func (r *Release) GetEffectiveTime() time.Time {
	if r.Status.DeploymentEndTime.IsZero() {
		return r.ObjectMeta.CreationTimestamp.Time
	}
	return r.Status.DeploymentEndTime.Time
}
