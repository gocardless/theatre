package status

import (
	workloadsv1alpha1 "github.com/gocardless/theatre/v3/apis/workloads/v1alpha1"
)

// Result is the basis for updating the status of a Console with termination
// events
type Result struct {
	Phase     *workloadsv1alpha1.ConsolePhase
	Condition workloadsv1alpha1.ConsoleConditionType
	Reason    workloadsv1alpha1.ConsoleConditionReason
}
