package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// ADR 0017: availability and netting are per (node, item, ownerRef), a line is
// covered by ONE owner, and offer and promise lines carry the owner chosen.

// sellerListing projects a seller's listing of ci-1001 at a node with its
// figure computed at asOf.
func (e *env) sellerListing(node, listing, owner string, qty int, asOf time.Time) {
	e.t.Helper()
	must(e.t, e.svc.OnListingCreated(ctx, e.meta("ListingCreated"), usecases.ListingCreated{
		ListingID: listing, CatalogItemID: "ci-1001", NodeID: node, OwnerRef: owner,
	}))
	must(e.t, e.svc.OnListingAvailabilityChanged(ctx, e.meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: listing, CatalogItemID: "ci-1001", NodeID: node, OwnerRef: owner, AvailableQty: qty, AsOf: asOf,
	}))
}

// sellersOnly zeroes the operator's ci-1001 at N-AUS3 and lists the sellers'.
func (e *env) sellersOnly(stock map[string]int) {
	e.t.Helper()
	e.feed(0, at(22, 14, 1))
	for _, owner := range []string{"S-A", "S-B", "S-C"} {
		if qty, ok := stock[owner]; ok {
			e.sellerListing("N-AUS3", "lst-"+owner, owner, qty, at(22, 14, 1))
		}
	}
}

func (e *env) quoteQty(qty int) []string {
	e.t.Helper()
	res, err := e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		DestinationPostalPrefix: "787", Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: qty}}, Tiers: []shared.Tier{shared.TierNextDay},
	})
	must(e.t, err)
	if len(res.Offers) == 0 {
		return []string{"NONE:" + res.Unavailable[0].Constraint}
	}
	return []string{res.Offers[0].OfferID, res.Offers[0].Lines[0].OwnerRef}
}

func TestTwoSellersWithThreeEachCannotTakeALineOfFive(t *testing.T) {
	e := newEnv(t)
	e.sellersOnly(map[string]int{"S-A": 3, "S-B": 3})
	eq(t, "x5 from 3 + 3", e.quoteQty(5)[0], "NONE:ITEM_QUANTITY_NOT_AVAILABLE")
	eq(t, "x3 is one owner's", e.quoteQty(3)[1], "S-A")
}

func TestTheOfferAndThePromiseCarryTheChosenOwner(t *testing.T) {
	e := newEnv(t)
	e.sellersOnly(map[string]int{"S-A": 6, "S-B": 8})
	q := e.quote(shared.TierNextDay)
	o := offerOf(t, q, "NEXT_DAY")
	eq(t, "the offer names the biggest seller", o.Lines[0].OwnerRef, "S-B")
	got, err := e.svc.GetOffer(ctx, o.OfferID)
	must(t, err)
	eq(t, "the stored offer keeps it", got.Lines[0].OwnerRef, "S-B")
	p := e.make(o.OfferID).Promise
	eq(t, "the promise takes it", p.Lines[0].OwnerRef, "S-B")
	var made promise.PromiseMade
	for _, ev := range e.store.Outbox().Events() {
		if m, ok := ev.(promise.PromiseMade); ok {
			made = m
		}
	}
	eq(t, "PromiseMade names it", made.Lines[0].OwnerRef, "S-B")
}

func TestAnOperatorOwnedLineNamesNoOwner(t *testing.T) {
	e := newEnv(t)
	e.sellerListing("N-AUS3", "lst-S-A", "S-A", 90, at(22, 14, 1))
	o := offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY")
	eq(t, "the operator covers first", o.Lines[0].OwnerRef, "")
}

// TestAHoldOnOneOwnerDoesNotReduceAnother is the netting isolation of rule 2.
func TestAHoldOnOneOwnerDoesNotReduceAnother(t *testing.T) {
	e := newEnv(t)
	e.sellersOnly(map[string]int{"S-A": 2, "S-B": 3})
	o := offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY")
	eq(t, "the first promise holds S-B", e.make(o.OfferID).Promise.Lines[0].OwnerRef, "S-B")
	// S-B nets to 1, S-A keeps its 2 units: the next x2 is S-A's
	eq(t, "S-A is untouched by S-B's hold", e.quoteQty(2)[1], "S-A")
	eq(t, "S-B alone cannot take x2 any more, nor can S-A take x3", e.quoteQty(3)[0], "NONE:ITEM_QUANTITY_NOT_AVAILABLE")
}

// TestTheReservationLagStaysPerKey is ADR 0015 under rule 2: a confirmed hold on
// S-A is released only by a figure of S-A's own key computed after the lag.
func TestTheReservationLagStaysPerKey(t *testing.T) {
	e := newEnv(t)
	e.sellersOnly(map[string]int{"S-A": 2, "S-B": 2})
	id := e.confirmedNextDay("ord-1", 1)
	got, err := e.svc.GetPromise(ctx, id)
	must(t, err)
	eq(t, "confirmed on S-A (the tie)", got.Lines[0].OwnerRef, "S-A")

	eq(t, "S-A's units are netted, S-B's are not", e.quoteQty(2)[1], "S-B")

	// confirmed at 14:05: a figure computed from 16:05 on accounts for the order
	e.clock.Advance(2 * time.Hour)
	e.sellerListing("N-AUS3", "lst-S-B", "S-B", 0, at(22, 16, 6))
	eq(t, "S-B's figure after the lag releases nothing of S-A", e.quoteQty(2)[0], "NONE:ITEM_QUANTITY_NOT_AVAILABLE")
	e.sellerListing("N-AUS3", "lst-S-A", "S-A", 2, at(22, 16, 6))
	eq(t, "S-A's own figure after the lag accounts for the order", e.quoteQty(2)[1], "S-A")
}

func TestMakePromiseTakesTheOwnerThatStillCovers(t *testing.T) {
	e := newEnv(t)
	e.sellersOnly(map[string]int{"S-A": 6, "S-B": 8})
	o := offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY")
	eq(t, "quoted on S-B", o.Lines[0].OwnerRef, "S-B")
	e.sellerListing("N-AUS3", "lst-S-B", "S-B", 1, at(22, 14, 2)) // S-B sold out meanwhile
	res := e.make(o.OfferID)
	eq(t, "not a re-quote: the promise is made", res.Created, true)
	eq(t, "from the offer", res.Promise.OfferID, o.OfferID)
	eq(t, "on the owner that still covers", res.Promise.Lines[0].OwnerRef, "S-A")
}

func TestMakePromiseRefusesWhenNoSingleOwnerCoversAnyMore(t *testing.T) {
	e := newEnv(t)
	e.sellersOnly(map[string]int{"S-A": 2})
	o := offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY")
	e.sellerListing("N-AUS3", "lst-S-A", "S-A", 1, at(22, 14, 2))
	e.sellerListing("N-AUS3", "lst-S-B", "S-B", 1, at(22, 14, 2))
	_, err := e.svc.MakePromise(ctx, usecases.MakePromiseCommand{OfferID: o.OfferID, Lines: lines()})
	if !errors.Is(err, promise.ErrGroupSpansNodes) {
		t.Fatalf("1 + 1 covered a line of 2: err = %v", err)
	}
}
