package v1alpha1

import (
	"context"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/gocardless/theatre/v3/pkg/workloads/console/events"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/sys/unix"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

// +kubebuilder:object:generate=false
type ConsoleIdBuilder interface {
	BuildId(*Console) string
}

// +kubebuilder:object:generate=false
type LifecycleEventRecorder interface {
	ConsoleRequest(context.Context, *Console, *ConsoleAuthorisationRule) error
	ConsoleAuthorise(context.Context, *Console, string) error
	ConsoleStart(context.Context, *Console, string) error
	ConsoleAttach(context.Context, *Console, string, string) error
	ConsoleTerminate(context.Context, *Console, bool, *corev1.Pod) error
}

var _ LifecycleEventRecorder = &lifecycleEventRecorderImpl{}
var _ ConsoleIdBuilder = &consoleIdBuilderImpl{}

// lifecycleEventRecorderImpl implements the interface to record lifecycle events given
//
// +kubebuilder:object:generate=false
type lifecycleEventRecorderImpl struct {
	// context name for the kubernetes cluster where this recorder runs
	contextName string

	idBuilder ConsoleIdBuilder
	logger    logr.Logger
	publisher events.Publisher
}

// consoleIdBuilderImpl implements the interface to build console IDs from a console object
//
// +kubebuilder:object:generate=false
type consoleIdBuilderImpl struct {
	// context name for the kubernetes cluster where this recorder runs
	contextName string
}

var (
	lifecycleEventsPublish = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_lifecycle_events_published_total",
			Help: "Count of requests handled by the webhook",
		},
		[]string{"event"},
	)
	lifecycleEventsPublishErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "theatre_lifecycle_events_published_errors_total",
			Help: "Count of requests handled by the webhook",
		},
		[]string{"event"},
	)
)

func init() {
	// Register custom metrics with the global controller runtime prometheus registry
	metrics.Registry.MustRegister(lifecycleEventsPublish, lifecycleEventsPublishErrors)
}

func NewConsoleIdBuilder(contextName string) ConsoleIdBuilder {
	return &consoleIdBuilderImpl{
		contextName: contextName,
	}
}

func NewLifecycleEventRecorder(contextName string, logger logr.Logger, publisher events.Publisher, idBuilder ConsoleIdBuilder) LifecycleEventRecorder {
	return &lifecycleEventRecorderImpl{
		contextName: contextName,
		logger:      logger,
		publisher:   publisher,
		idBuilder:   idBuilder,
	}
}

func (b *consoleIdBuilderImpl) BuildId(csl *Console) string {
	return events.NewConsoleEventID(b.contextName, csl.Namespace, csl.Name, csl.CreationTimestamp.Time)
}

func (l *lifecycleEventRecorderImpl) makeConsoleCommonEvent(eventKind events.EventKind, csl *Console) events.CommonEvent {
	return events.CommonEvent{
		Version:     "v1alpha1",
		Kind:        events.KindConsole,
		Event:       eventKind,
		ObservedAt:  time.Now().UTC(),
		Id:          l.idBuilder.BuildId(csl),
		Annotations: map[string]string{},
	}
}

func (l *lifecycleEventRecorderImpl) ConsoleRequest(ctx context.Context, csl *Console, authRule *ConsoleAuthorisationRule) error {
	authCount := 0
	if authRule != nil {
		authCount = authRule.AuthorisationsRequired
	}

	event := &events.ConsoleRequestEvent{
		CommonEvent: l.makeConsoleCommonEvent(events.EventRequest, csl),
		Spec: events.ConsoleRequestSpec{
			Reason:                 csl.Spec.Reason,
			Username:               csl.Spec.User,
			Context:                l.contextName,
			Namespace:              csl.Namespace,
			ConsoleTemplate:        csl.Spec.ConsoleTemplateRef.Name,
			Console:                csl.Name,
			RequiredAuthorisations: authCount,
			Timestamp:              csl.CreationTimestamp.Time,
			Labels:                 csl.Labels,
		},
	}

	id, err := l.publisher.Publish(ctx, event)
	if err != nil {
		lifecycleEventsPublishErrors.WithLabelValues("console_request").Inc()
		return err
	}
	lifecycleEventsPublish.WithLabelValues("console_request").Inc()

	l.logger.Info("event recorded", "id", id, "event", events.EventRequest)
	return nil
}

func (l *lifecycleEventRecorderImpl) ConsoleAuthorise(ctx context.Context, csl *Console, username string) error {
	event := &events.ConsoleAuthoriseEvent{
		CommonEvent: l.makeConsoleCommonEvent(events.EventAuthorise, csl),
		Spec: events.ConsoleAuthoriseSpec{
			Username: username,
		},
	}

	id, err := l.publisher.Publish(ctx, event)
	if err != nil {
		lifecycleEventsPublishErrors.WithLabelValues("console_authorise").Inc()
		return err
	}
	lifecycleEventsPublish.WithLabelValues("console_authorise").Inc()

	l.logger.Info("event recorded", "id", id, "event", events.EventAuthorise)
	return nil
}

func (l *lifecycleEventRecorderImpl) ConsoleStart(ctx context.Context, csl *Console, jobName string) error {
	event := &events.ConsoleStartEvent{
		CommonEvent: l.makeConsoleCommonEvent(events.EventStart, csl),
		Spec: events.ConsoleStartSpec{
			Job: jobName,
		},
	}

	id, err := l.publisher.Publish(ctx, event)
	if err != nil {
		lifecycleEventsPublishErrors.WithLabelValues("console_start").Inc()
		return err
	}
	lifecycleEventsPublish.WithLabelValues("console_start").Inc()

	l.logger.Info("event recorded", "id", id, "event", events.EventStart)
	return nil
}

func (l *lifecycleEventRecorderImpl) ConsoleAttach(ctx context.Context, csl *Console, username string, containerName string) error {
	event := &events.ConsoleAttachEvent{
		CommonEvent: l.makeConsoleCommonEvent(events.EventAttach, csl),
		Spec: events.ConsoleAttachSpec{
			Username:  username,
			Pod:       csl.Status.PodName,
			Container: containerName,
		},
	}

	id, err := l.publisher.Publish(ctx, event)
	if err != nil {
		lifecycleEventsPublishErrors.WithLabelValues("console_attach").Inc()
		return err
	}
	lifecycleEventsPublish.WithLabelValues("console_attach").Inc()

	l.logger.Info("event recorded", "id", id, "event", events.EventAttach)
	return nil
}

func (l *lifecycleEventRecorderImpl) ConsoleTerminate(ctx context.Context, csl *Console, timedOut bool, pod *corev1.Pod) error {
	containerStatuses := make(map[string]string)
	if pod != nil {
		appendStatusMessages(containerStatuses, pod.Status.InitContainerStatuses)
		appendStatusMessages(containerStatuses, pod.Status.ContainerStatuses)
		appendStatusMessages(containerStatuses, pod.Status.EphemeralContainerStatuses)
	}

	event := &events.ConsoleTerminatedEvent{
		CommonEvent: l.makeConsoleCommonEvent(events.EventTerminated, csl),
		Spec: events.ConsoleTerminatedSpec{
			TimedOut:          timedOut,
			ContainerStatuses: containerStatuses,
		},
	}

	id, err := l.publisher.Publish(ctx, event)
	if err != nil {
		lifecycleEventsPublishErrors.WithLabelValues("console_terminate").Inc()
		return err
	}
	lifecycleEventsPublish.WithLabelValues("console_terminate").Inc()

	l.logger.Info("event recorded", "id", id, "event", events.EventTerminated)
	return nil
}

func appendStatusMessages(result map[string]string, containerStatuses []corev1.ContainerStatus) {
	if containerStatuses == nil {
		return
	}

	for _, containerStatus := range containerStatuses {
		if containerStatus.State.Terminated != nil {
			s := containerStatus.State.Terminated
			var message strings.Builder
			message.WriteString(fmt.Sprintf("Terminated with exit code %d", s.ExitCode))
			if s.Reason != "" {
				message.WriteString(fmt.Sprintf(". Reason: %s", s.Reason))
			}
			if s.Signal != 0 {
				message.WriteString(fmt.Sprintf(" (received signal %s)", unix.SignalName(syscall.Signal(s.Signal))))
			}
			if s.Message != "" {
				message.WriteString(fmt.Sprintf(". Message: %s", s.Message))
			}
			result[containerStatus.Name] = message.String()
		} else if containerStatus.State.Waiting != nil {
			s := containerStatus.State.Waiting
			var message strings.Builder
			message.WriteString("Waiting.")
			if s.Reason != "" {
				message.WriteString(fmt.Sprintf(" Reason: %s.", s.Reason))
			}
			if s.Message != "" {
				message.WriteString(fmt.Sprintf(" Message: %s", s.Message))
			}
			result[containerStatus.Name] = message.String()
		}
	}
}
