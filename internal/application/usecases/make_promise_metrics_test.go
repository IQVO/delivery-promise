package usecases_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// recordingMetrics counts the ports.PromiseMetrics calls of a use case.
type recordingMetrics struct {
	mu      sync.Mutex
	made    int
	refused []string
}

var _ ports.PromiseMetrics = (*recordingMetrics)(nil)

func (m *recordingMetrics) PromiseMade(context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.made++
}

func (m *recordingMetrics) PromiseRefused(_ context.Context, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refused = append(m.refused, reason)
}

// meteredService is e's service with m injected.
func (e *env) meteredService(m ports.PromiseMetrics) *usecases.Service {
	return usecases.New(usecases.Deps{
		UoW: e.store.UnitOfWork(), Offers: e.store.Offers(), Promises: e.store.Promises(), Books: e.store.Books(), Policies: e.store.Policies(),
		Network: e.store.Network(), Shipments: e.store.Shipments(), Outbox: e.store.Outbox(), Inbox: e.store.Inbox(),
		Clock: e.clock, IDs: e.ids, Metrics: m,
	})
}

func TestMakePromiseCountsEachAttemptOnceByOutcome(t *testing.T) {
	e := newEnv(t)
	o := offerOf(t, e.quote(), "NEXT_DAY")
	m := &recordingMetrics{}
	svc := e.meteredService(m)
	cmd := usecases.MakePromiseCommand{OfferID: o.OfferID, Lines: lines()}

	if _, err := svc.MakePromise(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	eq(t, "made after a new promise", m.made, 1)

	// The same request again is a replay of the stored promise, not an attempt.
	if _, err := svc.MakePromise(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	eq(t, "made after a replay", m.made, 1)
	eq(t, "refused after a replay", len(m.refused), 0)

	e.clock.Advance(30 * time.Minute)
	if _, err := svc.MakePromise(ctx, usecases.MakePromiseCommand{OfferID: "off-nope", Lines: lines()}); err == nil {
		t.Fatal("a missing offer must be refused")
	}
	if _, err := svc.MakePromise(ctx, usecases.MakePromiseCommand{OfferID: "-", Lines: lines()}); err == nil {
		t.Fatal("a bad offer id must be refused")
	}
	if _, err := svc.MakePromise(ctx, usecases.MakePromiseCommand{OfferID: o.OfferID, Lines: []shared.Line{}}); err == nil {
		t.Fatal("no lines must be refused")
	}
	want := []string{"not-found", "validation-failed", "validation-failed"}
	eq(t, "refusals", len(m.refused), len(want))
	for i, r := range want {
		eq(t, "reason", m.refused[i], r)
	}
	eq(t, "made after refusals", m.made, 1)
}

func TestMakePromiseWithoutMetricsStillWorks(t *testing.T) {
	e := newEnv(t)
	o := offerOf(t, e.quote(), "NEXT_DAY")
	eq(t, "created", e.make(o.OfferID).Created, true)
}
