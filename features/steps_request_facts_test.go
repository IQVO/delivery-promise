package features_test

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/cucumber/godog"

	inboundhttp "github.com/claudioed/delivery-promise/internal/adapters/inbound/http"
	"github.com/claudioed/delivery-promise/internal/application/requestfacts"
	"github.com/claudioed/delivery-promise/internal/domain/requestfact"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// requestFactsWorld is the state of request_facts.feature: the REAL REST
// router with the best-effort recorder and the bounded queue of ADR 0032 over
// the in-memory outbox. The worker is driven deterministically: stopping it
// runs its final drain, so no scenario sleeps or races.
type requestFactsWorld struct {
	queue   *requestfacts.Queue
	metrics *countingFactMetrics
}

// countingFactMetrics is the ports.RequestFactMetrics the scenarios read.
type countingFactMetrics struct {
	mu      sync.Mutex
	written int
	dropped map[string]int
}

func (m *countingFactMetrics) FactWritten(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.written++
}

func (m *countingFactMetrics) FactDropped(_ context.Context, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropped[reason]++
}

// brokenOutbox is an outbox that refuses every append.
type brokenOutbox struct{}

func (brokenOutbox) Append(context.Context, ...shared.Event) error { return errors.New("outbox down") }

// enable switches the request facts ON the way cmd/api does: a bounded queue,
// a recorder over it, and the same router with the recorder behind it.
func (r *requestFactsWorld) enable(w *world, capacity int, brokenStore bool) {
	r.metrics = &countingFactMetrics{dropped: map[string]int{}}
	n := 0
	writer := requestfacts.OutboxWriter{UoW: w.store.UnitOfWork(), Outbox: w.store.Outbox(), NewID: func() string { n++; return fmt.Sprintf("req-%d", n) }}
	if brokenStore {
		writer.Outbox = brokenOutbox{}
	}
	r.queue = requestfacts.NewQueue(writer, r.metrics, nil, requestfacts.Config{Capacity: capacity})
	rec := requestfacts.NewRecorder(r.queue, r.metrics, w.clock.Now)
	w.router = inboundhttp.NewRouter(&inboundhttp.Server{API: w.svc, Facts: rec}, nil, "")
}

// drain runs the worker until its final drain ends: every queued fact is
// written or dropped, as at a graceful stop.
func (r *requestFactsWorld) drain() error {
	if r.queue == nil {
		return errors.New("the request facts are not enabled in this scenario")
	}
	ctx, stop := context.WithCancel(context.Background())
	stop()
	if err := r.queue.Run(ctx); !errors.Is(err, context.Canceled) {
		return fmt.Errorf("the worker returned %v", err)
	}
	return nil
}

func (r *requestFactsWorld) facts(w *world) []requestfact.DeliveryRequestAnswered {
	out := []requestfact.DeliveryRequestAnswered{}
	for _, e := range w.store.Outbox().Events() {
		if f, ok := e.(requestfact.DeliveryRequestAnswered); ok {
			out = append(out, f)
		}
	}
	return out
}

func (r *requestFactsWorld) count(want int, w *world) error {
	if got := len(r.facts(w)); got != want {
		return fmt.Errorf("%d DeliveryRequestAnswered rows in the outbox, want %d", got, want)
	}
	return nil
}

func (r *requestFactsWorld) lastFact(w *world, kind, region string, answered bool) error {
	all := r.facts(w)
	if len(all) == 0 {
		return errors.New("no DeliveryRequestAnswered in the outbox")
	}
	f := all[len(all)-1]
	if string(f.Kind) != kind || f.Region != region || f.Answered != answered || (!answered && f.ShortestPromisedDays != nil) || (answered && f.ShortestPromisedDays == nil) {
		return fmt.Errorf("fact = %+v, want kind %s region %s answered %v", f, kind, region, answered)
	}
	return nil
}

func (r *requestFactsWorld) droppedIs(reason string, want int) error {
	if r.metrics == nil {
		return errors.New("the request facts are not enabled in this scenario")
	}
	r.metrics.mu.Lock()
	defer r.metrics.mu.Unlock()
	if got := r.metrics.dropped[reason]; got != want {
		return fmt.Errorf("%d facts dropped for %q, want %d (%v)", got, reason, want, r.metrics.dropped)
	}
	return nil
}

func (r *requestFactsWorld) writtenIs(want int) error {
	r.metrics.mu.Lock()
	defer r.metrics.mu.Unlock()
	if r.metrics.written != want {
		return fmt.Errorf("%d facts written, want %d", r.metrics.written, want)
	}
	return nil
}

// registerRequestFactSteps registers the steps of request_facts.feature.
func registerRequestFactSteps(sc *godog.ScenarioContext, w *world) {
	r := &requestFactsWorld{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*r = requestFactsWorld{}
		return ctx, nil
	})
	sc.Step(`^the request facts are enabled with a queue of (\d+)$`, func(n int) { r.enable(w, n, false) })
	sc.Step(`^the request facts are enabled with a queue of (\d+) over an outbox that fails$`, func(n int) { r.enable(w, n, true) })
	sc.Step(`^the request facts worker drains the queue$`, r.drain)
	sc.Step(`^the outbox holds (\d+) DeliveryRequestAnswered rows?$`, func(n int) error { return r.count(n, w) })
	sc.Step(`^the last request fact is a (QUOTE|ESTIMATE) for region "([^"]*)" answered with a promise$`, func(kind, region string) error { return r.lastFact(w, kind, region, true) })
	sc.Step(`^the last request fact is a (QUOTE|ESTIMATE) for region "([^"]*)" answered with no promise$`, func(kind, region string) error { return r.lastFact(w, kind, region, false) })
	sc.Step(`^(\d+) request facts? (?:was|were) dropped as "([^"]*)"$`, func(n int, reason string) error { return r.droppedIs(reason, n) })
	sc.Step(`^(\d+) request facts? (?:was|were) written$`, r.writtenIs)
}
