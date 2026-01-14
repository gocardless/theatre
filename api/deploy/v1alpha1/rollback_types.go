package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// Condition types for Rollback resources
const (
	// RollbackConditionInProgress indicates whether the rollback is currently in progress.
	// Status=True means the rollback is in progress (e.g. the ArgoCD sync is in progress).
	// Status=False means the rollback is yet to start or has completed.
	RollbackConditionInProgress = "InProgress"

	// RollbackConditionSucceded indicates whether the rollback has succeeded.
	// Status=True means the rollback has succeeded.
	// Status=False means the rollback has not failed.
	RollbackConditionSucceded = "Succeeded"
)

// RollbackSpec defines the desired state of Rollback
type RollbackSpec struct {
	// ToReleaseName is the target release to rollback to. This is a reference to
	// the Release resource name. If left empty, the operator will pick the latest
	// healthy release to roll back to.
	// +kubebuilder:validation:Optional
	ToReleaseName string `json:"toReleaseName,omitempty"`

	// Reason is a human-readable message explaining why the rollback was initiated.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=512
	Reason string `json:"reason"`

	// InitiatedBy tracks who or what triggered the rollback for audit purposes.
	// +kubebuilder:validation:Optional
	InitiatedBy RollbackInitiator `json:"initiatedBy,omitempty"`

	// DeploymentOptions contains additional provider-specific options.
	// +kubebuilder:validation:Optional
	DeploymentOptions map[string]string `json:"deploymentOptions,omitempty"`
}

// RollbackInitiator tracks who or what initiated the rollback
type RollbackInitiator struct {
	// User is the username or email of the person who initiated the rollback.
	// +kubebuilder:validation:Optional
	User string `json:"user,omitempty"`

	// System is the automated system that initiated the rollback, if applicable
	// (e.g., "health-check-policy", "canary-analysis").
	// +kubebuilder:validation:Optional
	System string `json:"system,omitempty"`
}

// RollbackStatus defines the observed state of Rollback
type RollbackStatus struct {
	// Message is a human-readable message indicating the state of the rollback.
	Message string `json:"message,omitempty"`

	// FromReleaseName is the release being rolled back from. This is a reference
	// to the Release resource name.
	FromReleaseName string `json:"fromReleaseName"`

	// Automatic indicates whether this rollback was triggered automatically
	// (e.g., by a health check) or manually by a user.
	Automatic bool `json:"automatic,omitempty"`

	// StartTime is when the rollback operation started.
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the rollback operation completed (successfully or failed).
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// DeploymentID is the unique identifier for the deployment in the CICD system.
	// This is used to poll for deployment status.
	DeploymentID string `json:"deploymentID,omitempty"`

	// DeploymentURL is the URL to the CI job performing the rollback.
	DeploymentURL string `json:"deploymentURL,omitempty"`

	// AttemptCount tracks how many times the controller has attempted to
	// initiate the rollback via the CI system.
	AttemptCount int32 `json:"attemptCount,omitempty"`

	// Conditions represent the latest observations of the rollback's state.
	// Known condition types are: "InProgress", "Succeeded".
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rb
// +kubebuilder:printcolumn:name="From",type=string,JSONPath=`.status.fromReleaseName`
// +kubebuilder:printcolumn:name="To",type=string,JSONPath=`.spec.toReleaseName`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.spec.reason`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Rollback is the Schema for the rollbacks API. It represents a historical
// record of a release rollback operation.
type Rollback struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of Rollback
	// +required
	Spec RollbackSpec `json:"spec"`

	// Status defines the observed state of Rollback
	// +optional
	Status RollbackStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RollbackList contains a list of Rollback
type RollbackList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Rollback `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Rollback{}, &RollbackList{})
}
