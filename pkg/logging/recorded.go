package logging

import (
	"fmt"

	kitlog "github.com/go-kit/kit/log"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
)

const (
	// EventTypeDontRecord will tell the logger not to emit a Kubernetes event for this log
	// line
	EventTypeDontRecord = "DontRecord"
)

// WithNoRecord adds a log key that suppresses the recorder
func WithNoRecord(logger kitlog.Logger) kitlog.Logger {
	return kitlog.With(logger, "eventType", EventTypeDontRecord)
}

// WithRecorder decorates a kitlog.Logger so that any log entries that contain an
// appropriate event will also log to the Kubernetes resource using events.
func WithRecorder(logger kitlog.Logger, recorder record.EventRecorder, object runtime.Object) kitlog.Logger {
	return kitlog.LoggerFunc(
		func(keyvals ...interface{}) error {
			if err := logger.Log(keyvals...); err != nil {
				return err
			}

			// Pop key values from our slice into a map so we can better access each element
			kvs := map[string]string{}
			for len(keyvals) > 0 {
				if k, ok := keyvals[0].(string); ok {
					kvs[k] = fmt.Sprintf("%v", keyvals[1])
				}

				keyvals = keyvals[2:]
			}

			var event, eventType, message string
			var ok bool

			if event, ok = kvs["event"]; !ok {
				return nil // no event
			}

			if kvs["eventType"] == EventTypeDontRecord {
				return nil // don't record this event
			}

			if err, hasError := kvs["error"]; hasError {
				eventType = corev1.EventTypeWarning
				message = err
			} else {
				eventType = corev1.EventTypeNormal
				message = kvs["msg"]
			}

			recorder.Event(object, eventType, event, message)

			return nil
		},
	)
}
