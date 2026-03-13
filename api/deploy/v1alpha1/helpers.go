package v1alpha1

import (
	"bytes"
	"cmp"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"time"

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

// GetEffectiveTime returns the effective time of the rollback, which is the completion time
// if set, otherwise the creation time.
func (rollback *Rollback) GetEffectiveTime() time.Time {
	if rollback.Status.CompletionTime.IsZero() {
		return rollback.ObjectMeta.CreationTimestamp.Time
	}
	return rollback.Status.CompletionTime.Time
}

func FindInProgressRollback(rollbackList *RollbackList) *Rollback {
	for _, rollback := range rollbackList.Items {
		if meta.IsStatusConditionTrue(rollback.Status.Conditions, RollbackConditionInProgress) {
			return &rollback
		}
	}
	return nil
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

func (release *Release) SetPreviousRelease(previousRelease string) {
	release.Status.PreviousRelease.ReleaseRef = previousRelease
	if previousRelease != "" {
		release.Status.PreviousRelease.TransitionTime = metav1.Now()
	} else {
		release.Status.PreviousRelease.TransitionTime = metav1.Time{}
	}
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
	if activeRelease == nil {
		return nil
	}

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

// Returns the effective time of the release, which is the deployment end time,
// if set, otherwise the creation time.
func (r *Release) GetEffectiveTime() time.Time {
	if r.Status.DeploymentEndTime.IsZero() {
		return r.ObjectMeta.CreationTimestamp.Time
	}
	return r.Status.DeploymentEndTime.Time
}

// AutomatedRollbackPolicy helpers

// PolicyEvaluation is used as a result of evaluating the constraints of an automated rollback policy.
// It indicates whether the policy is allowed to trigger with the relevant reason and message.
// +kubebuilder:object:generate=false
type PolicyEvaluation struct {
	Allowed bool
	Reason  string
	Message string
}

// EvaluatePolicyConstraints evaluates the constraints of the automated rollback policy
// and returns a policy evaluation, which indicates whether the policy is allowed to trigger
// with the relevant reason and message.
func (policy *AutomatedRollbackPolicy) EvaluatePolicyConstraints(release *Release) PolicyEvaluation {
	if !policy.Spec.Enabled {
		return PolicyEvaluation{
			Allowed: false,
			Reason:  AutomatedRollbackPolicyReasonSetByUser,
			Message: "Automated rollback policy is disabled",
		}
	}

	// Check if the trigger condition on the release has recovered and whether automated rollback can be re-enabled
	if release != nil && meta.IsStatusConditionFalse(policy.Status.Conditions, AutomatedRollbackPolicyConditionActive) {

		releaseRecovered := recutil.IsConditionStatusKnown(release.Status.Conditions, []string{string(policy.Spec.Trigger.ConditionType)}) &&
			!meta.IsStatusConditionPresentAndEqual(release.Status.Conditions, policy.Spec.Trigger.ConditionType, policy.Spec.Trigger.ConditionStatus)
		automatedCond := meta.FindStatusCondition(policy.Status.Conditions, AutomatedRollbackPolicyConditionActive)

		if !releaseRecovered {
			return PolicyEvaluation{
				Allowed: false,
				Reason:  automatedCond.Reason,
				Message: automatedCond.Message,
			}
		}
	}

	return PolicyEvaluation{
		Allowed: true,
		Reason:  AutomatedRollbackPolicyReasonSetByUser,
		Message: "Automated rollback is enabled",
	}
}
