package v1alpha1

import (
	"context"
	"time"

	"github.com/go-logr/logr"
	"github.com/gocardless/theatre/v3/pkg/workloads/console/events"
)

func CommonEventFromConsole(ctx string, eventKind events.EventKind, csl *Console) events.CommonEvent {
	return events.CommonEvent{
		Version:    "v1alpha1",
		Kind:       events.KindConsole,
		Event:      eventKind,
		ObservedAt: time.Now().UTC(),
		Id: events.NewConsoleEventID(
			ctx, csl.Namespace, csl.Name,
			csl.CreationTimestamp.Time,
		),
		Annotations: map[string]string{},
	}
}

// +kubebuilder:object:generate=false
type LifecycleEventRecorder interface {
	ConsoleRequest(context.Context, *Console) error
	ConsoleAuthorise(context.Context, *Console, string) error
	ConsoleStart(context.Context, *Console, string) error
	ConsoleAttach(context.Context, *Console, string, string) error
	ConsoleTerminate(context.Context, *Console, bool) error
}

var _ LifecycleEventRecorder = &lifecycleEventRecorderImpl{}

// lifecycleEventRecorderImpl implements the interface to record lifecycle events given
//
// +kubebuilder:object:generate=false
type lifecycleEventRecorderImpl struct {
	// context name for the kubernetes cluster where this recorder runs
	contextName string

	logger    logr.Logger
	publisher events.Publisher
}

func NewLifecycleEventRecorder(contextName string, logger logr.Logger, publisher events.Publisher) LifecycleEventRecorder {
	return &lifecycleEventRecorderImpl{
		contextName: contextName,
		logger:      logger,
		publisher:   publisher,
	}
}

func (l *lifecycleEventRecorderImpl) ConsoleRequest(ctx context.Context, csl *Console) error {
	event := &events.ConsoleRequestEvent{
		CommonEvent: CommonEventFromConsole(l.contextName, events.EventRequest, csl),
		Spec: events.ConsoleRequestSpec{
			Reason:          csl.Spec.Reason,
			Username:        csl.Spec.User,
			Context:         l.contextName,
			Namespace:       csl.Namespace,
			ConsoleTemplate: csl.Spec.ConsoleTemplateRef.Name,
			Console:         csl.Name,
			Timestamp:       csl.CreationTimestamp.Time,
			Labels:          csl.Labels,
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
		CommonEvent: CommonEventFromConsole(l.contextName, events.EventAuthorise, csl),
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
		CommonEvent: CommonEventFromConsole(l.contextName, events.EventStart, csl),
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
		CommonEvent: CommonEventFromConsole(l.contextName, events.EventAttach, csl),
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
		CommonEvent: CommonEventFromConsole(l.contextName, events.EventTerminated, csl),
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
