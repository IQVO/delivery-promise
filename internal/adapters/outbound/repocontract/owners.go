package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// ownerCases pin the owner of a line and of a listing (ADR 0017): every
// adapter persists it, and the hold queries net per (node, item, ownerRef).
var ownerCases = []named{
	{"an offer's lines keep their owners", ownedOfferRoundTrip},
	{"a promise's lines keep their owners, through a rehome too", ownedPromiseRoundTrip},
	{"a listing keeps its owner; operator-owned is ''", ownedListingRoundTrip},
	{"HeldQuantities and ConfirmedHolds are keyed by owner", ownedHolds},
}

// NewOwnedPromise builds a PROVISIONAL promise like NewPromise whose ci-1001 x2
// line is covered by owner (the only owner with stock).
func NewOwnedPromise(t *testing.T, id, offerID, node, pickup, owner string) *promise.Promise {
	t.Helper()
	p, _, err := promise.Make(promise.Making{
		ID: promise.ID(id), OfferID: offerID, DestinationPrefix: "787", Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
		Terms: promise.Terms{
			Tier: shared.TierNextDay, ShipBy: T0.Add(4 * time.Hour), Basis: shared.BasisScheduled, PolicyVersion: "v0",
			Window: shared.Window{Earliest: T0.Add(18 * time.Hour), Latest: T0.Add(30 * time.Hour), TimeZone: "America/Chicago"},
		},
		Witness: shared.Witness{NodeID: node, PickupDepartureID: pickup, Legs: []string{"dep-2200"}, DeliveryWaveID: "w-1"},
		BookID:  string(book.MakeID(node, pickup)), HoldTTL: 15 * time.Minute,
		Stock: map[string][]shared.OwnerStock{"ci-1001": {{OwnerRef: owner, Available: 2}}},
	}, T0)
	noErr(t, err)
	return p
}

func ownedOfferRoundTrip(t *testing.T, f Fixture) {
	s := NewOffer(t, "off-o").Snapshot()
	s.Lines = []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2, OwnerRef: "S-0042"}, {CatalogItemID: "ci-2002", Quantity: 1}}
	o, err := offer.Restore(s)
	noErr(t, err)
	noErr(t, f.Offers.Save(ctx, o))
	got, err := f.Offers.Get(ctx, "off-o")
	noErr(t, err)
	eq(t, "seller line", got.Lines()[0].OwnerRef, "S-0042")
	eq(t, "operator line", got.Lines()[1].OwnerRef, "")
}

func ownedPromiseRoundTrip(t *testing.T, f Fixture) {
	noErr(t, f.Promises.Save(ctx, NewOwnedPromise(t, "prm-o", "off-o", "N-AUS3", "dep-1800", "S-0042")))
	p, err := f.Promises.Get(ctx, "prm-o")
	noErr(t, err)
	eq(t, "owner", p.Lines()[0].OwnerRef, "S-0042")
	_, err = p.Confirm("ord-1", 1, T0.Add(time.Minute))
	noErr(t, err)
	noErr(t, f.Promises.Save(ctx, p))
	w := shared.Witness{NodeID: "N-DFW7", PickupDepartureID: "dep-9", Legs: []string{"dep-2200"}, DeliveryWaveID: "w-1"}
	noErr(t, p.Rehome(w, "N-DFW7:dep-9", []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2, OwnerRef: "S-0007"}}))
	noErr(t, f.Promises.Save(ctx, p))
	got, err := f.Promises.Get(ctx, "prm-o")
	noErr(t, err)
	eq(t, "rehomed owner", got.Lines()[0].OwnerRef, "S-0007")
	eq(t, "rehomed node", got.Witness().NodeID, "N-DFW7")
}

func ownedListingRoundTrip(t *testing.T, f Fixture) {
	n := f.Network
	noErr(t, n.PutListing(ctx, readmodel.Listing{ID: "lst-s", ItemID: "ci-1", NodeID: "n1", OwnerRef: "S-0042", AvailableQty: 3, AsOf: T0}))
	noErr(t, n.PutListing(ctx, readmodel.Listing{ID: "lst-o", ItemID: "ci-1", NodeID: "n1", AvailableQty: 5, AsOf: T0}))
	s, err := n.GetListing(ctx, "lst-s")
	noErr(t, err)
	eq(t, "seller", s.OwnerRef, "S-0042")
	o, err := n.GetListing(ctx, "lst-o")
	noErr(t, err)
	eq(t, "operator", o.OwnerRef, "")
	ls, err := n.ListingsForNode(ctx, "n1", []string{"ci-1"})
	noErr(t, err)
	eq(t, "both owners of one item at one node", len(ls), 2)
	eq(t, "by listing id", ls[0].OwnerRef+"|"+ls[1].OwnerRef, "|S-0042")
}

func ownedHolds(t *testing.T, f Fixture) {
	noErr(t, f.Promises.Save(ctx, NewOwnedPromise(t, "prm-a", "off-a", "N-AUS3", "dep-1800", "S-A")))
	noErr(t, f.Promises.Save(ctx, NewOwnedPromise(t, "prm-b", "off-b", "N-AUS3", "dep-1800", "S-B")))
	noErr(t, f.Promises.Save(ctx, NewOwnedPromise(t, "prm-c", "off-c", "N-AUS3", "dep-1800", "S-B")))
	noErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-o", "off-o", "N-AUS3", "dep-1800")))
	held, err := f.Promises.HeldQuantities(ctx, []string{"N-AUS3"})
	noErr(t, err)
	key := func(owner string) repository.HoldKey {
		return repository.HoldKey{NodeID: "N-AUS3", ItemID: "ci-1001", OwnerRef: owner}
	}
	eq(t, "S-A", held[key("S-A")], 2)
	eq(t, "S-B", held[key("S-B")], 4)
	eq(t, "operator", held[key("")], 2)
	eq(t, "one key per owner", len(held), 3)

	p, err := f.Promises.Get(ctx, "prm-b")
	noErr(t, err)
	_, err = p.Confirm("ord-1", 1, T0.Add(time.Minute))
	noErr(t, err)
	noErr(t, f.Promises.Save(ctx, p))
	confirmed, err := f.Promises.ConfirmedHolds(ctx, []string{"N-AUS3"})
	noErr(t, err)
	eq(t, "one confirmed line", len(confirmed), 1)
	eq(t, "keyed by its owner", confirmed[0].Key, key("S-B"))
}
