package v1alpha1

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DirectoryRoleBindingSpec defines the desired state of DirectoryRoleBinding
type DirectoryRoleBindingSpec struct {
	Subjects []rbacv1.Subject `json:"subjects"`
	RoleRef  rbacv1.RoleRef   `json:"roleRef"`
}

// DirectoryRoleBindingStatus defines the observed state of DirectoryRoleBinding
type DirectoryRoleBindingStatus struct{}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion

// DirectoryRoleBinding is the Schema for the directoryrolebindings API
type DirectoryRoleBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DirectoryRoleBindingSpec   `json:"spec,omitempty"`
	Status DirectoryRoleBindingStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DirectoryRoleBindingList contains a list of DirectoryRoleBinding
type DirectoryRoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DirectoryRoleBinding `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DirectoryRoleBinding{}, &DirectoryRoleBindingList{})
}
