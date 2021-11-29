package events

import "context"

type Publisher interface {
	Publish(context.Context, interface{}) (string, error)
}

var _ Publisher = &NopPublisher{}

// https://en.wikipedia.org/wiki/NOP_(code)
type NopPublisher struct{}

func (_ NopPublisher) Publish(_ context.Context, _ interface{}) (string, error) { return "nop", nil }

func NewNopPublisher() *NopPublisher {
	return &NopPublisher{}
}
