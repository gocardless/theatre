package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// RollbackPolicySpec defines the desired state of RollbackPolicy
type RollbackPolicySpec struct {
	// TargetName identifies which releases this policy applies to,
	// matching Release.config.targetName.
	// +kubebuilder:validation:Required
	TargetName string `json:"targetName"`

	// Trigger defines the Release condition that triggers a rollback.
	// +optional
	Trigger RollbackTrigger `json:"trigger,omitempty"`

	// Automated configures automated rollback behavior.
	// +optional
	Automated AutomatedRollbackPolicy `json:"automated,omitempty"`
}

// RollbackTrigger defines the Release condition that triggers a rollback
type RollbackTrigger struct {
	// ConditionType is the Release status condition type to watch.
	// +kubebuilder:default="Healthy"
	// +kubebuilder:validation:Optional
	ConditionType string `json:"conditionType,omitempty"`

	// ConditionStatus is the status value that triggers a rollback.
	// +kubebuilder:default="False"
	// +kubebuilder:validation:Enum=True;False
	// +optional
	ConditionStatus *metav1.ConditionStatus `json:"conditionStatus,omitempty"`
}

// AutomatedRollbackPolicy configures automated rollback behavior
type AutomatedRollbackPolicy struct {
	// Enabled controls whether automated rollbacks are active.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// Limit is the maximum number of consecutive automated
	// rollbacks before automation is disabled. 0 means unlimited. Default is 3.
	// +kubebuilder:default=3
	Limit *int32 `json:"limit,omitempty"`

	// MinInterval is the minimum time to wait between automated rollbacks. Default is 5m.
	MinInterval *metav1.Duration `json:"minInterval,omitempty"`

	// BreakerResetWindow is the "cooldown" period. If this duration passes
	// since the first rollback, the status.consecutiveRollbackCount is reset to 0.
	// Default is 1h.
	BreakerWindow *metav1.Duration `json:"breakerWindow,omitempty"`

	// ResetOnRecovery re-enables automation and resets the consecutive
	// rollback counter when the trigger condition returns to normal (e.g.
	// "True" if spec.trigger.conditionStatus is "False" and vice versa).
	// +kubebuilder:default=false
	ResetOnRecovery bool `json:"resetOnRecovery,omitempty"`
}

// RollbackPolicyStatus defines the observed state of RollbackPolicy
type RollbackPolicyStatus struct {
	// ConsecutiveCount tracks how many automated rollbacks have
	// been performed since the last recovery.
	// +optional
	ConsecutiveCount int32 `json:"consecutiveCount,omitempty"`

	// LastAutomatedRollbackTime is when the last automated rollback was created.
	// +optional
	LastAutomatedRollbackTime *metav1.Time `json:"lastAutomatedRollbackTime,omitempty"`

	// WindowStartTime is the start time of the consecutive rollback window.
	// +optional
	WindowStartTime *metav1.Time `json:"windowStartTime,omitempty"`

	// Conditions represent the latest observations of the policy's state.
	// +optional
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

// RollbackPolicy is the Schema for the rollbackpolicies API.
type RollbackPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// Metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the desired state of RollbackPolicy
	// +required
	Spec RollbackPolicySpec `json:"spec"`

	// Status defines the observed state of RollbackPolicy
	// +optional
	Status RollbackPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// RollbackPolicyList contains a list of RollbackPolicy
type RollbackPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []RollbackPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&RollbackPolicy{}, &RollbackPolicyList{})
}
