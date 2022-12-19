package logging

import (
	"fmt"

	"github.com/go-logr/logr"
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
func WithNoRecord(logger logr.Logger) logr.Logger {
	return logger.WithValues("eventType", EventTypeDontRecord)
}

var _ logr.LogSink = &EventRecordingLogSink{}

type EventRecordingLogSink struct {
	logger   logr.LogSink
	info     logr.RuntimeInfo
	recorder record.EventRecorder
	object   runtime.Object
	values   []interface{}
}

// Init implements logr.LogSink.
func (erl *EventRecordingLogSink) Init(info logr.RuntimeInfo) {
	erl.info = info
}

// Enabled tests whether this Logger is enabled.  For example, commandline
// flags might be used to set the logging verbosity and disable some info
// logs.
// Enabled implements logr.LogSink.
func (erl *EventRecordingLogSink) Enabled(level int) bool {
	return erl.logger.Enabled(level)
}

// WithName implements logr.LogSink.
func (erl *EventRecordingLogSink) WithName(name string) logr.LogSink {
	return &EventRecordingLogSink{
		logger:   erl.logger.WithName(name),
		recorder: erl.recorder,
		object:   erl.object,
		values:   erl.values,
	}
}

// WithValues implements logr.LogSink.
func (erl *EventRecordingLogSink) WithValues(values ...interface{}) logr.LogSink {
	return &EventRecordingLogSink{
		logger:   erl.logger.WithValues(values...),
		recorder: erl.recorder,
		object:   erl.object,
		values:   append(erl.values, values...),
	}
}

func WithEventRecorder(logger logr.LogSink, recorder record.EventRecorder, object runtime.Object) logr.Logger {
	return logr.New(&EventRecordingLogSink{
		logger:   logger,
		recorder: recorder,
		object:   object,
		values:   []interface{}{},
	})
}

// Info implements logr.LogSink.
func (erl *EventRecordingLogSink) Info(levels int, msg string, keyvals ...interface{}) {
	erl.logger.Info(levels, msg, keyvals...)

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
		return // no event
	}

	if kvs["eventType"] == EventTypeDontRecord {
		return // don't record this event
	}

	if err, hasError := kvs["error"]; hasError {
		eventType = corev1.EventTypeWarning
		message = err
	} else {
		eventType = corev1.EventTypeNormal
		message = msg
	}

	erl.recorder.Event(erl.object, eventType, event, message)
}

// Error logs an error, with the given message and key/value pairs as context.
// It functions similarly to calling Info with the "error" named value, but may
// have unique behavior, and should be preferred for logging errors (see the
// package documentations for more information).
//
// The msg field should be used to add context to any underlying error,
// while the err field should be used to attach the actual error that
// triggered this log line, if present.
//
// Error implements logr.LogSink.
func (erl *EventRecordingLogSink) Error(err error, msg string, keysAndValues ...interface{}) {
	erl.logger.Error(err, msg, keysAndValues...)
}
