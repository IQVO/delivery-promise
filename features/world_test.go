package features_test

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/memory"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// The worked example of the promise model, Thursday 2026-10-22 14:05 in Austin
// (CDT, UTC-5): a shopper in postal prefix 787 asks for ci-1001 x2.
var cdt = time.FixedZone("CDT", -5*3600)

func at(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, cdt).UTC() }

// world is the state of one scenario: the in-memory store behind the use
// cases, the offers of the last quote, the promise the scenario works on and
// the outcome of the last command.
type world struct {
	store *memory.Store
	clock *memory.FakeClock
	svc   *usecases.Service

	events       int
	lastQuote    views.QuoteResult
	promiseID    string
	firstPromise string
	created      bool
	lastErr      error
	ran          bool
	counts       memory.Counts
	outboxLen    int
}

func newWorld() *world {
	w := &world{}
	w.reset()
	return w
}

// reset wires a fresh empty store, clock and id sequence.
func (w *world) reset() {
	w.store = memory.NewStore()
	w.clock = memory.NewFakeClock(at(22, 14, 5))
	w.svc = usecases.New(usecases.Deps{
		UoW: w.store.UnitOfWork(), Offers: w.store.Offers(), Promises: w.store.Promises(), Books: w.store.Books(),
		Policies: w.store.Policies(), Network: w.store.Network(), Shipments: w.store.Shipments(), Outbox: w.store.Outbox(),
		Inbox: w.store.Inbox(), Clock: w.clock, IDs: &memory.SeqIDs{},
	})
	w.events, w.lastQuote, w.promiseID, w.firstPromise, w.created, w.lastErr, w.ran = 0, views.QuoteResult{}, "", "", false, nil, false
	w.counts, w.outboxLen = memory.Counts{}, 0
}

func (w *world) ctx() context.Context { return context.Background() }

// meta mints a fresh CloudEvents envelope for a consumer call.
func (w *world) meta(typ string) usecases.Meta {
	w.events++
	return usecases.Meta{ID: fmt.Sprintf("evt-%04d", w.events), Type: typ, Time: w.clock.Now()}
}

// run executes one command and remembers its error and where the outbox stood,
// so the Then steps can ask what the command published. A refused command is
// not a failed step: the scenario asserts the outcome.
func (w *world) run(command func() error) {
	w.outboxLen = len(w.store.Outbox().Events())
	w.counts = w.store.Counts()
	w.lastErr = command()
	w.ran = true
}

// given executes a precondition: it must succeed, or the scenario is broken.
func (w *world) given(what string, command func() error) error {
	if err := command(); err != nil {
		return fmt.Errorf("precondition %q failed: %w", what, err)
	}
	return nil
}

// published returns the names of the events appended since the last command ran.
func (w *world) published() []string {
	events := w.store.Outbox().Events()[w.outboxLen:]
	names := make([]string, len(events))
	for i, e := range events {
		names[i] = e.EventName()
	}
	return names
}

func (w *world) offer(tier string) (views.Offer, error) {
	for _, o := range w.lastQuote.Offers {
		if o.Tier == tier {
			return o, nil
		}
	}
	return views.Offer{}, fmt.Errorf("the last quote has no %s offer (offers %d, unavailable %+v)", tier, len(w.lastQuote.Offers), w.lastQuote.Unavailable)
}

func (w *world) promise() (views.Promise, error) {
	if w.promiseID == "" {
		return views.Promise{}, errNoPromise
	}
	return w.svc.GetPromise(w.ctx(), w.promiseID)
}

func lines(item string, qty int) []shared.Line {
	return []shared.Line{{CatalogItemID: item, Quantity: qty}}
}

var (
	errNoPromise = errors.New("the scenario has not made a promise yet")
	errNoCommand = errors.New("no command was run in this scenario")
)
