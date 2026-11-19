package requestfacts

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/requestfact"
)

type fakeMetrics struct {
	mu      sync.Mutex
	written int
	dropped map[string]int
}

func newFakeMetrics() *fakeMetrics { return &fakeMetrics{dropped: map[string]int{}} }

func (m *fakeMetrics) FactWritten(context.Context) { m.mu.Lock(); m.written++; m.mu.Unlock() }
func (m *fakeMetrics) FactDropped(_ context.Context, reason string) {
	m.mu.Lock()
	m.dropped[reason]++
	m.mu.Unlock()
}
func (m *fakeMetrics) snapshot() (int, map[string]int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]int{}
	for k, v := range m.dropped {
		out[k] = v
	}
	return m.written, out
}

type writerFunc func(ctx context.Context, f requestfact.Fact) error

func (w writerFunc) Write(ctx context.Context, f requestfact.Fact) error { return w(ctx, f) }

func fact(items int) requestfact.Fact {
	return requestfact.Fact{Region: "78", Kind: requestfact.KindQuote, Items: items, At: time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)}
}

func logs() (*slog.Logger, *bytes.Buffer, *sync.Mutex) {
	var buf bytes.Buffer
	var mu sync.Mutex
	return slog.New(slog.NewTextHandler(lockedWriter{&buf, &mu}, nil)), &buf, &mu
}

type lockedWriter struct {
	b  *bytes.Buffer
	mu *sync.Mutex
}

func (w lockedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func TestConfigDefaults(t *testing.T) {
	for _, c := range []Config{{}, {Capacity: -1, FlushInterval: -time.Second}, {Capacity: MaxCapacity + 1}} {
		if got := c.normalised(); got.Capacity != DefaultCapacity || got.FlushInterval != DefaultFlushInterval {
			t.Errorf("%+v normalised to %+v", c, got)
		}
	}
	if got := (Config{Capacity: 1, FlushInterval: time.Nanosecond}).normalised(); got.Capacity != 1 || got.FlushInterval != time.Nanosecond {
		t.Errorf("a valid config changed: %+v", got)
	}
	if got := (Config{Capacity: MaxCapacity}).normalised(); got.Capacity != MaxCapacity {
		t.Errorf("the maximum capacity changed: %+v", got)
	}
	q := NewQueue(writerFunc(nil), nil, nil, Config{Capacity: 7})
	if q.Capacity() != 7 {
		t.Fatalf("Capacity = %d", q.Capacity())
	}
}

func TestAFullQueueDropsTheNewestFactAndCountsIt(t *testing.T) {
	m := newFakeMetrics()
	lg, buf, mu := logs()
	q := NewQueue(writerFunc(nil), m, lg, Config{Capacity: 2})
	done := make(chan struct{})
	go func() { // Offer must return at once whatever the state of the worker: it never blocks
		defer close(done)
		for i := 1; i <= 5; i++ {
			q.Offer(fact(i))
		}
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Offer blocked on a full queue")
	}
	_, dropped := m.snapshot()
	if dropped[ReasonQueueFull] != 3 || len(dropped) != 1 {
		t.Fatalf("dropped = %v, want 3 queue_full", dropped)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(buf.String(), "level=WARN") || !strings.Contains(buf.String(), "reason=queue_full") || !strings.Contains(buf.String(), "queue_capacity=2") {
		t.Fatalf("no WARN naming the reason: %q", buf.String())
	}
	if first, second := <-q.facts, <-q.facts; first.Items != 1 || second.Items != 2 {
		t.Fatalf("the OLDEST facts stay queued (drop-new): %d, %d", first.Items, second.Items)
	}
}

func TestTheWorkerWritesEveryQueuedFactOncePerFlush(t *testing.T) {
	m := newFakeMetrics()
	var mu sync.Mutex
	var items []int
	q := NewQueue(writerFunc(func(ctx context.Context, f requestfact.Fact) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("a write has no deadline: it could stall the worker")
		}
		mu.Lock()
		items = append(items, f.Items)
		mu.Unlock()
		return nil
	}), m, nil, Config{Capacity: 10, FlushInterval: 5 * time.Millisecond})
	for i := 1; i <= 4; i++ {
		q.Offer(fact(i))
	}
	ctx, stop := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- q.Run(ctx) }()
	waitFor(t, func() bool { w, _ := m.snapshot(); return w == 4 })
	stop()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(items) != 4 || items[0] != 1 || items[3] != 4 {
		t.Fatalf("written in order = %v", items)
	}
	if _, dropped := m.snapshot(); len(dropped) != 0 {
		t.Fatalf("dropped = %v", dropped)
	}
}

func TestAFailedWriteDropsTheFactCountsItAndTheWorkerGoesOn(t *testing.T) {
	m := newFakeMetrics()
	lg, buf, mu := logs()
	var calls atomic.Int32
	q := NewQueue(writerFunc(func(_ context.Context, f requestfact.Fact) error {
		calls.Add(1)
		if f.Items%2 == 1 {
			return errors.New("outbox down")
		}
		return nil
	}), m, lg, Config{Capacity: 10, FlushInterval: 5 * time.Millisecond})
	for i := 1; i <= 4; i++ {
		q.Offer(fact(i))
	}
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { _ = q.Run(ctx) }()
	waitFor(t, func() bool { w, d := m.snapshot(); return w == 2 && d[ReasonWriteFailed] == 2 })
	if calls.Load() != 4 {
		t.Fatalf("each fact is tried exactly once (no retry): %d calls", calls.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(buf.String(), "reason=write_failed") || !strings.Contains(buf.String(), "outbox down") {
		t.Fatalf("the WARN does not name the cause: %q", buf.String())
	}
}

func TestAPanickingWriterDropsTheFactAndTheWorkerSurvives(t *testing.T) {
	m := newFakeMetrics()
	q := NewQueue(writerFunc(func(_ context.Context, f requestfact.Fact) error {
		if f.Items == 1 {
			panic("boom")
		}
		return nil
	}), m, nil, Config{Capacity: 10, FlushInterval: 5 * time.Millisecond})
	q.Offer(fact(1))
	q.Offer(fact(2))
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { _ = q.Run(ctx) }()
	waitFor(t, func() bool { w, d := m.snapshot(); return w == 1 && d[ReasonWriteFailed] == 1 })
}

func TestABlockedWriterIsAbandonedAtTheWriteTimeoutAndNeverBlocksOffer(t *testing.T) {
	m := newFakeMetrics()
	q := NewQueue(writerFunc(func(ctx context.Context, _ requestfact.Fact) error {
		<-ctx.Done() // a database that does not answer: only the deadline ends the write
		return ctx.Err()
	}), m, nil, Config{Capacity: 2, FlushInterval: time.Millisecond})
	q.Offer(fact(1))
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { _ = q.Run(ctx) }()
	start := time.Now()
	for i := 0; i < 50; i++ { // while the worker is stuck, the request path stays instant
		q.Offer(fact(i))
	}
	if time.Since(start) > time.Second {
		t.Fatalf("Offer took %v while the writer was blocked", time.Since(start))
	}
	_, dropped := m.snapshot()
	if dropped[ReasonQueueFull] == 0 {
		t.Fatalf("a stuck worker fills the queue and the newest facts are dropped: %v", dropped)
	}
}

func TestDropsAreLoggedAtMostOncePerInterval(t *testing.T) {
	m := newFakeMetrics()
	lg, buf, mu := logs()
	q := NewQueue(writerFunc(nil), m, lg, Config{Capacity: 1})
	clock := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	q.now = func() time.Time { return clock }
	q.Offer(fact(1)) // queued
	q.Offer(fact(2)) // dropped: first WARN
	q.Offer(fact(3)) // dropped within the interval: counted, not logged
	clock = clock.Add(warnEvery - time.Nanosecond)
	q.Offer(fact(4)) // still inside the interval
	clock = clock.Add(time.Nanosecond)
	q.Offer(fact(5)) // the interval elapsed: second WARN reports the drops since the first
	mu.Lock()
	defer mu.Unlock()
	out := buf.String()
	if strings.Count(out, "level=WARN") != 2 {
		t.Fatalf("want 2 WARN lines, got:\n%s", out)
	}
	if !strings.Contains(out, "dropped_since_last_warning=1") || !strings.Contains(out, "dropped_since_last_warning=3") {
		t.Fatalf("the WARNs do not carry the counts since the previous one:\n%s", out)
	}
	if _, dropped := m.snapshot(); dropped[ReasonQueueFull] != 4 {
		t.Fatalf("every drop is counted even when not logged: %v", dropped)
	}
}

func TestOfferNeverPanics(t *testing.T) {
	m := newFakeMetrics()
	q := NewQueue(writerFunc(nil), m, nil, Config{Capacity: 1})
	close(q.facts) // sending on a closed channel panics: Offer turns it into a counted drop
	q.Offer(fact(1))
	if _, dropped := m.snapshot(); dropped[ReasonInternal] != 1 {
		t.Fatalf("dropped = %v", dropped)
	}
}

func TestStoppingTheWorkerDrainsWhatIsLeftAndCountsWhatIsNot(t *testing.T) {
	m := newFakeMetrics()
	q := NewQueue(writerFunc(func(context.Context, requestfact.Fact) error { return nil }), m, nil, Config{Capacity: 10, FlushInterval: time.Hour})
	for i := 1; i <= 3; i++ {
		q.Offer(fact(i))
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if err := q.Run(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	if w, d := m.snapshot(); w != 3 || len(d) != 0 {
		t.Fatalf("the final drain wrote %d, dropped %v", w, d)
	}
}

func TestTheFinalDrainStopsAtItsDeadlineAndCountsTheRest(t *testing.T) {
	m := newFakeMetrics()
	q := NewQueue(writerFunc(func(ctx context.Context, _ requestfact.Fact) error {
		<-ctx.Done() // the database never answers
		return ctx.Err()
	}), m, nil, Config{Capacity: 10})
	for i := 1; i <= 3; i++ {
		q.Offer(fact(i))
	}
	start := time.Now()
	q.finalDrain(20 * time.Millisecond)
	if time.Since(start) > 2*time.Second {
		t.Fatalf("the final drain ran %v past its deadline", time.Since(start))
	}
	// the first fact timed out (write_failed); the other two were left and are counted as lost at shutdown
	if _, d := m.snapshot(); d[ReasonWriteFailed] != 1 || d[ReasonShutdown] != 2 || len(q.facts) != 0 {
		t.Fatalf("dropped = %v, queued %d", d, len(q.facts))
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
