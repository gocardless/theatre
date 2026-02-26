package deploy

import "github.com/prometheus/client_golang/prometheus"

var (
	rollbackLabels        = []string{"namespace", "status"}
	rollbackTerminalTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_rollback_terminal_total",
			Help: "Count of rollbacks that reached a terminal state (succeeded or failed)",
		},
		rollbackLabels,
	)
)
