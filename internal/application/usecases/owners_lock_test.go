package usecases_test

import (
	"errors"
	"testing"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// seedSellerFulfillmentCenter adds N-DFW7, whose ci-1001 is held only by the
// given sellers.
func (e *env) seedSellerFulfillmentCenter(stock map[string]int) {
	e.t.Helper()
	e.node("N-DFW7", "FULFILLMENT_CENTER", "SORTABLE", "R-SC", fcProfile(95, 2500))
	e.departure("dep-DFW7-1800", "lane-1", "PICKUP", "N-DFW7", "N-SAU1", at(22, 18, 0), at(22, 19, 15), 40)
	for _, owner := range []string{"S-X", "S-Y", "S-Z"} {
		if qty, ok := stock[owner]; ok {
			e.sellerListing("N-DFW7", "lst-DFW7-"+owner, owner, qty, at(22, 14, 1))
		}
	}
}

// TestALockReNetsTheLinesUnderTheLockedNode is rule 6: the rehome applies the
// OwnerChoice rule at the locked node and the promise takes that node's owner.
func TestALockReNetsTheLinesUnderTheLockedNode(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1) // operator-owned at N-AUS3
	e.seedSellerFulfillmentCenter(map[string]int{"S-X": 1, "S-Z": 4, "S-Y": 4})

	e.lockAt(id, "N-DFW7", "dep-DFW7-1800")

	got, err := e.svc.GetPromise(ctx, id)
	must(t, err)
	eq(t, "the promise follows the lock", got.WitnessPath.NodeID, "N-DFW7")
	eq(t, "on the locked node's owner (tie to the lowest)", got.Lines[0].OwnerRef, "S-Y")
	eq(t, "charged there", e.bookOf("N-DFW7:dep-DFW7-1800").Confirmed(), 1)
	// the confirmed units now net S-Y at N-DFW7 only
	stock, err := e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{DestinationPostalPrefix: "787",
		Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 37}}, Tiers: []shared.Tier{shared.TierNextDay}})
	must(t, err)
	eq(t, "N-AUS3's operator stock is whole again", stock.Offers[0].WitnessPath.NodeID, "N-AUS3")
}

// TestALockWhereNoSingleOwnerCoversKeepsTheCharge: two sellers with 1 each at
// the locked node cannot take a line of 2; the lock is a fact, so the charge
// stays where it was and the projection does not fail.
func TestALockWhereNoSingleOwnerCoversKeepsTheCharge(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	e.seedSellerFulfillmentCenter(map[string]int{"S-X": 1, "S-Y": 1})

	e.lockAt(id, "N-DFW7", "dep-DFW7-1800")

	got, err := e.svc.GetPromise(ctx, id)
	must(t, err)
	eq(t, "the promise stays on the quoted node", got.WitnessPath.NodeID, "N-AUS3")
	eq(t, "with its owner", got.Lines[0].OwnerRef, "")
	eq(t, "the quoted node keeps the charge", e.bookOf("N-AUS3:dep-AUS3-1800").Confirmed(), 1)
	if _, err := e.store.Books().Get(ctx, "N-DFW7:dep-DFW7-1800"); err == nil {
		t.Fatal("the locked node was charged")
	}
}

// TestListingProjectionIsKeyedByOwner is rule 1: the owner travels from the
// listing events into AvailabilityView, absent means "", and the asOf rule
// orders the figures of one key only.
func TestListingProjectionIsKeyedByOwner(t *testing.T) {
	e := newEnv(t)
	e.sellerListing("N-AUS3", "lst-S-A", "S-A", 5, at(22, 15, 0))
	e.sellerListing("N-AUS3", "lst-S-B", "S-B", 4, at(22, 14, 30)) // older than S-A's: still applied
	a, err := e.store.Network().GetListing(ctx, "lst-S-A")
	must(t, err)
	b, err := e.store.Network().GetListing(ctx, "lst-S-B")
	must(t, err)
	op, err := e.store.Network().GetListing(ctx, "lst-1")
	must(t, err)
	eq(t, "S-A owner", a.OwnerRef, "S-A")
	eq(t, "S-B figure", b.AvailableQty, 4)
	eq(t, "operator-owned is ''", op.OwnerRef, "")

	// an older figure of the same key is ignored
	e.sellerListing("N-AUS3", "lst-S-A", "S-A", 1, at(22, 14, 45))
	a, err = e.store.Network().GetListing(ctx, "lst-S-A")
	must(t, err)
	eq(t, "S-A keeps its newer figure", a.AvailableQty, 5)
}

func TestARowSuppressedBeforeItsCreationGainsItsKey(t *testing.T) {
	e := newEnv(t)
	must(t, e.svc.OnListingToggled(ctx, e.meta("ListingSuppressed"), usecases.ListingToggled{ListingID: "lst-early", NodeID: "N-AUS3"}, true))
	must(t, e.svc.OnListingCreated(ctx, e.meta("ListingCreated"), usecases.ListingCreated{
		ListingID: "lst-early", CatalogItemID: "ci-1001", NodeID: "N-AUS3", OwnerRef: "S-Q",
	}))
	l, err := e.store.Network().GetListing(ctx, "lst-early")
	must(t, err)
	eq(t, "item", l.ItemID, "ci-1001")
	eq(t, "owner", l.OwnerRef, "S-Q")
	eq(t, "still suppressed", l.Suppressed, true)
	// a known listing is not rewritten by a replayed creation
	must(t, e.svc.OnListingCreated(ctx, e.meta("ListingCreated"), usecases.ListingCreated{
		ListingID: "lst-early", CatalogItemID: "ci-9", NodeID: "N-AUS3", OwnerRef: "S-Q",
	}))
	l, err = e.store.Network().GetListing(ctx, "lst-early")
	must(t, err)
	eq(t, "item kept", l.ItemID, "ci-1001")
}

func TestAMalformedOwnerRefIsRefused(t *testing.T) {
	e := newEnv(t)
	err := e.svc.OnListingCreated(ctx, e.meta("ListingCreated"), usecases.ListingCreated{ListingID: "lst-x", CatalogItemID: "ci-1001", NodeID: "N-AUS3", OwnerRef: "-x"})
	if !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("ListingCreated: err = %v", err)
	}
	err = e.svc.OnListingAvailabilityChanged(ctx, e.meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: "lst-x", CatalogItemID: "ci-1001", NodeID: "N-AUS3", OwnerRef: "-x", AvailableQty: 1, AsOf: at(22, 14, 1),
	})
	if !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("ListingAvailabilityChanged: err = %v", err)
	}
}
