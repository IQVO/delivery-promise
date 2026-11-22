package requestfacts

import (
	"context"
	"fmt"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/domain/requestfact"
)

// Writer stores one fact. The production one appends one analytics outbox row
// in its own short transaction (OutboxWriter).
type Writer interface {
	Write(ctx context.Context, f requestfact.Fact) error
}

// Queue is the bounded in-memory hand-over between the request path and the
// single background worker (ADR 0032). Offer never blocks and never fails the
// caller: a full queue drops the newest fact. Run drains the queue every flush
// interval and writes each fact on its own; a failed write drops the fact
// (best effort by design, there is no retry). Every loss is counted and
// logged at WARN (rate limited).
type Queue struct {
	facts   chan requestfact.Fact
	writer  Writer
	metrics ports.RequestFactMetrics
	logger  *slog.Logger
	cfg     Config
	now     func() time.Time

	lastWarn atomic.Int64 // unix nanoseconds of the last drop WARN
	dropped  atomic.Int64 // drops since the last drop WARN
}

// NewQueue builds the Queue. A nil metrics records nothing, a nil logger uses
// slog.Default().
func NewQueue(w Writer, m ports.RequestFactMetrics, logger *slog.Logger, cfg Config) *Queue {
	if logger == nil {
		logger = slog.Default()
	}
	cfg = cfg.normalised()
	return &Queue{facts: make(chan requestfact.Fact, cfg.Capacity), writer: w, metrics: m, logger: logger, cfg: cfg, now: time.Now}
}

// Capacity is the bound of the queue.
func (q *Queue) Capacity() int { return cap(q.facts) }

// Offer queues f without ever blocking: when the queue is full the fact is
// dropped and counted. It never panics.
func (q *Queue) Offer(f requestfact.Fact) {
	defer func() {
		if r := recover(); r != nil {
			q.drop(context.Background(), ReasonInternal, fmt.Errorf("offer panicked: %v", r))
		}
	}()
	select {
	case q.facts <- f:
	default:
		q.drop(context.Background(), ReasonQueueFull, nil)
	}
}

// Run is the single worker: it drains the queue every flush interval until ctx
// is cancelled, then drains what is left for at most ShutdownDrain and
// returns ctx.Err().
func (q *Queue) Run(ctx context.Context) error {
	t := time.NewTicker(q.cfg.FlushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			q.finalDrain(ShutdownDrain)
			return ctx.Err()
		case <-t.C:
			q.drain(ctx)
		}
	}
}

// drain writes the facts that were queued when it started; later ones wait for
// the next tick, so a steady inflow cannot keep one drain running forever.
func (q *Queue) drain(ctx context.Context) {
	for range len(q.facts) {
		if ctx.Err() != nil {
			return
		}
		select {
		case f := <-q.facts:
			q.write(ctx, f)
		default:
			return
		}
	}
}

// finalDrain stores what is still queued after the worker was stopped, within
// ShutdownDrain; whatever is left then is lost and counted.
func (q *Queue) finalDrain(timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	q.drain(ctx)
	for {
		select {
		case <-q.facts:
			q.drop(context.Background(), ReasonShutdown, nil)
		default:
			return
		}
	}
}

// write stores one fact in its own bounded transaction; a failure (an error or
// a panic of the writer) drops the fact.
func (q *Queue) write(ctx context.Context, f requestfact.Fact) {
	wctx, cancel := context.WithTimeout(ctx, WriteTimeout)
	defer cancel()
	if err := q.safeWrite(wctx, f); err != nil {
		q.drop(ctx, ReasonWriteFailed, err)
		return
	}
	if q.metrics != nil {
		q.metrics.FactWritten(ctx)
	}
}

func (q *Queue) safeWrite(ctx context.Context, f requestfact.Fact) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("writer panicked: %v", r)
		}
	}()
	return q.writer.Write(ctx, f)
}

// drop counts a lost fact and, at most once per warnEvery, logs it at WARN
// with the drops since the previous log.
func (q *Queue) drop(ctx context.Context, reason string, cause error) {
	if q.metrics != nil {
		q.metrics.FactDropped(ctx, reason)
	}
	n := q.dropped.Add(1)
	now := q.now().UnixNano()
	last := q.lastWarn.Load()
	if now-last < int64(warnEvery) || !q.lastWarn.CompareAndSwap(last, now) {
		return
	}
	q.dropped.Add(-n)
	attrs := []any{"reason", reason, "dropped_since_last_warning", n, "queue_capacity", cap(q.facts)}
	if cause != nil {
		attrs = append(attrs, "error", cause)
	}
	q.logger.Warn("request fact dropped (best effort, no response was affected)", sanitizeArgs(attrs)...)
}
