package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConsoleAuthorisationRule declares rules specifying what commands need to be authorised and by whom.
type ConsoleAuthorisationRule struct {
	// Human readable name of authorisation rule added to logs for auditing.
	Name string `json:"name"`

	// The matching rule to compare to the command and arguments of the console.
	//
	// This uses basic wildcard matching: Each element of the array is evaluated
	// against the corresponding element of the console's `spec.command` field.
	// An element consisting of a single `*` character will assert on the
	// presence of an element, but will allow any contents.
	// An element consisting of `**`, at the end of the match array, will match 0
	// or more additional elements in the command, but can only be used at the
	// end of the rule.
	//
	// Pattern matching _within_ elements is deliberately not supported, as this
	// makes it much harder to construct rules which are secure and do not allow chaining of additional commands (e.g. in a shell context).
	//
	// +kubebuilder:validation:MinItems=1
	MatchCommandElements []string `json:"matchCommandElements"`

	ConsoleAuthorisers `json:",inline"`
}

// ConsoleAuthorisers declares the subjects required to perform authorisations.
type ConsoleAuthorisers struct {
	// The number of authorisations required from members of the subjects before the console can run.
	AuthorisationsRequired int `json:"authorisationsRequired"`

	// List of subjects that can provide authorisation for the console command to run.
	Subjects []rbacv1.Subject `json:"subjects"`
}

// PodTemplatePreserveMetadataSpec describes the data a pod should have when created from a template
type PodTemplatePreserveMetadataSpec struct {
	// +kubebuilder:pruning:PreserveUnknownFields
	// Standard object's metadata. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Specification of the desired behavior of the pod. More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#spec-and-status
	Spec corev1.PodSpec `json:"spec,omitempty"`
}

// ConsoleTemplateSpec defines the desired state of ConsoleTemplate
type ConsoleTemplateSpec struct {
	Template PodTemplatePreserveMetadataSpec `json:"template"`

	// Default time, in seconds, that a Console will be created for.
	// Maximum value of 1 week (as per MaxTimeoutSeconds).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=604800
	DefaultTimeoutSeconds int `json:"defaultTimeoutSeconds"`

	// Maximum time, in seconds, that a Console can be created for.
	// Maximum value of 1 week.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=604800
	MaxTimeoutSeconds        int              `json:"maxTimeoutSeconds"`
	AdditionalAttachSubjects []rbacv1.Subject `json:"additionalAttachSubjects,omitempty"`

	// Specifies the TTL before running for any Console created with this
	// template. If set, the Console will be eligible for garbage collection
	// TTLSecondsBeforeRunning seconds if it has not progressed to the Running
	// phase. If not set, this value defaults to 60 minutes. This field is
	// modeled on the TTL mechanism in Kubernetes 1.12.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=86400
	DefaultTTLSecondsBeforeRunning *int32 `json:"defaultTtlSecondsBeforeRunning,omitempty"`

	// Specifies the TTL for any Console created with this template. If set, the
	// Console will be eligible for garbage collection
	// DefaultTTLSecondsAfterFinished seconds after it enters the Stopped or
	// Destroyed phase. If not set, this value defaults to 24 hours. This field
	// is modeled closely on the TTL mechanism in Kubernetes 1.12.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=604800
	DefaultTTLSecondsAfterFinished *int32 `json:"defaultTtlSecondsAfterFinished,omitempty"`

	// List of authorisation rules to match against in order from top to bottom.
	// +optional
	AuthorisationRules []ConsoleAuthorisationRule `json:"authorisationRules,omitempty"`
	// Default authorisation rule to use if no authorisation rules are defined or no authorisation rules match.
	// +optional
	DefaultAuthorisationRule *ConsoleAuthorisers `json:"defaultAuthorisationRule,omitempty"`
}

// ConsoleTemplateStatus defines the observed state of ConsoleTemplate
type ConsoleTemplateStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion

// ConsoleTemplate is the Schema for the consoletemplates API
type ConsoleTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConsoleTemplateSpec   `json:"spec,omitempty"`
	Status ConsoleTemplateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConsoleTemplateList contains a list of ConsoleTemplate
type ConsoleTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConsoleTemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConsoleTemplate{}, &ConsoleTemplateList{})
}
