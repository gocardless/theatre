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
	r.Status.History = []HistoryEntry{}

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

func (r *Release) AnnotatedWithActivate() bool {
	_, ok := r.Annotations[AnnotationKeyReleaseActivate]
	return ok
}

func (r *Release) AnnotatedWithSetDeploymentStartTime() bool {
	_, ok := r.Annotations[AnnotationKeyReleaseSetDeploymentStartTime]
	return ok
}

func (r *Release) AnnotatedWithSetDeploymentEndTime() bool {
	_, ok := r.Annotations[AnnotationKeyReleaseSetDeploymentEndTime]
	return ok
}

func (r *Release) SetDeploymentStartTime(timestamp metav1.Time) {
	r.Status.DeploymentStartTime = timestamp
}

func (r *Release) SetDeploymentEndTime(timestamp metav1.Time) {
	r.Status.DeploymentEndTime = timestamp
}

func (r *Release) Activate(message string, previousRelease *Release) {
	// Push to the history if this isn't the first activation
	if r.Status.NextRelease.ReleaseRef != "" {
		r.PushHistoryEntry()
	}

	r.Status.Message = message
	if previousRelease != nil {
		r.Status.PreviousRelease = ReleaseTransition{
			ReleaseRef:     previousRelease.Name,
			TransitionTime: metav1.Now(),
		}
	}

	// This is the current active release, so it has no next release
	r.Status.NextRelease = ReleaseTransition{}

	meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{
		Type:               ReleaseConditionActive,
		Status:             metav1.ConditionTrue,
		Reason:             ReasonDeployed,
		Message:            message,
		ObservedGeneration: r.ObjectMeta.Generation,
	})
}

func (r *Release) Deactivate(message string, nextRelease Release) {
	r.PushHistoryEntry()
	r.Status.Message = message
	r.Status.NextRelease = ReleaseTransition{
		ReleaseRef:     nextRelease.Name,
		TransitionTime: metav1.Now(),
	}
	meta.SetStatusCondition(&r.Status.Conditions, metav1.Condition{
		Type:               ReleaseConditionActive,
		Status:             metav1.ConditionFalse,
		Reason:             ReasonSuperseded,
		Message:            message,
		ObservedGeneration: r.ObjectMeta.Generation,
	})
}

func (r *Release) PushHistoryEntry() {
	he := HistoryEntry{
		Timestamp: metav1.Now(),
	}

	if r.Status.Message != "" {
		he.Message = r.Status.Message
	}

	if !r.Status.DeploymentStartTime.IsZero() {
		he.DeploymentStartTime = r.Status.DeploymentStartTime
	}

	if !r.Status.DeploymentEndTime.IsZero() {
		he.DeploymentEndTime = r.Status.DeploymentEndTime
	}

	if r.Status.PreviousRelease.ReleaseRef != "" {
		he.PreviousRelease = r.Status.PreviousRelease
	}

	if r.Status.NextRelease.ReleaseRef != "" {
		he.NextRelease = r.Status.NextRelease
	}

	r.Status.History = append(r.Status.History, he)
}
