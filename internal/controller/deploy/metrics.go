package deploy

import (
	deployv1alpha1 "github.com/gocardless/theatre/v5/api/deploy/v1alpha1"
	"github.com/prometheus/client_golang/prometheus"
)

var (
	// Common label keys that may appear on rollbacks (copied from releases)
	// These are defined upfront as Prometheus requires label keys at registration time
	rollbackLabelKeys = []string{
		"status",
		"cluster",
		"service",
		"environment",
		"namespace",
		"release",
		"team",
		"initiatedBy",
		"severity",
		"escalation_path",
	}

	rollbackTerminalTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_rollback_terminal_total",
			Help: "Count of rollbacks that reached a terminal state (succeeded or failed)",
		},
		rollbackLabelKeys,
	)
)

// buildRollbackLabels creates a prometheus.Labels map from the rollback's labels
// plus the required namespace and status labels. Only includes labels that are
// defined in rollbackLabelKeys to avoid Prometheus errors.
func buildRollbackLabels(rollback *deployv1alpha1.Rollback, status string) prometheus.Labels {
	labels := prometheus.Labels{
		"status":      status,
		"namespace":   rollback.Namespace,
		"initiatedBy": rollback.Spec.InitiatedBy.Principal,
	}

	// Add labels from the rollback that match our defined label keys
	for k, v := range rollback.Labels {
		// Only include labels that are in our predefined list
		for _, allowedKey := range rollbackLabelKeys {
			if k == allowedKey {
				labels[k] = v
				break
			}
		}
	}

	return labels
}
