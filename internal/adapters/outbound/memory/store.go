// Package memory holds in-memory TEST DOUBLES of the application's out ports.
// They are for unit, BDD and contract tests only: cmd/** never imports this
// package (enforced by internal/architecture) because the service runs on real
// Postgres and Kafka.
//
// Every aggregate repository round-trips its aggregate through Snapshot and
// Restore, so a loaded aggregate never aliases stored state, a corrupt row can
// not become a live aggregate and the doubles keep the persistence contract of
// the real adapters: a miss is ports.ErrNotFound, a stale or duplicate save is
// ports.ErrConcurrentUpdate.
//
// One Store backs every double. Its UnitOfWork is atomic: it holds the store
// for the whole unit and restores the state taken at the start when the unit
// fails or panics, outbox and inbox included.
package memory

import (
	"context"
	"sync"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// state is everything a unit of work can change. Stored values are replaced,
// never edited in place, so copying the maps is enough.
type state struct {
	offers      map[offer.ID]offer.Snapshot
	promises    map[promise.ID]promise.Snapshot
	books       map[book.ID]book.Snapshot
	policies    map[int]policy.Snapshot
	nodes       map[string]readmodel.Node
	regions     map[string]readmodel.Region
	areas       map[string]readmodel.Area
	lanes       map[string]search.Lane
	items       map[string]readmodel.Item
	listings    map[string]readmodel.Listing
	departures  map[string]search.Departure
	waves       map[string]search.Wave
	scorecards  map[string]readmodel.Scorecard
	assignments map[string]readmodel.Assignment
	shipments   map[string][]string
	trackingOf  map[string]string
	outcomes    map[string]readmodel.PackageOutcome
	inbox       map[string]string
	outbox      []shared.Event
}

func newState() state {
	return state{
		offers: map[offer.ID]offer.Snapshot{}, promises: map[promise.ID]promise.Snapshot{}, books: map[book.ID]book.Snapshot{},
		policies: map[int]policy.Snapshot{}, nodes: map[string]readmodel.Node{}, regions: map[string]readmodel.Region{},
		areas: map[string]readmodel.Area{}, lanes: map[string]search.Lane{}, items: map[string]readmodel.Item{},
		listings: map[string]readmodel.Listing{}, departures: map[string]search.Departure{}, waves: map[string]search.Wave{},
		scorecards: map[string]readmodel.Scorecard{}, assignments: map[string]readmodel.Assignment{}, shipments: map[string][]string{},
		trackingOf: map[string]string{}, outcomes: map[string]readmodel.PackageOutcome{}, inbox: map[string]string{},
	}
}

func cloneMap[K comparable, V any](m map[K]V) map[K]V {
	out := make(map[K]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (st state) clone() state {
	return state{
		offers: cloneMap(st.offers), promises: cloneMap(st.promises), books: cloneMap(st.books), policies: cloneMap(st.policies),
		nodes: cloneMap(st.nodes), regions: cloneMap(st.regions), areas: cloneMap(st.areas), lanes: cloneMap(st.lanes),
		items: cloneMap(st.items), listings: cloneMap(st.listings), departures: cloneMap(st.departures), waves: cloneMap(st.waves),
		scorecards: cloneMap(st.scorecards), assignments: cloneMap(st.assignments), shipments: cloneMap(st.shipments),
		trackingOf: cloneMap(st.trackingOf), outcomes: cloneMap(st.outcomes), inbox: cloneMap(st.inbox),
		outbox: append([]shared.Event(nil), st.outbox...),
	}
}

// Store is the shared state of the in-memory doubles.
type Store struct {
	mu sync.Mutex
	st state
}

// NewStore returns an empty store.
func NewStore() *Store { return &Store{st: newState()} }

// txKey marks a ctx that carries the open unit of work of one store.
type txKey struct{ store *Store }

// enter locks the store for one repository call, unless ctx already carries the
// unit of work that holds it. The returned func releases the lock.
func (s *Store) enter(ctx context.Context) func() {
	if ctx.Value(txKey{s}) != nil {
		return func() {}
	}
	s.mu.Lock()
	return s.mu.Unlock
}

// Offers returns the OfferRepository double.
func (s *Store) Offers() *OfferRepository { return &OfferRepository{s} }

// Promises returns the PromiseRepository double.
func (s *Store) Promises() *PromiseRepository { return &PromiseRepository{s} }

// Books returns the BookRepository double.
func (s *Store) Books() *BookRepository { return &BookRepository{s} }

// Policies returns the PolicyRepository double.
func (s *Store) Policies() *PolicyRepository { return &PolicyRepository{s} }

// Network returns the NetworkViews double.
func (s *Store) Network() *NetworkViews { return &NetworkViews{s} }

// Shipments returns the ShipmentViews double.
func (s *Store) Shipments() *ShipmentViews { return &ShipmentViews{s} }

// Outbox returns the EventOutbox double.
func (s *Store) Outbox() *Outbox { return &Outbox{s} }

// Inbox returns the Inbox double.
func (s *Store) Inbox() *Inbox { return &Inbox{s} }

// UnitOfWork returns the UnitOfWork double.
func (s *Store) UnitOfWork() *UnitOfWork { return &UnitOfWork{s} }

// Counts is how many rows each aggregate table of the store holds.
type Counts struct {
	Offers, Promises, Books, Policies, OutboxEvents, InboxRows int
	// VersionSum is the sum of every aggregate version: any saved change moves it.
	VersionSum int64
}

// Counts returns the size of every table and the sum of the aggregate
// versions, so a test can prove that a failed use case stored and changed
// nothing.
func (s *Store) Counts() Counts {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v int64
	for _, o := range s.st.offers {
		v += o.Version
	}
	for _, p := range s.st.promises {
		v += p.Version
	}
	for _, b := range s.st.books {
		v += b.Version
	}
	return Counts{
		Offers: len(s.st.offers), Promises: len(s.st.promises), Books: len(s.st.books), Policies: len(s.st.policies),
		OutboxEvents: len(s.st.outbox), InboxRows: len(s.st.inbox), VersionSum: v,
	}
}

// UnitOfWork is the in-memory ports.UnitOfWork: serialised, atomic, re-entrant.
type UnitOfWork struct{ s *Store }

// Do runs fn holding the store. When fn returns an error or panics the state
// taken before fn is restored; the error is returned unchanged. A Do inside a
// Do joins it.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if ctx.Value(txKey{u.s}) != nil {
		return fn(ctx)
	}
	u.s.mu.Lock()
	defer u.s.mu.Unlock()
	before := u.s.st.clone()
	committed := false
	defer func() {
		if !committed {
			u.s.st = before
		}
	}()
	err = fn(context.WithValue(ctx, txKey{u.s}, struct{}{}))
	committed = err == nil
	return err
}

// Outbox is the in-memory ports.EventOutbox.
type Outbox struct{ s *Store }

// Append stores the events in order.
func (o *Outbox) Append(ctx context.Context, events ...shared.Event) error {
	defer o.s.enter(ctx)()
	o.s.st.outbox = append(o.s.st.outbox, events...)
	return nil
}

// Events returns the appended events in order (a copy of the slice).
func (o *Outbox) Events() []shared.Event {
	o.s.mu.Lock()
	defer o.s.mu.Unlock()
	return append([]shared.Event(nil), o.s.st.outbox...)
}

// Inbox is the in-memory ports.Inbox.
type Inbox struct{ s *Store }

// Begin records the CloudEvents id and reports whether it is new.
func (i *Inbox) Begin(ctx context.Context, eventID, eventType string, _ time.Time) (bool, error) {
	defer i.s.enter(ctx)()
	if _, seen := i.s.st.inbox[eventID]; seen {
		return false, nil
	}
	i.s.st.inbox[eventID] = eventType
	return true, nil
}
