package usecases_test

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Leak A: units of a CONFIRMED promise became sellable again at confirm. The
// worked example of the docs (order-assignment-model.md): the order is placed
// at 14:10, the fleet raises the held order at 15:56 and releases it to the
// floor at 16:02, so the WMS reserves nothing for about 1h50 and the feed that
// nets the reservation can only be computed after that.

// feed projects a ListingAvailabilityChanged for lst-1 at the injected clock.
func (e *env) feed(qty int, asOf time.Time) {
	e.t.Helper()
	must(e.t, e.svc.OnListingAvailabilityChanged(ctx, e.meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3", AvailableQty: qty, AsOf: asOf,
	}))
}

// canPromiseTwoUnits reports whether a fresh quote for ci-1001 x2 still has a NEXT_DAY offer.
func (e *env) canPromiseTwoUnits() bool {
	e.t.Helper()
	q := e.quote(shared.TierNextDay)
	if len(q.Offers) == 0 {
		eq(e.t, "refusal", q.Unavailable[0].Constraint, "ITEM_QUANTITY_NOT_AVAILABLE")
		return false
	}
	return true
}

func TestConfirmedUnitsStayDecrementedUntilTheFeedThatAccountsForThemLands(t *testing.T) {
	e := newEnv(t)
	e.feed(2, at(22, 14, 1)) // exactly the two units the shopper is about to buy
	eq(t, "before the order, two units can be promised", e.canPromiseTwoUnits(), true)

	// 14:05 promise (provisional hold), 14:10 order placed -> confirmed.
	p := e.make(offerOf(t, e.quote(), "NEXT_DAY").OfferID).Promise
	e.clock.Advance(5 * time.Minute)
	_, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-1", 1)
	must(t, err)

	eq(t, "14:10 confirmed: the units are not sellable again", e.canPromiseTwoUnits(), false)

	// 14:25 the provisional hold would have lapsed (HoldTTL 15): the sweep must change nothing.
	e.clock.Advance(15 * time.Minute)
	_, err = e.svc.ReleaseExpiredHolds(ctx, 10)
	must(t, err)
	eq(t, "14:25 after the hold TTL and the sweep", e.canPromiseTwoUnits(), false)

	// 14:30 a feed computed BEFORE the warehouse reserved anything lands, still 2 units.
	e.clock.Advance(5 * time.Minute)
	e.feed(2, at(22, 14, 30))
	eq(t, "14:30 a feed that predates the reservation", e.canPromiseTwoUnits(), false)

	// 15:00 an out-of-order older feed (asOf 14:10) arrives late: ignored, still netted.
	e.clock.Advance(30 * time.Minute)
	e.feed(2, at(22, 14, 10))
	eq(t, "15:00 a late, older feed", e.canPromiseTwoUnits(), false)

	// 16:02 the fleet releases the order and the WMS reserves; 16:09 a feed computed after
	// that lands: the figure now accounts for the order, nothing is netted twice.
	e.clock.Advance(69 * time.Minute)
	e.feed(0, at(22, 16, 8))
	eq(t, "16:09 the accounting feed (usable is 0)", e.canPromiseTwoUnits(), false)
	e.feed(2, at(22, 16, 10)) // replenished afterwards: the confirmed order is no longer netted
	eq(t, "16:10 stock replenished after the reservation", e.canPromiseTwoUnits(), true)
}

func TestACancelledConfirmedPromiseGivesItsUnitsBack(t *testing.T) {
	e := newEnv(t)
	e.feed(2, at(22, 14, 1))
	p := e.make(offerOf(t, e.quote(), "NEXT_DAY").OfferID).Promise
	_, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-1", 1)
	must(t, err)
	eq(t, "confirmed", e.canPromiseTwoUnits(), false)
	_, err = e.svc.CancelPromise(ctx, p.PromiseID)
	must(t, err)
	eq(t, "cancelled", e.canPromiseTwoUnits(), true)
}
