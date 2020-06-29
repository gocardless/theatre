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
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConsoleAuthorisationSpec defines the desired state of ConsoleAuthorisation
type ConsoleAuthorisationSpec struct {
	// The reference to the console by name that this console authorisation belongs to.
	ConsoleRef corev1.LocalObjectReference `json:"consoleRef"`

	// List of authorisations that have been given to the referenced console.
	Authorisations []rbacv1.Subject `json:"authorisations"`
}

// ConsoleAuthorisationStatus defines the observed state of ConsoleAuthorisation
type ConsoleAuthorisationStatus struct{}

// +kubebuilder:object:root=true

// ConsoleAuthorisation is the Schema for the consoleauthorisations API
type ConsoleAuthorisation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConsoleAuthorisationSpec   `json:"spec,omitempty"`
	Status ConsoleAuthorisationStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConsoleAuthorisationList contains a list of ConsoleAuthorisation
type ConsoleAuthorisationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConsoleAuthorisation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConsoleAuthorisation{}, &ConsoleAuthorisationList{})
}
