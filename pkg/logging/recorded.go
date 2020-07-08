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

var _ logr.Logger = &EventRecordingLogger{}

type EventRecordingLogger struct {
	logr.Logger
	recorder record.EventRecorder
	object   runtime.Object
}

// WithRecorder decorates a kitlog.Logger so that any log entries that contain an
// appropriate event will also log to the Kubernetes resource using events.
func (erl *EventRecordingLogger) Info(msg string, keyvals ...interface{}) {
	erl.Info(msg, keyvals...)

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

// // RecorderAwareCaller returns the file and line where the Log method was
// // invoked, adjusting for the fact that this may have been hijacked by the
// // event recorder.
// func RecorderAwareCaller() kitlog.Valuer {
// 	return func() interface{} {
// 		skipSuffixes := []string{
// 			// As logger.Log is called within recorded.go, these frames must be
// 			// skipped over also.
// 			"github.com/gocardless/theatre/pkg/logging/recorded.go",
// 		}

// 		// Start at stack frame depth 3, as per the kitlog default.
// 		depth := 3

// 		for {
// 			_, file, line, ok := goruntime.Caller(depth)

// 			// We should never hit the bottom of the stack, but *if* we do then return
// 			// something.
// 			if !ok {
// 				return "error:0"
// 			}

// 			// If file matches *any* of the files to skip, then we have *not* the
// 			// caller that we want
// 			skipThisFrame := false
// 			for _, suffix := range skipSuffixes {
// 				if strings.HasSuffix(file, suffix) {
// 					skipThisFrame = true
// 				}
// 			}

// 			if skipThisFrame {
// 				depth++
// 				continue
// 			}

// 			// Found a good frame, so format it in the same way that kitlog.Caller
// 			// does.
// 			idx := strings.LastIndexByte(file, '/')
// 			return file[idx+1:] + ":" + strconv.Itoa(line)
// 		}
// 	}
// }
