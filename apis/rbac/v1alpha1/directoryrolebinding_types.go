/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

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
