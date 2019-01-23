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
	Status ConsoleStatus `json:"status,omitempty" protobuf:"bytes,3,opt,name=status"`
}

// ConsoleSpec defines the specification for a console
type ConsoleSpec struct {
	User               string                      `json:"user"`
	Reason             string                      `json:"reason"`
	TimeoutSeconds     int                         `json:"timeoutSeconds"`
	ConsoleTemplateRef corev1.LocalObjectReference `json:"consoleTemplateRef"`
}

type ConsoleStatus struct {
	PodName    string       `json:"podName" protobuf:"bytes,1,opt,name=podName"`
	ExpiryTime *metav1.Time `json:"expiryTime,omitempty" protobuf:"bytes,2,opt,name=expiryTime"`
	Phase      string       `json:"phase" protobuf:"bytes,3,opt,name=phase"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConsoleList is a list of consoles
type ConsoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []Console `json:"items"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ConsoleTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ConsoleTemplateSpec `json:"spec"`
}

type ConsoleTemplateSpec struct {
	User                     string                 `json:"user"`
	Template                 corev1.PodTemplateSpec `json:"template"`
	DefaultTimeoutSeconds    int                    `json:"defaultTimeoutSeconds"`
	MaxTimeoutSeconds        int                    `json:"maxTimeoutSeconds"`
	AdditionalAttachSubjects []rbacv1.Subject       `json:"additionalAttachSubjects"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

type ConsoleTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []ConsoleTemplate `json:"items"`
}
