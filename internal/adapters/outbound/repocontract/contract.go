// Package repocontract is the repository contract suite: the behaviour every
// implementation of the out ports must show, run against the in-memory doubles
// and against Postgres. It is test support (it imports "testing") but not a
// _test.go file so that both adapters can run it; cmd/** must never import it
// (enforced by internal/architecture).
package repocontract

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Fixture is one implementation under test, empty at the start of each case.
type Fixture struct {
	UoW       ports.UnitOfWork
	Offers    ports.OfferRepository
	Promises  ports.PromiseRepository
	Books     ports.BookRepository
	Policies  ports.PolicyRepository
	Network   ports.NetworkViews
	Shipments ports.ShipmentViews
	Outbox    ports.EventOutbox
	Inbox     ports.Inbox
	// OutboxCount returns the number of outbox rows committed so far.
	OutboxCount func() int
}

type named struct {
	name string
	fn   func(*testing.T, Fixture)
}

// Run executes every case of the suite, each on a fresh Fixture.
func Run(t *testing.T, newFixture func(t *testing.T) Fixture) {
	t.Helper()
	groups := []struct {
		name  string
		cases []named
	}{
		{"offers", offerCases}, {"promises", promiseCases}, {"books", bookCases}, {"policies", policyCases},
		{"network views", networkCases}, {"shipment views", shipmentCases}, {"unit of work", unitOfWorkCases},
	}
	for _, g := range groups {
		for _, c := range g.cases {
			t.Run(g.name+"/"+c.name, func(t *testing.T) { c.fn(t, newFixture(t)) })
		}
	}
}

func noErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func eq[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func eqTime(t *testing.T, name string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

var ctx = context.Background()

// T0 is the fixed instant every builder works from.
var T0 = time.Date(2026, 10, 22, 19, 5, 0, 0, time.UTC)

// NewOffer builds a QUOTED offer for ci-1001 x2 at node N-AUS3.
func NewOffer(t *testing.T, id string) *offer.Offer {
	t.Helper()
	o, _, err := offer.New(offer.Quote{
		ID: offer.ID(id), GroupNo: 1, DestinationPrefix: "787", Tier: shared.TierNextDay,
		Lines:   []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
		Window:  shared.Window{Earliest: T0.Add(18 * time.Hour), Latest: T0.Add(30 * time.Hour), TimeZone: "America/Chicago"},
		Witness: shared.Witness{NodeID: "N-AUS3", PickupDepartureID: "dep-1800", Legs: []string{"dep-2200"}, DeliveryWaveID: "w-1"},
		Basis:   shared.BasisScheduled, PolicyVersion: "v0", TTL: 10 * time.Minute, ExpiresAt: T0.Add(10 * time.Minute),
		LatestMeetableAt: T0.Add(3 * time.Hour), PickupCutoffAt: T0.Add(4 * time.Hour),
	}, T0)
	noErr(t, err)
	return o
}

// NewBook builds an OPEN book of the given share at node/pickup.
func NewBook(t *testing.T, node, pickup string, share int) *book.Book {
	t.Helper()
	b, err := book.Open(book.Opening{NodeID: node, PickupDepartureID: pickup, CutoffAt: T0.Add(4 * time.Hour), HoldDeadlineAt: T0.Add(3 * time.Hour), Share: share})
	noErr(t, err)
	return b
}

// NewPromise builds a PROVISIONAL promise from offer offerID at node on book
// node:pickup, with legs, a wave and the lines ci-1001 x2.
func NewPromise(t *testing.T, id, offerID, node, pickup string) *promise.Promise {
	t.Helper()
	p, _, err := promise.Make(promise.Making{
		ID: promise.ID(id), OfferID: offerID, DestinationPrefix: "787", Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
		Terms: promise.Terms{
			Tier: shared.TierNextDay, ShipBy: T0.Add(4 * time.Hour), Basis: shared.BasisScheduled, PolicyVersion: "v0",
			Window: shared.Window{Earliest: T0.Add(18 * time.Hour), Latest: T0.Add(30 * time.Hour), TimeZone: "America/Chicago"},
		},
		Witness: shared.Witness{NodeID: node, PickupDepartureID: pickup, Legs: []string{"dep-2200"}, DeliveryWaveID: "w-1"},
		BookID:  string(book.MakeID(node, pickup)), HoldTTL: 15 * time.Minute, Available: map[string]int{"ci-1001": 2},
	}, T0)
	noErr(t, err)
	return p
}

// NewPolicy publishes version current+1 with a NEXT_DAY deadline.
func NewPolicy(t *testing.T, current int) *policy.Policy {
	t.Helper()
	p, _, err := policy.Publish(current, policy.Input{
		OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 2, SharePercent: 80,
		Deadlines: []policy.Deadline{{Tier: shared.TierNextDay, RegionID: "R-SC", Minute: 23*60 + 59}},
	}, T0)
	noErr(t, err)
	return p
}

func offerID(s string) offer.ID { return offer.ID(s) }

// mustEvent returns a real domain event for the outbox cases.
func mustEvent(t *testing.T) shared.Event {
	t.Helper()
	return promise.PromiseReleased{Header: shared.Header{Version: 2, At: T0}, PromiseID: "prm-1", Reason: promise.ReleaseHoldExpired}
}
