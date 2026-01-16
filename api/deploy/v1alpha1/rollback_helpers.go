package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *Rollback) IsCompleted() bool {
	succeededCondition := meta.FindStatusCondition(r.Status.Conditions, RollbackConditionSucceded)
	return succeededCondition != nil && succeededCondition.Status != metav1.ConditionUnknown
}
