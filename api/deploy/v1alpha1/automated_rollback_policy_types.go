package v1alpha1

import (
	apiextv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// AutomatedRollbackPolicySpec defines the desired state of RollbackPolicy
type AutomatedRollbackPolicySpec struct {
	// TargetName identifies which releases this policy applies to,
	// matching Release.config.targetName.
	// +kubebuilder:validation:Required
	TargetName string `json:"targetName"`

	// Trigger defines the Release condition that triggers a rollback.
	// +optional
	Trigger RollbackTrigger `json:"trigger,omitempty"`

	// Enabled controls whether automated rollbacks are active.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// MaxConsecutiveRollbacks is the maximum number of consecutive automated
	// rollbacks before automation is disabled. If left empty, the limit is
	// unlimited.
	// +kubebuilder:validation:Optional
	MaxConsecutiveRollbacks *int32 `json:"maxConsecutiveRollbacks,omitempty"`

	// MinInterval is the minimum time to wait between automated rollbacks.
	// +kubebuilder:validation:Optional
	MinInterval *metav1.Duration `json:"minInterval,omitempty"`

	// ResetPeriod is the "cooldown" period. If this duration passes
	// since the first rollback, the status.consecutiveRollbackCount is reset to 0.
	// +kubebuilder:validation:Optional
	ResetPeriod *metav1.Duration `json:"resetPeriod,omitempty"`

	// ResetOnRecovery re-enables automation and resets the consecutive
	// rollback counter when the trigger condition returns to normal for a
	// following release (e.g. "True" if spec.trigger.conditionStatus is
	// "False" and vice versa).
	// +kubebuilder:validation:Optional
	ResetOnRecovery bool `json:"resetOnRecovery,omitempty"`

	// DeploymentOptions contains additional rollback provider-specific options.
	// +kubebuilder:validation:Optional
	DeploymentOptions map[string]apiextv1.JSON `json:"deploymentOptions,omitempty"`
}

// RollbackTrigger defines the Release condition that triggers a rollback
type RollbackTrigger struct {
	// ConditionType is the Release status condition type to watch.
	// +kubebuilder:default="RollbackRequired"
	// +kubebuilder:validation:Optional
	ConditionType string `json:"conditionType,omitempty"`

	// ConditionStatus is the status value that triggers a rollback.
	// +kubebuilder:default="True"
	// +kubebuilder:validation:Enum=True;False
	// +optional
	ConditionStatus metav1.ConditionStatus `json:"conditionStatus,omitempty"`
}

// RollbackPolicyStatus defines the observed state of RollbackPolicy
type RollbackPolicyStatus struct {
	// ConsecutiveCount tracks how many automated rollbacks have
	// been performed since the last recovery.
	ConsecutiveCount int32 `json:"consecutiveCount,omitempty"`

	// LastAutomatedRollbackTime is when the last automated rollback was created.
	LastAutomatedRollbackTime *metav1.Time `json:"lastAutomatedRollbackTime,omitempty"`

	// WindowStartTime is the start time of the consecutive rollback window.
	WindowStartTime *metav1.Time `json:"windowStartTime,omitempty"`

	// Conditions represent the latest observations of the policy's state.
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rbp
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetName`
// +kubebuilder:printcolumn:name="TriggerCondition",type=string,JSONPath=`.spec.trigger.conditionType`
// +kubebuilder:printcolumn:name="TriggerWhen",type=string,JSONPath=`.spec.trigger.conditionStatus`
// +kubebuilder:printcolumn:name="Automated",type=boolean,JSONPath=`.spec.automated.enabled`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AutomatedRollbackPolicy is the Schema for the automatedrollbackpolicies API.
type AutomatedRollbackPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of RollbackPolicy
	// +required
	Spec AutomatedRollbackPolicySpec `json:"spec"`

	// Status defines the observed state of RollbackPolicy
	// +optional
	Status RollbackPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RollbackPolicyList contains a list of RollbackPolicy
type AutomatedRollbackPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AutomatedRollbackPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&AutomatedRollbackPolicy{}, &AutomatedRollbackPolicyList{})
}
