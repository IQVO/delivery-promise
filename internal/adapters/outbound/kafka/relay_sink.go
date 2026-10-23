package kafka

import (
	"context"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/outbox"
)

// MessageWriter is the subset of *kafkago.Writer the RelaySink needs, so unit
// tests can substitute a fake.
type MessageWriter interface {
	WriteMessages(ctx context.Context, msgs ...kafkago.Message) error
}

// RelaySink writes already-encoded outbox messages to the topic each names. Its
// writer has NO fixed Topic (kafka-go requires exactly one of Writer.Topic and
// Message.Topic); NewWriter builds it with RequireAll acks.
type RelaySink struct {
	writer MessageWriter
}

// NewRelaySink returns a sink over w.
func NewRelaySink(w MessageWriter) *RelaySink { return &RelaySink{writer: w} }

// Send writes msgs in one WriteMessages call.
func (s *RelaySink) Send(ctx context.Context, msgs ...outbox.Message) error {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]kafkago.Message, len(msgs))
	for i, m := range msgs {
		if m.Topic == "" {
			return fmt.Errorf("kafka relay sink: message %d (%s) has no topic", i, m.EventType)
		}
		headers := make([]kafkago.Header, len(m.Headers))
		for j, h := range m.Headers {
			headers[j] = kafkago.Header{Key: h.Key, Value: []byte(h.Value)}
		}
		out[i] = kafkago.Message{Topic: m.Topic, Key: m.Key, Value: m.Value, Headers: headers}
	}
	if err := s.writer.WriteMessages(ctx, out...); err != nil {
		return fmt.Errorf("kafka relay sink: write %d message(s): %w", len(out), err)
	}
	return nil
}
