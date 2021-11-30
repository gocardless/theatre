package events

import (
	"context"
	"encoding/json"
	"fmt"

	"cloud.google.com/go/pubsub"
)

type ErrorPubsubFailedConnect struct{ err error }

func (e ErrorPubsubFailedConnect) Unwrap() error { return e.err }
func (e ErrorPubsubFailedConnect) Error() string {
	return fmt.Sprintf(
		"failed to connect to pubsub topic: %s",
		e.Error(),
	)
}

// Test we implement the error interface
var _ error = &ErrorPubsubFailedConnect{}

// ErrorPubsubFailedPublish provides context on the reasons that we failed to publish our message
type ErrorPubsubFailedPublish struct {
	err     error
	Topic   string
	Message interface{}
}

func (e ErrorPubsubFailedPublish) Unwrap() error { return e.err }
func (e ErrorPubsubFailedPublish) Error() string {
	return fmt.Sprintf(
		"failed to publish message '%v' to topic '%s': %s",
		e.Message,
		e.Topic,
		e.err,
	)
}

// Test we implement the error interface
var _ error = &ErrorPubsubFailedPublish{}

// GooglePubSubPublisher implements the publisher.Publisher interface,
// allowing us to publish messages to a Google Pub/Sub topic
type GooglePubSubPublisher struct {
	client *pubsub.Client
	topic  *pubsub.Topic
}

// Test we implement the Publisher interface
var _ Publisher = &GooglePubSubPublisher{}

func NewGooglePubSubPublisher(ctx context.Context, projectName string, topicName string) (*GooglePubSubPublisher, error) {
	client, err := pubsub.NewClient(ctx, projectName)
	if err != nil {
		return nil, ErrorPubsubFailedConnect{err: err}
	}

	return &GooglePubSubPublisher{
		client: client,
		topic:  client.Topic(topicName),
	}, nil
}

func (p *GooglePubSubPublisher) Stop() {
	p.topic.Stop()
}

func (p *GooglePubSubPublisher) Publish(ctx context.Context, msg interface{}) (string, error) {
	messageBytes, err := json.Marshal(msg)
	if err != nil {
		return "", ErrorPubsubFailedPublish{err: err, Topic: p.topic.ID(), Message: msg}
	}

	result := p.topic.Publish(ctx, &pubsub.Message{Data: messageBytes})
	id, err := result.Get(ctx)
	if err != nil {
		return "", err
	}
	return id, nil
}
