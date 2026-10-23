package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func eq[T comparable](t *testing.T, name string, got, want T) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
}

func offerOf(t *testing.T, q views.QuoteResult, tier string) views.Offer {
	t.Helper()
	for _, o := range q.Offers {
		if o.Tier == tier {
			return o
		}
	}
	t.Fatalf("no %s offer in %+v (unavailable %+v)", tier, q.Offers, q.Unavailable)
	return views.Offer{}
}

func lines() []shared.Line { return []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}} }

func (e *env) make(offerID string) usecases.MakePromiseResult {
	e.t.Helper()
	res, err := e.svc.MakePromise(ctx, usecases.MakePromiseCommand{OfferID: offerID, Lines: lines()})
	must(e.t, err)
	return res
}

func (e *env) bookOf(id string) *book.Book {
	e.t.Helper()
	b, err := e.store.Books().Get(ctx, book.ID(id))
	must(e.t, err)
	return b
}

func TestQuoteReturnsTheWorkedExampleAndPersistsTheOffers(t *testing.T) {
	e := newEnv(t)
	q := e.quote()
	o := offerOf(t, q, "NEXT_DAY")
	eq(t, "state", o.State, "QUOTED")
	eq(t, "group", o.GroupNo, 1)
	eq(t, "basis", o.Basis, "SCHEDULED")
	eq(t, "policy", o.PolicyVersion, "v0")
	eq(t, "node", o.WitnessPath.NodeID, "N-AUS3")
	eq(t, "pickup", o.WitnessPath.PickupDepartureID, "dep-AUS3-1800")
	eq(t, "wave", o.WitnessPath.DeliveryWaveID, "W-DAU1-1023-A")
	eq(t, "legs", len(o.WitnessPath.Legs), 1)
	if !o.Window.Earliest.Equal(at(23, 9, 0)) || !o.Window.Latest.Equal(at(23, 21, 0)) || !o.ExpiresAt.Equal(at(22, 14, 15)) {
		t.Fatalf("offer = %+v", o)
	}
	eq(t, "tiers offered", len(q.Offers), 3)
	eq(t, "unavailable", len(q.Unavailable), 1)
	eq(t, "SAME_DAY constraint", q.Unavailable[0].Constraint, "DELIVERY_SLA_UNAVAILABLE")
	eq(t, "SAME_DAY group", q.Unavailable[0].GroupNo, 1)
	stored, err := e.svc.GetOffer(ctx, o.OfferID)
	must(t, err)
	eq(t, "stored", stored.OfferID, o.OfferID)
	eq(t, "analytics events", count(e.eventNames(), "DeliveryOfferQuoted"), 3)
}

func TestQuoteWholeQuoteConstraints(t *testing.T) {
	e := newEnv(t)
	q, err := e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{DestinationPostalPrefix: "999", Lines: lines(), Tiers: []shared.Tier{shared.TierNextDay}})
	must(t, err)
	eq(t, "no offers", len(q.Offers), 0)
	eq(t, "not served", q.Unavailable[0].Constraint, "DESTINATION_NOT_SERVED")
	eq(t, "no group on a whole-quote constraint", q.Unavailable[0].GroupNo, 0)
	q, err = e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{DestinationPostalPrefix: "787", Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 38}}})
	must(t, err)
	eq(t, "quantity", q.Unavailable[0].Constraint, "ITEM_QUANTITY_NOT_AVAILABLE")
	eq(t, "all four tiers", len(q.Unavailable), 4)
	eq(t, "nothing persisted", e.store.Counts().Offers, 0)
}

func TestQuoteDeadlineAndDiscontinuedItem(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.PublishPromisePolicy(ctx, policy.Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100,
		Deadlines: []policy.Deadline{{Tier: shared.TierNextDay, RegionID: "R-SC", Minute: 14 * 60}}})
	must(t, err)
	q := e.quote()
	eq(t, "deadline", q.Unavailable[0].Constraint, "PAST_ORDER_DEADLINE")
	eq(t, "tier", q.Unavailable[0].Tier, "NEXT_DAY")
	offerOf(t, q, "TWO_DAY")
	eq(t, "new policy recorded", offerOf(t, q, "TWO_DAY").PolicyVersion, "v1")
	must(t, e.svc.OnCatalogItemChanged(ctx, e.meta("CatalogItemDiscontinued"), usecases.CatalogItemChanged{CatalogItemID: "ci-1001"}, true))
	q = e.quote()
	eq(t, "discontinued", q.Unavailable[len(q.Unavailable)-1].Constraint, "ITEM_QUANTITY_NOT_AVAILABLE")
}

func TestQuoteValidation(t *testing.T) {
	e := newEnv(t)
	bad := map[string]usecases.QuoteCommand{
		"prefix":   {DestinationPostalPrefix: "78 7", Lines: lines()},
		"no lines": {DestinationPostalPrefix: "787"},
		"tier":     {DestinationPostalPrefix: "787", Lines: lines(), Tiers: []shared.Tier{"EXPRESS"}},
		"quantity": {DestinationPostalPrefix: "787", Lines: []shared.Line{{CatalogItemID: "ci-1", Quantity: 0}}},
	}
	for name, cmd := range bad {
		if _, err := e.svc.QuoteDeliveryOffers(ctx, cmd); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestMakePromiseHoldsCapacityAndConsumesTheOffer(t *testing.T) {
	e := newEnv(t)
	o := offerOf(t, e.quote(), "NEXT_DAY")
	e.clock.Advance(4 * time.Minute)
	res := e.make(o.OfferID)
	p := res.Promise
	eq(t, "created", res.Created, true)
	eq(t, "state", p.State, "PROVISIONAL")
	eq(t, "tier", p.Terms.Tier, "NEXT_DAY")
	eq(t, "node", p.WitnessPath.NodeID, "N-AUS3")
	eq(t, "hold expires", p.HoldExpiresAt.Equal(at(22, 14, 24)), true)
	eq(t, "shipBy", p.Terms.ShipBy.Equal(at(22, 18, 0)), true)
	b := e.bookOf("N-AUS3:dep-AUS3-1800")
	eq(t, "provisional", b.Provisional(), 1)
	eq(t, "share from the slot", b.Share(), 2500)
	consumed, _ := e.svc.GetOffer(ctx, o.OfferID)
	eq(t, "offer consumed", consumed.State, "CONSUMED")
	eq(t, "analytics", count(e.eventNames(), "PromiseMade"), 1)
}

func TestMakePromiseReplayAndRefusals(t *testing.T) {
	e := newEnv(t)
	o := offerOf(t, e.quote(), "NEXT_DAY")
	first := e.make(o.OfferID)
	replay := e.make(o.OfferID)
	eq(t, "replay is not created", replay.Created, false)
	eq(t, "same promise", replay.Promise.PromiseID, first.Promise.PromiseID)
	eq(t, "one hold", e.bookOf("N-AUS3:dep-AUS3-1800").Provisional(), 1)
	cases := map[string]struct {
		cmd  usecases.MakePromiseCommand
		want error
	}{
		"missing offer":  {usecases.MakePromiseCommand{OfferID: "off-nope", Lines: lines()}, ports.ErrNotFound},
		"bad offer id":   {usecases.MakePromiseCommand{OfferID: "-", Lines: lines()}, shared.ErrValidation},
		"no lines":       {usecases.MakePromiseCommand{OfferID: o.OfferID}, shared.ErrValidation},
		"other lines":    {usecases.MakePromiseCommand{OfferID: offerOf(t, e.quote(), "TWO_DAY").OfferID, Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 1}}}, shared.ErrValidation},
		"consumed offer": {usecases.MakePromiseCommand{OfferID: o.OfferID, Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 1}}}, shared.ErrValidation},
	}
	for name, c := range cases {
		if _, err := e.svc.MakePromise(ctx, c.cmd); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	e.clock.Advance(11 * time.Minute)
	stale := offerOf(t, e.quote(), "TWO_DAY")
	e.clock.Advance(11 * time.Minute)
	if _, err := e.svc.MakePromise(ctx, usecases.MakePromiseCommand{OfferID: stale.OfferID, Lines: lines()}); !errors.Is(err, offer.ErrOfferNotQuotable) {
		t.Errorf("an expired offer: %v", err)
	}
}

func TestMakePromiseRefusesAWitnessThatIsNoLongerFeasible(t *testing.T) {
	cases := map[string]func(*env){
		"departure cancelled": func(e *env) {
			must(e.t, e.svc.OnDepartureCancelled(ctx, e.meta("DepartureCancelled"), usecases.DepartureCancelled{DepartureID: "dep-SAU1-DAU1-2200"}))
		},
		"departure full": func(e *env) {
			must(e.t, e.svc.OnDepartureCapacityChanged(ctx, e.meta("DepartureCapacityChanged"), usecases.DepartureCapacityChanged{DepartureID: "dep-AUS3-1800", RemainingPackages: 0}))
		},
		"wave full": func(e *env) {
			must(e.t, e.svc.OnWaveCapacityChanged(ctx, e.meta("DeliveryWaveCapacityChanged"), usecases.WaveCapacityChanged{WaveID: "W-DAU1-1023-A", RemainingStops: 0}))
		},
		"wave cancelled": func(e *env) {
			must(e.t, e.svc.OnWaveCancelled(ctx, e.meta("DeliveryWaveCancelled"), usecases.WaveCancelled{WaveID: "W-DAU1-1023-A"}))
		},
		"node suspended": func(e *env) {
			must(e.t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeSuspended"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, readmodel.NodeSuspended))
		},
	}
	for name, mutate := range cases {
		e := newEnv(t)
		o := offerOf(t, e.quote(), "NEXT_DAY")
		mutate(e)
		before := e.store.Counts()
		_, err := e.svc.MakePromise(ctx, usecases.MakePromiseCommand{OfferID: o.OfferID, Lines: lines()})
		if !errors.Is(err, book.ErrPromiseCapacityExhausted) {
			t.Errorf("%s: %v", name, err)
		}
		if e.store.Counts() != before {
			t.Errorf("%s: a refused MakePromise changed state", name)
		}
	}
}

func TestMakePromiseRefusesWhenStockWentAway(t *testing.T) {
	e := newEnv(t)
	o := offerOf(t, e.quote(), "NEXT_DAY")
	must(t, e.svc.OnListingAvailabilityChanged(ctx, e.meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3", AvailableQty: 1, AsOf: at(22, 14, 6),
	}))
	if _, err := e.svc.MakePromise(ctx, usecases.MakePromiseCommand{OfferID: o.OfferID, Lines: lines()}); !errors.Is(err, promise.ErrGroupSpansNodes) {
		t.Fatalf("err = %v", err)
	}
	// a provisional hold reduces what the next quote can promise
	must(t, e.svc.OnListingAvailabilityChanged(ctx, e.meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3", AvailableQty: 3, AsOf: at(22, 14, 7),
	}))
	e.make(offerOf(t, e.quote(), "NEXT_DAY").OfferID)
	q := e.quote()
	eq(t, "3 - 2 held < 2", q.Unavailable[0].Constraint, "ITEM_QUANTITY_NOT_AVAILABLE")
}

func TestTheBookFillsUpAndRaisesExhausted(t *testing.T) {
	e := newEnv(t)
	e.node("N-AUS3", "FULFILLMENT_CENTER", "SORTABLE", "R-SC", func() *usecases.NodeProfileDeclared { p := fcProfile(95, 1); p.ProfileRevision = 2; return p }())
	a := offerOf(t, e.quote(), "NEXT_DAY")
	b := offerOf(t, e.quote(), "NEXT_DAY")
	e.make(a.OfferID)
	eq(t, "exhausted raised", count(e.eventNames(), "PromiseBookExhausted"), 1)
	if _, err := e.svc.MakePromise(ctx, usecases.MakePromiseCommand{OfferID: b.OfferID, Lines: lines()}); !errors.Is(err, book.ErrPromiseCapacityExhausted) {
		t.Fatalf("second promise: %v", err)
	}
	q := e.quote(shared.TierNextDay)
	eq(t, "quote reports the exhausted book", q.Unavailable[0].Constraint, "NODE_CAPACITY_EXHAUSTED")
	books, err := e.svc.ListPromiseBooks(ctx, "N-AUS3", "2026-10-22")
	must(t, err)
	eq(t, "books of the day", len(books.Items), 1)
	eq(t, "capacity", books.Items[0].Capacity.Share, 1)
	books, _ = e.svc.ListPromiseBooks(ctx, "N-AUS3", "2026-10-21")
	eq(t, "other day", len(books.Items), 0)
}

func TestConfirmIsIdempotentAndMovesTheHold(t *testing.T) {
	e := newEnv(t)
	p := e.make(offerOf(t, e.quote(), "NEXT_DAY").OfferID).Promise
	got, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-1", 1)
	must(t, err)
	eq(t, "state", got.State, "CONFIRMED")
	eq(t, "order", got.OrderID, "ord-1")
	b := e.bookOf("N-AUS3:dep-AUS3-1800")
	eq(t, "provisional", b.Provisional(), 0)
	eq(t, "confirmed", b.Confirmed(), 1)
	events := len(e.store.Outbox().Events())
	again, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-1", 1)
	must(t, err)
	eq(t, "repeat", again.State, "CONFIRMED")
	eq(t, "no second event", len(e.store.Outbox().Events()), events)
	eq(t, "still one confirmed", e.bookOf("N-AUS3:dep-AUS3-1800").Confirmed(), 1)
	if _, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-2", 1); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("another order: %v", err)
	}
	if _, err := e.svc.ConfirmPromise(ctx, "prm-nope", "ord-1", 1); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	list, err := e.svc.ListPromisesByOrder(ctx, "ord-1")
	must(t, err)
	eq(t, "by order", len(list.Items), 1)
	if _, err := e.svc.ListPromisesByOrder(ctx, ""); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("order id required: %v", err)
	}
}

func TestConfirmAfterTheHoldLapsedIsRefused(t *testing.T) {
	e := newEnv(t)
	p := e.make(offerOf(t, e.quote(), "NEXT_DAY").OfferID).Promise
	e.clock.Advance(15 * time.Minute)
	if _, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-1", 1); !errors.Is(err, promise.ErrPromiseHoldExpired) {
		t.Fatalf("err = %v", err)
	}
	n, err := e.svc.ReleaseExpiredHolds(ctx, 10)
	must(t, err)
	eq(t, "released", n, 1)
	eq(t, "capacity returned", e.bookOf("N-AUS3:dep-AUS3-1800").Provisional(), 0)
	eq(t, "analytics", count(e.eventNames(), "PromiseReleased"), 1)
	if _, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-1", 1); !errors.Is(err, promise.ErrPromiseClosed) {
		t.Fatalf("a released promise: %v", err)
	}
	n, _ = e.svc.ReleaseExpiredHolds(ctx, 10)
	eq(t, "nothing left to release", n, 0)
}

func TestCancelReleasesConfirmedCapacity(t *testing.T) {
	e := newEnv(t)
	p := e.make(offerOf(t, e.quote(), "NEXT_DAY").OfferID).Promise
	_, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-1", 1)
	must(t, err)
	must(t, e.svc.OnCustomerOrderCancelled(ctx, e.meta("CustomerOrderCancelled"), usecases.CustomerOrderCancelled{OrderID: "ord-1", Reason: "CUSTOMER_REQUEST"}))
	got, _ := e.svc.GetPromise(ctx, p.PromiseID)
	eq(t, "state", got.State, "CANCELLED")
	eq(t, "reason", got.CancelReason, "CUSTOMER_REQUEST")
	eq(t, "confirmed released", e.bookOf("N-AUS3:dep-AUS3-1800").Confirmed(), 0)
	eq(t, "analytics", count(e.eventNames(), "PromiseCancelled"), 1)
	must(t, e.svc.CancelPromises(ctx, "ord-1", nil, "AGAIN"))
	eq(t, "closed promises are skipped", count(e.eventNames(), "PromiseCancelled"), 1)
}

func TestCancelNamedGroupsOnly(t *testing.T) {
	e := newEnv(t)
	p := e.make(offerOf(t, e.quote(), "NEXT_DAY").OfferID).Promise
	_, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-1", 2)
	must(t, err)
	must(t, e.svc.OnCustomerOrderCancelled(ctx, e.meta("CustomerOrderCancelled"), usecases.CustomerOrderCancelled{OrderID: "ord-1", GroupNos: []int{1}}))
	got, _ := e.svc.GetPromise(ctx, p.PromiseID)
	eq(t, "group 2 untouched", got.State, "CONFIRMED")
	must(t, e.svc.OnCustomerOrderCancelled(ctx, e.meta("CustomerOrderCancelled"), usecases.CustomerOrderCancelled{OrderID: "ord-1", GroupNos: []int{2}}))
	got, _ = e.svc.GetPromise(ctx, p.PromiseID)
	eq(t, "group 2 cancelled", got.State, "CANCELLED")
	eq(t, "default reason", got.CancelReason, "ORDER_CANCELLED")
}

func TestOrderPlacedReconcilesTheConfirm(t *testing.T) {
	e := newEnv(t)
	p := e.make(offerOf(t, e.quote(), "NEXT_DAY").OfferID).Promise
	placed := usecases.CustomerOrderPlaced{OrderID: "ord-1", Groups: []usecases.OrderGroup{{GroupNo: 1, PromiseID: p.PromiseID}, {GroupNo: 2, PromiseID: "prm-unknown"}}}
	must(t, e.svc.OnCustomerOrderPlaced(ctx, e.meta("CustomerOrderPlaced"), placed))
	got, _ := e.svc.GetPromise(ctx, p.PromiseID)
	eq(t, "confirmed by the event", got.State, "CONFIRMED")
	must(t, e.svc.OnCustomerOrderPlaced(ctx, e.meta("CustomerOrderPlaced"), placed))
	eq(t, "a second event is a no-op", count(e.eventNames(), "PromiseConfirmed"), 1)
	// a dead hold is logged, not an error
	late := e.make(offerOf(t, e.quote(), "TWO_DAY").OfferID).Promise
	e.clock.Advance(time.Hour)
	must(t, e.svc.OnCustomerOrderPlaced(ctx, e.meta("CustomerOrderPlaced"), usecases.CustomerOrderPlaced{OrderID: "ord-9", Groups: []usecases.OrderGroup{{GroupNo: 1, PromiseID: late.PromiseID}}}))
	got, _ = e.svc.GetPromise(ctx, late.PromiseID)
	eq(t, "still provisional", got.State, "PROVISIONAL")
}

func TestConsumersAreIdempotentOnTheCloudEventsID(t *testing.T) {
	e := newEnv(t)
	p := e.make(offerOf(t, e.quote(), "NEXT_DAY").OfferID).Promise
	m := e.meta("CustomerOrderPlaced")
	placed := usecases.CustomerOrderPlaced{OrderID: "ord-1", Groups: []usecases.OrderGroup{{GroupNo: 1, PromiseID: p.PromiseID}}}
	must(t, e.svc.OnCustomerOrderPlaced(ctx, m, placed))
	before := e.store.Counts()
	must(t, e.svc.OnCustomerOrderPlaced(ctx, m, placed))
	if e.store.Counts() != before {
		t.Fatal("a redelivered message changed state")
	}
}

func TestExpireOffersAndCloseBooks(t *testing.T) {
	e := newEnv(t)
	o := offerOf(t, e.quote(), "NEXT_DAY")
	e.make(offerOf(t, e.quote(), "TWO_DAY").OfferID)
	n, err := e.svc.ExpireOffers(ctx, 10)
	must(t, err)
	eq(t, "nothing expired yet", n, 0)
	e.clock.Advance(10 * time.Minute)
	n, err = e.svc.ExpireOffers(ctx, 10)
	must(t, err)
	eq(t, "expired (the consumed one is not listed)", n >= 3, true)
	got, _ := e.svc.GetOffer(ctx, o.OfferID)
	eq(t, "state", got.State, "EXPIRED")
	closed, err := e.svc.CloseBooks(ctx, 10)
	must(t, err)
	eq(t, "not due", closed, 0)
	e.clock.Set(at(22, 18, 0))
	closed, err = e.svc.CloseBooks(ctx, 10)
	must(t, err)
	eq(t, "closed at the cutoff", closed, 1)
	eq(t, "state", e.bookOf("N-AUS3:dep-AUS3-1800").State(), book.StateClosed)
	closed, _ = e.svc.CloseBooks(ctx, 10)
	eq(t, "once", closed, 0)
}

func TestPolicyPublishAndCurrent(t *testing.T) {
	e := newEnv(t)
	cur, err := e.svc.CurrentPolicy(ctx)
	must(t, err)
	eq(t, "default", cur.PolicyVersion, "v0")
	in := policy.Input{OfferTTLMinutes: 5, HoldTTLMinutes: 20, PackagesPerOrder: 2, SharePercent: 50}
	p1, err := e.svc.PublishPromisePolicy(ctx, in)
	must(t, err)
	p2, err := e.svc.PublishPromisePolicy(ctx, in)
	must(t, err)
	eq(t, "v1", p1.PolicyVersion, "v1")
	eq(t, "v2", p2.PolicyVersion, "v2")
	cur, _ = e.svc.CurrentPolicy(ctx)
	eq(t, "current", cur.Version, 2)
	eq(t, "events", count(e.eventNames(), "PromisePolicyPublished"), 2)
	in.OfferTTLMinutes = 0
	if _, err := e.svc.PublishPromisePolicy(ctx, in); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("invalid policy: %v", err)
	}
	// the TTL of the policy is used by the next quote
	o := offerOf(t, e.quote(), "NEXT_DAY")
	eq(t, "ttl 5 minutes", o.ExpiresAt.Equal(at(22, 14, 10)), true)
	// 2 packages per order need room for 2 on every departure and the share halves
	p := e.make(o.OfferID).Promise
	eq(t, "hold ttl 20 minutes", p.HoldExpiresAt.Equal(at(22, 14, 25)), true)
	eq(t, "share at 50%", e.bookOf("N-AUS3:dep-AUS3-1800").Share(), 1250)
}
