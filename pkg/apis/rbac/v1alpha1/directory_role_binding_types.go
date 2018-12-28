package v1alpha1

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// DirectoryRoleBinding only extends the standard RoleBinding resource provided by
// Kubernetes. We shouldn't ever require other fields here.
type DirectoryRoleBinding rbacv1.RoleBinding

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type DirectoryRoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []DirectoryRoleBinding `json:"items"`
}
