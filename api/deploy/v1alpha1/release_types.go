package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ReleaseConfig defines the desired state of Release
type ReleaseConfig struct {
	// TargetName is a namespace-unique identifier for this release target
	// +kubebuilder:validation:Required
	TargetName string `json:"targetName"`

	// Revisions is a map of revision names to their specifications
	// Each source must be unique across all revisions
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=10
	Revisions map[string]Revision `json:"revisions"`
}

type RevisionType string

type Revision struct {
	// ID is the unique identifier of the revision (e.g., commit SHA, image digest, chart version)
	// +kubebuilder:validation:Required
	ID string `json:"id"`

	// Source identifies where this revision comes from (e.g., repository URL, registry URL)
	// +kubebuilder:validation:Optional
	Source string `json:"source"`

	// Type specifies the kind of revision source (git, container_image, helm_chart)
	// +kubebuilder:validation:Optional
	Type RevisionType `json:"type"`

	// Metadata contains additional optional information about the revision
	// +kubebuilder:validation:Optional
	Metadata RevisionMetadata `json:"metadata,omitempty"`
}

type RevisionMetadata struct {
	// Author is the author of the commit, if available. The field is optional.
	// +kubebuilder:validation:Optional
	Author string `json:"author,omitempty"`

	// Branch is the branch of the commit, if available. The field is optional.
	// +kubebuilder:validation:Optional
	Branch string `json:"branch,omitempty"`

	// Message is the message of the commit, if available. The field is optional.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`

	// Tags are additional tags associated with this revision
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=10
	Tags []string `json:"tags,omitempty"`
}

// ReleaseStatus defines the observed state of Release.

type Phase string

const (
	PhaseActive   Phase = "Active"
	PhaseInactive Phase = "Inactive"
)

type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "Healthy"
	HealthStatusUnhealthy HealthStatus = "Unhealthy"
	HealthStatusUnknown   HealthStatus = "Unknown"
)

type ReleaseStatusEntry struct {
	// Phase is the current phase of the release. Active indicates the release
	// is currently live in the cluster, Inactive indicates the release is no
	// longer the latest release.
	// +kubebuilder:validation:Enum:=Active;Inactive
	// +kubebuilder:validation:Required
	Phase Phase `json:"phase"`

	// Message is a human-readable message indicating the state of the release.
	// +kubebuilder:validation:Optional
	Message string `json:"message,omitempty"`

	// DeploymentStartTime is the time when the release was started.
	// +kubebuilder:validation:Optional
	DeploymentStartTime metav1.Time `json:"deploymentStartTime,omitempty"`

	// DeploymentEndTime is the time when the release was completed.
	// +kubebuilder:validation:Optional
	DeploymentEndTime metav1.Time `json:"deploymentEndTime,omitempty"`

	// SupersededBy is the name of the release that superseded this release.
	// +kubebuilder:validation:Optional
	SupersededBy string `json:"supersededBy,omitempty"`

	// SupersededTime is the time when this release was superseded.
	// +kubebuilder:validation:Optional
	SupersededTime metav1.Time `json:"supersededTime,omitempty"`

	// HealthStatus indicates whether the release is healthy or not, as determined by an external monitoring system.
	// +kubebuilder:validation:Enum:=Healthy;Unhealthy;Unknown
	// +kubebuilder:validation:Optional
	// +kubebuilder:default:=Unknown
	HealthStatus HealthStatus `json:"healthStatus,omitempty"`

	// HealthStatusLastChecked is the last time the health status was checked by the external system.
	// +kubebuilder:validation:Optional
	HealthStatusLastChecked metav1.Time `json:"healthStatusLastChecked,omitempty"`
}

type ReleaseStatus struct {
	ReleaseStatusEntry `json:",inline"`

	// History is a list of previous statuses of the release.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=50
	History []ReleaseStatusEntry `json:"history,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
type Release struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// ReleaseConfig the release configuration
	// +required
	ReleaseConfig `json:"config,omitempty,omitzero"`

	// Status defines the observed state of Release
	// +optional
	Status ReleaseStatus `json:"status,omitempty,omitzero"`
}

// +kubebuilder:object:root=true

// ReleaseList contains a list of Release
type ReleaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Release `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Release{}, &ReleaseList{})
}
