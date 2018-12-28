package v1alpha1

import (
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SudoRoleBinding provides a concept of temporarily sudo-ing into a role binding provided
// the user is within a specific ACL.
type SudoRoleBinding struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SudoRoleBindingSpec   `json:"spec"`
	Status SudoRoleBindingStatus `json:"status"`
}

type SudoRoleBindingSpec struct {
	Expiry      *int64             `json:"expiry"`
	RoleBinding rbacv1.RoleBinding `json:"roleBinding"`
}

type SudoRoleBindingStatus struct {
	Grants []SudoRoleBindingGrant `json:"grants"`
}

type SudoRoleBindingGrant struct {
	Subject rbacv1.Subject `json:"subject"`
	Expiry  string         `json:"expiry"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type SudoRoleBindingList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []SudoRoleBinding `json:"items"`
}
