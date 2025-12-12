package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *Release) IsStatusInitialised() bool {
	return r.Status.ObservedGeneration > 0
}

func (r *Release) InitialiseStatus(message string) {
	r.Status.Message = message
	r.Status.ObservedGeneration = r.ObjectMeta.Generation

	conditionActive := metav1.Condition{
		Type:               ReleaseConditionActive,
		Status:             metav1.ConditionFalse,
		Reason:             ReasonCreated,
		Message:            message,
		ObservedGeneration: r.ObjectMeta.Generation,
	}

	meta.SetStatusCondition(&r.Status.Conditions, conditionActive)

	conditionHealthy := metav1.Condition{
		Type:               ReleaseConditionHealthy,
		Status:             metav1.ConditionUnknown,
		Reason:             ReasonCreated,
		Message:            message,
		ObservedGeneration: r.ObjectMeta.Generation,
	}

	meta.SetStatusCondition(&r.Status.Conditions, conditionHealthy)
}

func (r *Release) UpdateObservedGeneration() {
	r.Status.ObservedGeneration = r.ObjectMeta.Generation
}
