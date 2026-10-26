package kafka

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	ce "github.com/cloudevents/sdk-go/v2/event"
	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/delivery-promise/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// errBadPayload marks a CloudEvent whose `data` cannot be decoded or lacks a
// field the use case needs: a deterministic problem, never retried.
var errBadPayload = errors.New("malformed event payload")

// errPoison marks a message the consumer can never apply: the run loop
// dead-letters it at once, without retrying, and commits past it.
var errPoison = errors.New("poison message")

// HandlerFunc applies one decoded CloudEvent. nil means done (or deterministic
// and skippable); a non-nil error is transient unless it wraps errBadPayload.
type HandlerFunc func(ctx context.Context, m usecases.Meta, e ce.Event) error

// Consumer reads the producers' integration topics under one stable consumer
// group and dispatches each message on the FULL CloudEvents `type` to its
// handler. Types it has no handler for are ignored.
type Consumer struct {
	Name     string
	Topics   []string
	Reader   Reader
	Handlers map[string]HandlerFunc
	Logger   *slog.Logger
	Retry    RetryPolicy

	// DLQ is the dead-letter writer: a transient failure is retried
	// domainMaxHandlerAttempts times before the message is published to
	// "<topic>.dlq" instead of blocking the partition forever; a payload that
	// can never be applied goes there at once.
	DLQ DeadLetterWriter

	sleep sleepFunc // test hook; nil => real, ctx-cancellable sleep
}

// NewConsumer builds a Consumer reading topics from brokers under groupID (from
// an environment variable at the composition root, never a literal). Building
// the reader and the writer does not dial.
func NewConsumer(name string, brokers, topics []string, groupID string, handlers map[string]HandlerFunc, logger *slog.Logger) *Consumer {
	return &Consumer{
		Name:     name,
		Topics:   topics,
		Reader:   kafkago.NewReader(readerConfig(brokers, topics, groupID)),
		Handlers: handlers,
		Logger:   defaultLogger(logger),
		DLQ:      newDomainDLQWriter(brokers),
	}
}

// Run consumes until ctx is cancelled or the reader fails. A message's offset is
// committed only after HandleMessage returned nil; a transient failure retries
// the SAME message with capped exponential backoff, up to
// domainMaxHandlerAttempts, then dead-letters it.
func (c *Consumer) Run(ctx context.Context) error {
	loop := consumeLoop{
		reader: c.Reader,
		handle: c.handleTraced,
		logger: defaultLogger(c.Logger),
		name:   c.Name,
		retry:  c.Retry,
		sleep:  c.sleep,
	}
	if c.DLQ != nil {
		loop.maxAttempts = domainMaxHandlerAttempts
		loop.deadLetter = func(ctx context.Context, msg kafkago.Message, cause error) error {
			return publishDeadLetter(ctx, c.DLQ, msg.Topic+DLQSuffix, msg, cause)
		}
	}
	return loop.run(ctx)
}

// Close releases the reader and the dead-letter writer.
func (c *Consumer) Close() error {
	err := c.Reader.Close()
	if c.DLQ != nil {
		err = errors.Join(err, c.DLQ.Close())
	}
	return err
}

// HandleMessage decodes one CloudEvents 1.0 message and dispatches it. It
// returns nil for everything deterministic that cannot be dead-lettered (not a
// valid CloudEvent, a type this consumer does not act on), an errPoison for a
// payload that can never be applied, and a non-nil error for a transient
// failure, after the unit of work rolled back.
func (c *Consumer) HandleMessage(ctx context.Context, value []byte) error {
	logger := defaultLogger(c.Logger)
	e, err := cloudevents.Decode(value)
	if err != nil {
		logger.WarnContext(ctx, "skipping a message that is not a valid CloudEvent", "consumer", c.Name, "error", err)
		return nil
	}
	handle, ok := c.Handlers[e.Type()]
	if !ok {
		return nil
	}
	err = handle(ctx, usecases.Meta{ID: e.ID(), Type: e.Type(), Time: e.Time()}, e)
	if !errors.Is(err, errBadPayload) {
		return err
	}
	logger.WarnContext(ctx, "dead-lettering an event that can never be applied", "consumer", c.Name, "type", e.Type(), "event_id", e.ID(), "error", err)
	if c.DLQ == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", errPoison, err)
}

// decodeData decodes e's JSON `data` into a T and checks the ids the use case
// keys on are present.
func decodeData[T any](e ce.Event, required func(T) []string) (T, error) {
	var v T
	if err := e.DataAs(&v); err != nil {
		return v, fmt.Errorf("%w: %v", errBadPayload, err)
	}
	if required != nil {
		for i, id := range required(v) {
			if id == "" {
				return v, fmt.Errorf("%w: required field #%d is empty", errBadPayload, i+1)
			}
		}
	}
	return v, nil
}
