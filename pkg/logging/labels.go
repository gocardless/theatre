package logging

import (
	"fmt"
	"strings"

	"github.com/go-logr/logr"
)

// WithLabels decorates a logr.Logger so that any log entries contain all
// labels which keys are prefix by labelKeyPrefix
func WithLabels(logger logr.Logger, labels map[string]string, labelKeyPrefix string) logr.Logger {
	for key, value := range labels {
		logger = logger.WithValues(
			fmt.Sprintf(
				"%s%s",
				labelKeyPrefix,
				linearLabelKey(key),
			),
			value,
		)
	}

	return logger
}

// linearLabelKey reduces a given label key to a linear form such that
// an incoming label key will have underscores in place of periods and
// forward slashes. This does have the potential to have multiple keys
// map down onto a single key. This is not likely to occur, and is
// likely avoidable for most installations.
//
// This behaviour matches the behaviour seen in some other systems
// such as Prometheus where labels are reduced to a alphanumeric
// string with underscore spacers.
//
// e.g. app.kubernetes.io/instance would become
// app_kubernetes_io_instance
func linearLabelKey(labelKey string) string {
	labelKey = strings.ReplaceAll(labelKey, "/", "_")
	labelKey = strings.ReplaceAll(labelKey, ".", "_")

	return labelKey
}
