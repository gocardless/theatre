package v1alpha1

import (
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
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
	// ToReleaseRef is the target release to rollback to. This is a reference to
	// the Release resource. If the Name field is left empty, the operator will pick
	// the latest healthy release for the specified Target to roll back to.
	// +kubebuilder:validation:Required
	ToReleaseRef ReleaseReference `json:"toReleaseRef"`

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
	DeploymentOptions map[string]apiextv1.JSON `json:"deploymentOptions,omitempty"`
}

// ReleaseReference is a reference to a Release resource
type ReleaseReference struct {
	// Target is the target name of the release. This is required to identify
	// which release target to operate on.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Target is immutable"
	Target string `json:"target"`

	// Name is the name of the release resource. If left empty, the system will
	// automatically select the appropriate release (e.g., the latest healthy release).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:XValidation:rule="oldSelf == '' || self == oldSelf",message="Name is immutable once set"
	Name string `json:"name,omitempty"`
}

// RollbackInitiator tracks who or what initiated the rollback
type RollbackInitiator struct {
	// Principal is the identifier of the person or system who initiated the rollback
	// +kubebuilder:validation:Optional
	Principal string `json:"principal,omitempty"`

	// Type indicates what type of principal initiated the rollback
	// (e.g. "user", "system")
	// +kubebuilder:validation:Optional
	Type string `json:"type,omitempty"`
}

// RollbackStatus defines the observed state of Rollback
type RollbackStatus struct {
	// Message is a human-readable message indicating the state of the rollback.
	Message string `json:"message,omitempty"`

	// FromReleaseRef is the release being rolled back from. This is a reference
	// to the Release resource name.
	FromReleaseRef ReleaseReference `json:"fromReleaseRef,omitempty"`

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
// +kubebuilder:printcolumn:name="From",type=string,JSONPath=`.status.fromReleaseRef.name`
// +kubebuilder:printcolumn:name="To",type=string,JSONPath=`.spec.toReleaseRef.name`
// +kubebuilder:printcolumn:name="Initiator",type=string,JSONPath=`.spec.initiatedBy.principal`
// +kubebuilder:printcolumn:name="Succeeded",type=string,JSONPath=`.status.conditions[?(@.type=="Succeeded")].status`
// +kubebuilder:printcolumn:name="Reason",type=string,JSONPath=`.spec.reason`,priority=10
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
