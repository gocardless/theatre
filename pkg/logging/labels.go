package logging

import (
	kitlog "github.com/go-kit/kit/log"
)

// WithLabels decorates a kitlog.Logger so that any log entries contain all
// labels which keys are prefix by labelKeyPrefix
func WithLabels(logger kitlog.Logger, labels map[string]string, labelKeyPrefix string) kitlog.Logger {
	for key, value := range labels {
		logger = kitlog.With(logger, labelKeyPrefix+key, value)
	}

	return logger
}
