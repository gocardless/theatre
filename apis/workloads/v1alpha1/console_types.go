package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ConsoleSpec defines the desired state of Console
type ConsoleSpec struct {
	User       string `json:"user"`
	Reason     string `json:"reason"`
	IncidentId string `json:"incident_id"`

	// Number of seconds that the console should run for.
	// If the process running within the console has not exited before this
	// timeout is reached, then the console will be terminated.
	// If this value exceeds the Maximum Timeout Seconds specified in the
	// ConsoleTemplate that this console refers to, then this timeout will be
	// clamped to that value.
	// Maximum value of 1 week (as per ConsoleTemplate.Spec.MaxTimeoutSeconds).
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=604800
	TimeoutSeconds int `json:"timeoutSeconds,omitempty"`

	ConsoleTemplateRef corev1.LocalObjectReference `json:"consoleTemplateRef"`

	// Specifies the TTL before running for this Console. The Console will be
	// eligible for garbage collection TTLSecondsBeforeRunning seconds if it has
	// not progressed to the Running phase. This field is modeled on the TTL
	// mechanism in Kubernetes 1.12.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=86400
	TTLSecondsBeforeRunning *int32 `json:"ttlSecondsBeforeRunning,omitempty"`

	// Specifies the TTL for this Console. The Console will be eligible for
	// garbage collection TTLSecondsAfterFinished seconds after it enters the
	// Stopped or Destroyed phase. This field is modeled on the TTL mechanism in
	// Kubernetes 1.12.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=604800
	// +optional
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// The command and arguments to execute. If not specified the command from
	// the template specification will be used.
	Command []string `json:"command,omitempty"`

	// Disable TTY and STDIN on the underlying container. This should usually
	// be set to false so clients can attach interactively; however, in certain
	// situations, enabling the TTY on a container in the console causes
	// breakage - in Tekton steps, for example.
	Noninteractive bool `json:"noninteractive,omitempty"`
}

// ConsoleStatus defines the observed state of Console
type ConsoleStatus struct {
	PodName    string       `json:"podName"`
	ExpiryTime *metav1.Time `json:"expiryTime,omitempty"`
	// Time at which the job completed successfully
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
	Phase          ConsolePhase `json:"phase"`
}

// +kubebuilder:object:root=true
// +kubebuilder:storageversion

// Console declares an instance of a console environment to be created by a specific user
// +kubebuilder:printcolumn:name="User",type="string",JSONPath=".spec.user"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Expiry",type="string",JSONPath=".status.expiryTime"
type Console struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConsoleSpec   `json:"spec,omitempty"`
	Status ConsoleStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConsoleList contains a list of Console
type ConsoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Console `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Console{}, &ConsoleList{})
}
