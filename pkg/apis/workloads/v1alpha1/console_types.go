package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Console declares an instance of a console environment to be created by a specific user
// +kubebuilder:printcolumn:name="User",type="string",JSONPath=".spec.user"
// +kubebuilder:printcolumn:name="Phase",type="string",JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +kubebuilder:printcolumn:name="Expiry",type="string",JSONPath=".status.expiryTime"
type Console struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConsoleSpec   `json:"spec"`
	Status ConsoleStatus `json:"status,omitempty"`
}

// ConsoleSpec defines the specification for a console
type ConsoleSpec struct {
	User   string `json:"user"`
	Reason string `json:"reason"`

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

	// Specifies the TTL for this Console. The Console will be eligible for garbage
	// collection ConsoleTTLSecondsAfterFinished seconds after it enters the Stopped phase.
	// This field is modeled on the TTL mechanism in Kubernetes 1.12.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=604800
	// +optional
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// The command and arguments to execute. If not specified the command from
	// the template specification will be used.
	Command []string `json:"command,omitempty"`
}

// ConsoleStatus defines the status of a created console, populated at runtime
type ConsoleStatus struct {
	PodName    string       `json:"podName"`
	ExpiryTime *metav1.Time `json:"expiryTime,omitempty"`
	// Time at which the job completed successfully
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
	Phase          ConsolePhase `json:"phase"`
}

type ConsolePhase string

// These are valid phases for a console
const (
	// ConsolePending means the console has been created but its pod is not yet ready
	ConsolePending ConsolePhase = "Pending"
	// ConsoleRunning means the pod has started and is running
	ConsoleRunning ConsolePhase = "Running"
	// ConsoleStopped means the console has completed or timed out
	ConsoleStopped ConsolePhase = "Stopped"
	// ConsoleDestroyed means the consoles job has been deleted
	ConsoleDestroyed ConsolePhase = "Destroyed"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConsoleList is a list of consoles
type ConsoleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []Console `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConsoleTemplate declares a console template that can be instantiated through a Console object
type ConsoleTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ConsoleTemplateSpec `json:"spec"`
}

// ConsoleTemplateSpec defines the parameters that a created console will adhere to
type ConsoleTemplateSpec struct {
	Template corev1.PodTemplateSpec `json:"template"`

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

	// Specifies the TTL for any Console created with this template. If set, the Console
	// will be eligible for garbage collection ConsoleTTLSecondsAfterFinished seconds after
	// it enters the Stopped phase. If not set, this value defaults to 24 hours.
	// This field is modeled closely on the TTL mechanism in Kubernetes 1.12.
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

// ConsoleAuthorisationRule declares rules specifying what commands need to be authorised and by whom.
type ConsoleAuthorisationRule struct {
	// Human readable name of authorisation rule added to logs for auditing.
	Name string `json:"name"`

	// The match expression to compare to the command and arguments of the console.
	// Uses the glob format https://github.com/gobwas/glob.
	MatchCommand string `json:"matchCommand"`

	ConsoleAuthorisers `json:",inline"`
}

// ConsoleAuthorisers declares the subjects required to perform authorisations.
type ConsoleAuthorisers struct {
	// The number of authorisations required from members of the subjects before the console can run.
	AuthorisationsRequired int `json:"authorisationsRequired"`

	// List of subjects that can provide authorisation for the console command to run.
	Subjects []rbacv1.Subject `json:"subjects"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConsoleTemplateList is a list of console templates
type ConsoleTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []ConsoleTemplate `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConsoleAuthorisation declares a console authorisation that is instantiated when a console that requires authoristion is created. It is used to store authorisations for the associated console.
type ConsoleAuthorisation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ConsoleAuthorisationSpec `json:"spec"`
}

// ConsoleAuthorisationSpec defines the specification for a console authorisation
type ConsoleAuthorisationSpec struct {
	// The reference to the console by name that this console authorisation belongs to.
	ConsoleRef corev1.LocalObjectReference `json:"consoleRef"`

	// The user who created the referenced console.
	Owner string `json:"owner"`

	// List of authorisations that have been given to the referenced console.
	Authorisations []rbacv1.Subject `json:"authorisations"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// ConsoleAuthorisationList is a list of console authorisations
type ConsoleAuthorisationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []ConsoleAuthorisation `json:"items"`
}
