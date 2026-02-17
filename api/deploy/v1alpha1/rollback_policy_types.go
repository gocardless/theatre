package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// RollbackPolicySpec defines the desired state of RollbackPolicy
type RollbackPolicySpec struct {
}

// RollbackPolicyStatus defines the observed state of RollbackPolicy
type RollbackPolicyStatus struct {
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rbp

// RollbackPolicy is the Schema for the rollbackpolicies API.
type RollbackPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of RollbackPolicy
	// +required
	Spec RollbackPolicySpec `json:"spec"`

	// Status defines the observed state of RollbackPolicy
	// +optional
	Status RollbackPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RollbackPolicyList contains a list of RollbackPolicy
type RollbackPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RollbackPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RollbackPolicy{}, &RollbackPolicyList{})
}
