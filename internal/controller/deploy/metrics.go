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

	rollbackCompletionDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "theatre_rollback_completion_duration_seconds",
			Help:    "Time from rollback creation to rollback completion in seconds",
			Buckets: prometheus.DefBuckets,
		},
		rollbackLabelKeys,
	)

	rollbackRetryCount = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "theatre_rollback_retry_count",
			Help:    "Number of retries performed by rollbacks before reaching terminal state",
			Buckets: []float64{0, 1, 2, 3, 4, 5},
		},
		rollbackLabelKeys,
	)
)

// buildRollbackLabels creates a prometheus.Labels map from the rollback's labels
// plus the required namespace and status labels. All label keys from rollbackLabelKeys
// must be present to satisfy Prometheus cardinality requirements.
func buildRollbackLabels(rollback *deployv1alpha1.Rollback, status string) prometheus.Labels {
	labels := prometheus.Labels{
		"status":      status,
		"namespace":   rollback.Namespace,
		"initiatedBy": rollback.Spec.InitiatedBy.Principal,
	}

	// Initialize all label keys with empty strings to ensure consistent cardinality
	for _, key := range rollbackLabelKeys {
		if _, exists := labels[key]; !exists {
			labels[key] = ""
		}
	}

	// Override with actual rollback labels if present
	for k, v := range rollback.Labels {
		if _, exists := labels[k]; exists {
			labels[k] = v
		}
	}

	return labels
}
