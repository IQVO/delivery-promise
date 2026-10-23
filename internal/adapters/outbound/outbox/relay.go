// Package outbox is the transactional-outbox relay: a background loop that
// drains unpublished outbox rows (written in the same transaction as the
// aggregate) to a Sink, Kafka in production. Delivery is at-least-once with the
// CloudEvents id persisted at encode time, so a retry republishes the same id.
package outbox

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Defaults for the relay's tuning knobs.
const (
	DefaultInterval  = time.Second
	DefaultBatchSize = 100
	// DefaultMaxAttempts is the attempt cap a composition root uses when
	// OUTBOX_MAX_ATTEMPTS is unset. The relay's own zero value stays 0
	// (unlimited), so the cap is always an explicit WithMaxAttempts at the
	// composition root (fitness test TestOutboxRelaysPassAttemptCap).
	DefaultMaxAttempts = 20
)

// AttemptCapReachedMsg is the ERROR log message written once for a row whose
// failure reached the attempt cap.
const AttemptCapReachedMsg = "outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause"

// Header is one Kafka message header.
type Header struct {
	Key   string
	Value string
}

// Message is one outbox row's payload: the exact Kafka wire form of one event,
// minted ONCE when the use case runs so a relay retry republishes identical
// bytes (same CloudEvents id).
type Message struct {
	EventID    string // CloudEvents id, a UUID persisted so redelivery never changes it
	Topic      string // destination topic; the relay's writer has none and routes by this
	EventType  string // the full CloudEvents type
	Subject    string // CloudEvents subject (the aggregate id)
	Key        []byte // Kafka key (the aggregate id): one aggregate stays on one partition
	DataSchema string // CloudEvents dataschema URN
	Value      []byte // structured-mode CloudEvents JSON
	Headers    []Header
}

// SendFailure is the error a Store's Drain returns when send failed for one
// row: the pass stopped there, the failure was recorded on the row and
// Attempts is the row's failure count AFTER this one (0 when recording it
// failed too).
type SendFailure struct {
	RowID    int64
	Message  Message
	Attempts int
	Err      error
}

// Error keeps the "outbox row <id> (<type>): <cause>" wording runbooks grep for.
func (f *SendFailure) Error() string {
	return fmt.Sprintf("outbox row %d (%s): %v", f.RowID, f.Message.EventType, f.Err)
}

// Unwrap exposes the send error.
func (f *SendFailure) Unwrap() error { return f.Err }

// Store is the relay's view of the outbox table: Drain claims up to limit
// unpublished rows in id order, skipping rows that already failed maxAttempts
// times when maxAttempts > 0, calls send for each one at a time, marks a row
// published only after send returned nil, and stops at the first send error,
// which it returns as a *SendFailure.
type Store interface {
	Drain(ctx context.Context, limit, maxAttempts int, send func(context.Context, Message) error) (int, error)
}

// Sink is where drained messages go.
type Sink interface {
	Send(ctx context.Context, msgs ...Message) error
}

// OutboxRelay drains a Store into a Sink.
type OutboxRelay struct {
	store       Store
	sink        Sink
	logger      *slog.Logger
	interval    time.Duration
	batchSize   int
	maxAttempts int
}

// Option customises an OutboxRelay.
type Option func(*OutboxRelay)

// WithInterval sets how long the relay sleeps after a pass that did not fill a
// batch (or failed). Non-positive values are ignored.
func WithInterval(d time.Duration) Option {
	return func(r *OutboxRelay) {
		if d > 0 {
			r.interval = d
		}
	}
}

// WithBatchSize caps how many rows one pass claims. Non-positive values are ignored.
func WithBatchSize(n int) Option {
	return func(r *OutboxRelay) {
		if n > 0 {
			r.batchSize = n
		}
	}
}

// WithMaxAttempts caps how many times a row may fail before it is excluded
// from future claims. The excluded row stays in the outbox with its attempts
// and last error, the failure that reaches the cap is logged at ERROR with the
// row's id, topic, type and key, and later rows overtake it. 0 means
// unlimited; negative values are ignored.
func WithMaxAttempts(n int) Option {
	return func(r *OutboxRelay) {
		if n >= 0 {
			r.maxAttempts = n
		}
	}
}

// NewOutboxRelay constructs a relay draining store into sink.
func NewOutboxRelay(store Store, sink Sink, logger *slog.Logger, opts ...Option) *OutboxRelay {
	if logger == nil {
		logger = slog.Default()
	}
	r := &OutboxRelay{store: store, sink: sink, logger: logger, interval: DefaultInterval, batchSize: DefaultBatchSize}
	for _, o := range opts {
		o(r)
	}
	return r
}

// RelayOnce performs a single pass and returns how many rows it published.
func (r *OutboxRelay) RelayOnce(ctx context.Context) (int, error) {
	n, err := r.store.Drain(ctx, r.batchSize, r.maxAttempts, func(ctx context.Context, m Message) error {
		return r.sink.Send(ctx, m)
	})
	r.logAttemptCap(ctx, err)
	return n, err
}

func (r *OutboxRelay) logAttemptCap(ctx context.Context, err error) {
	var sf *SendFailure
	if r.maxAttempts == 0 || !errors.As(err, &sf) || sf.Attempts < r.maxAttempts {
		return
	}
	r.logger.ErrorContext(ctx, AttemptCapReachedMsg,
		"outbox_id", sf.RowID, "topic", sf.Message.Topic, "event_type", sf.Message.EventType,
		"key", string(sf.Message.Key), "attempts", sf.Attempts, "max_attempts", r.maxAttempts, "error", sf.Err)
}

// Run drains the outbox until ctx is cancelled and then returns ctx.Err(). A
// pass that publishes a full batch is followed at once by another; any other
// pass sleeps for the interval. A failing pass is logged and retried after the
// interval: the rows stay unpublished, so nothing is lost. Run never dials
// anything itself: a broker that is down at boot just makes passes fail.
func (r *OutboxRelay) Run(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.RelayOnce(ctx)
		if err != nil && !errors.Is(err, context.Canceled) {
			r.logger.ErrorContext(ctx, "outbox relay pass failed", "error", err)
		}
		if n == r.batchSize && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(r.interval):
		}
	}
}
