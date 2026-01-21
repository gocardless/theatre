package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// Condition types for Release resources
const (
	// ReleaseConditionActive indicates whether the release is currently active in the cluster.
	// Status=True means the release is actively serving traffic.
	// Status=False means the release has been superseded by another release.
	ReleaseConditionActive = "Active"

	// ReleaseConditionHealthy indicates whether the release has passed health analysis.
	// Status=True means the release passed health checks/analysis.
	// Status=False means the release failed health checks/analysis.
	// Status=Unknown means health status has not been determined yet.
	ReleaseConditionHealthy = "Healthy"

	// Reasons for condition status changes

	// ReasonInitialised indicates the release was successfully initialised.
	ReasonInitialised = "Initialised"

	// ReasonDeployed indicates the release was successfully deployed and is now active.
	ReasonDeployed = "Deployed"

	// ReasonSuperseded indicates the release was superseded by a different release.
	ReasonSuperseded = "Superseded"

	// ReasonRollback indicates the release is active due to a rollback
	ReasonRollback = "Rollback"

	// ReasonAnalysisSucceeded indicates the release passed health analysis checks.
	ReasonAnalysisSucceeded = "AnalysisSucceeded"

	// ReasonAnalysisFailed indicates the release failed health analysis checks.
	ReasonAnalysisFailed = "AnalysisFailed"
)

// ReleaseConfig defines the desired state of Release
type ReleaseConfig struct {
	// TargetName is a namespace-unique identifier for this release target
	// +kubebuilder:validation:Required
	TargetName string `json:"targetName"`

	// Revisions is a list of revisions to be released. Each revision.name must be
	// unique across all revisions
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	Revisions []Revision `json:"revisions"`
}
type Revision struct {
	// Name is unique identifier for this revision. E.g. application-revision, chart-revision, etc.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// ID is the unique identifier of the revision (e.g., commit SHA, image digest, chart version)
	// +kubebuilder:validation:Required
	ID string `json:"id"`

	// Source identifies where this revision comes from (e.g., repository URL, registry URL)
	// +kubebuilder:validation:Optional
	Source string `json:"source"`

	// Type specifies the kind of revision source (git, container_image, helm_chart)
	// +kubebuilder:validation:Optional
	Type string `json:"type"`

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
}

// This is common struct type used to indicate any previous and next releases
type ReleaseTransition struct {
	// Other Release associated with this transition
	ReleaseRef string `json:"releaseRef,omitempty"`
	// When the release transitioned to this state
	TransitionTime metav1.Time `json:"transitionTime,omitempty"`
}

type ReleaseStatus struct {
	// Conditions represent the latest available observations of a release's state.
	// Known conditions are:
	// * Active
	// * Healthy
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:listType=map
	// +kubebuilder:validation:listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Message is a human-readable message indicating the state of the release.
	Message string `json:"message,omitempty"`

	// DeploymentStartTime is the time when the release was started.
	DeploymentStartTime metav1.Time `json:"deploymentStartTime,omitempty"`

	// DeploymentEndTime is the time when the release was completed.
	DeploymentEndTime metav1.Time `json:"deploymentEndTime,omitempty"`

	// PreviousRelease is the name of the release that was superseded by this release.
	PreviousRelease ReleaseTransition `json:"previousRelease,omitempty"`

	// NextRelease is the name of the release that superseded this release.
	NextRelease ReleaseTransition `json:"nextRelease,omitempty"`

	// Signature is deterministic hash constructed out of the release revisions.
	// The signature is constructed out of the sum of names and ids of each revision.
	Signature string `json:"signature,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:printcolumn:name="Target_Name",type="string",JSONPath=".config.targetName"
// +kubebuilder:printcolumn:name="Active",type="string",JSONPath=".status.conditions[?(@.type==\"Active\")].status"
// +kubebuilder:printcolumn:name="Healthy",type="string",JSONPath=".status.conditions[?(@.type==\"Healthy\")].status"
// +kubebuilder:printcolumn:name="Signature",format="",type="string",JSONPath=".status.signature"
// +kubebuilder:printcolumn:name="Started_At",type="string",JSONPath=".status.previousRelease.transitionTime"
// +kubebuilder:printcolumn:name="Ended_At",type="string",JSONPath=".status.nextRelease.transitionTime"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
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
