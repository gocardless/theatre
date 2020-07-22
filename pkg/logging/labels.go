package logging

import (
	"github.com/go-logr/logr"
)

// WithLabels decorates a kitlog.Logger so that any log entries contain all
// labels which keys are prefix by labelKeyPrefix
func WithLabels(logger logr.Logger, labels map[string]string, labelKeyPrefix string) logr.Logger {
	for key, value := range labels {
		logger = logger.WithValues(labelKeyPrefix+key, value)
	}

	return logger
}
