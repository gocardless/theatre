package events

import (
	"strings"
	"time"
)

type Kind string

const (
	KindConsole Kind = "Console"
)

type EventKind string

const (
	EventRequest    EventKind = "Request"
	EventAuthorise  EventKind = "Authorise"
	EventStart      EventKind = "Start"
	EventAttach     EventKind = "Attach"
	EventTerminated EventKind = "Terminate"
)

type CommonEvent struct {
	Version     string            `json:"apiVersion"`
	Kind        Kind              `json:"kind"`
	Event       EventKind         `json:"event"`
	ObservedAt  time.Time         `json:"observed_at"`
	Id          string            `json:"id"`
	Annotations map[string]string `json:"annotations"`
}

// EventKind returns the Kind/Event of the Event
func (e CommonEvent) EventKind() string {
	return strings.Join([]string{string(e.Kind), string(e.Event)}, "/")
}

type ConsoleRequestSpec struct {
	Reason   string `json:"reason"`
	Username string `json:"username"`
	// Context is used to denote the cluster name,
	Context                string            `json:"context"`
	Namespace              string            `json:"namespace"`
	ConsoleTemplate        string            `json:"console_template"`
	Console                string            `json:"console"`
	RequiredAuthorisations int               `json:"required_authorisations"`
	Timestamp              time.Time         `json:"timestamp"`
	Labels                 map[string]string `json:"labels"`
}

type ConsoleRequestEvent struct {
	CommonEvent `json:",inline"`
	Spec        ConsoleRequestSpec `json:"spec"`
}

type ConsoleAuthoriseSpec struct {
	Username string `json:"username"`
}

type ConsoleAuthoriseEvent struct {
	CommonEvent `json:",inline"`
	Spec        ConsoleAuthoriseSpec `json:"spec"`
}

type ConsoleStartSpec struct {
	Job string `json:"job"`
}

type ConsoleStartEvent struct {
	CommonEvent `json:",inline"`
	Spec        ConsoleStartSpec `json:"spec"`
}

type ConsoleAttachSpec struct {
	Username  string `json:"username"`
	Pod       string `json:"pod"`
	Container string `json:"container"`
}

type ConsoleAttachEvent struct {
	CommonEvent `json:",inline"`
	Spec        ConsoleAttachSpec `json:"spec"`
}

type ConsoleTerminatedSpec struct {
	TimedOut          bool              `json:"timed_out"`
	ContainerStatuses map[string]string `json:"container_statuses"`
}

type ConsoleTerminatedEvent struct {
	CommonEvent `json:",inline"`
	Spec        ConsoleTerminatedSpec `json:"spec"`
}

// NewConsoleEventID creates a deterministic ID for consoles that can
// be used to correlate events.
func NewConsoleEventID(context, namespace, console string, time time.Time) string {
	return strings.Join([]string{
		// year (2006) month (01) day (02) hour (15) minute (04) second (05)
		time.UTC().Format("20060102150405"),
		context, namespace, console,
	}, "/")
}
