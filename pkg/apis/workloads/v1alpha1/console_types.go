package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Console declares an instance of a console environment to be created by a specific user
type Console struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConsoleSpec   `json:"spec"`
	Status ConsoleStatus `json:"status,omitempty"`
}

// ConsoleSpec defines the specification for a console
type ConsoleSpec struct {
	User               string                      `json:"user"`
	Reason             string                      `json:"reason"`
	TimeoutSeconds     int                         `json:"timeoutSeconds"`
	ConsoleTemplateRef corev1.LocalObjectReference `json:"consoleTemplateRef"`
}

// ConsoleStatus defines the status of a created console, populated at runtime
type ConsoleStatus struct {
	PodName    string       `json:"podName"`
	ExpiryTime *metav1.Time `json:"expiryTime,omitempty"`
	Phase      string       `json:"phase"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConsoleList is a list of consoles
type ConsoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Console `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConsoleTemplate declares a console template that can be instantiated through a Console object
type ConsoleTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ConsoleTemplateSpec `json:"spec"`
}

// ConsoleTemplateSpec defines the parameters that a created console will adhere to
type ConsoleTemplateSpec struct {
	Template                 corev1.PodTemplateSpec `json:"template"`
	DefaultTimeoutSeconds    int                    `json:"defaultTimeoutSeconds"`
	MaxTimeoutSeconds        int                    `json:"maxTimeoutSeconds"`
	AdditionalAttachSubjects []rbacv1.Subject       `json:"additionalAttachSubjects,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConsoleTemplateList is a list of console templates
type ConsoleTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []ConsoleTemplate `json:"items"`
}
