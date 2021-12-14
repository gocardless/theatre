package v1alpha1

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"github.com/gocardless/theatre/v3/pkg/workloads/console/events"
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
	ConsoleTerminate(context.Context, *Console, bool) error
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
		return err
	}

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
		return err
	}

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
		return err
	}

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
		return err
	}

	l.logger.Info("event recorded", "id", id, "event", events.EventAttach)
	return nil
}

func (l *lifecycleEventRecorderImpl) ConsoleTerminate(ctx context.Context, csl *Console, timedOut bool) error {
	event := &events.ConsoleTerminatedEvent{
		CommonEvent: l.makeConsoleCommonEvent(events.EventTerminated, csl),
		Spec: events.ConsoleTerminatedSpec{
			TimedOut: timedOut,
		},
	}

	id, err := l.publisher.Publish(ctx, event)
	if err != nil {
		return err
	}

	l.logger.Info("event recorded", "id", id, "event", events.EventTerminated)
	return nil
}
