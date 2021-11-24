// package events
package main

import (
	"strings"
	"time"
)

type CommonEvent struct {
	Version    string    `json:"apiVersion"`
	Kind       string    `json:"kind"`
	Event      string    `json:"event"`
	ObservedAt time.Time `json:"observed_at"`
	Id         string    `json:"id"`
}

func (e CommonEvent) EventKind() string {
	return strings.Join([]string{e.Kind, e.Event}, "/")
}

type ConsoleRequestSpec struct {
	Reason   string `json:"reason"`
	Username string `json:"username"`
	// Context is used to denote the cluster name,
	Context         string    `json:"context"`
	Namespace       string    `json:"namespace"`
	ConsoleTemplate string    `json:"console_template"`
	Console         string    `json:"console"`
	Timestamp       time.Time `json:"timestamp"`
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
	TimedOut bool   `json:"timed_out"`
	ExitCode uint16 `json:"exit_code"`
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
		time.Format("20060102150405"),
		context, namespace, console,
	}, "-")
}
