package repocontract

import (
	"strconv"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

var consolidatedCases = []named{
	{"a CONSOLIDATED offer round-trips consolidationEligible, true, false and unknown", consolidatedOfferRoundTrip},
	{"a CONSOLIDATED promise keeps consolidationEligible through a revision", consolidatedPromiseRoundTrip},
}

func flagPtr(b bool) *bool { return &b }

func flagText(p *bool) string {
	if p == nil {
		return "unknown"
	}
	return strconv.FormatBool(*p)
}

func consolidatedOffer(t *testing.T, id string, eligible *bool) *offer.Offer {
	t.Helper()
	o, _, err := offer.New(offer.Quote{
		ID: offer.ID(id), GroupNo: 1, DestinationPrefix: "787", Tier: shared.TierConsolidated,
		Lines:   []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
		Window:  shared.Window{Earliest: T0.Add(66 * time.Hour), Latest: T0.Add(78 * time.Hour), TimeZone: "America/Chicago"},
		Witness: shared.Witness{NodeID: "N-AUS3", PickupDepartureID: "dep-1800", Legs: []string{"dep-2200"}, DeliveryWaveID: "w-3"},
		Basis:   shared.BasisScheduled, PolicyVersion: "v0", TTL: 10 * time.Minute, ExpiresAt: T0.Add(10 * time.Minute),
		LatestMeetableAt: T0.Add(3 * time.Hour), PickupCutoffAt: T0.Add(4 * time.Hour), ConsolidationEligible: eligible,
	}, T0)
	noErr(t, err)
	return o
}

func consolidatedOfferRoundTrip(t *testing.T, f Fixture) {
	for i, want := range []*bool{flagPtr(true), flagPtr(false), nil} {
		id := "off-c" + strconv.Itoa(i)
		noErr(t, f.Offers.Save(ctx, consolidatedOffer(t, id, want)))
		got, err := f.Offers.Get(ctx, offer.ID(id))
		noErr(t, err)
		eq(t, "tier", got.Tier(), shared.TierConsolidated)
		eq(t, "consolidationEligible", flagText(got.ConsolidationEligible()), flagText(want))
	}
	noErr(t, f.Offers.Save(ctx, NewOffer(t, "off-plain")))
	plain, err := f.Offers.Get(ctx, "off-plain")
	noErr(t, err)
	eq(t, "a classic offer carries none", flagText(plain.ConsolidationEligible()), "unknown")
}

func consolidatedMaking(eligible *bool, latest time.Duration) promise.Terms {
	return promise.Terms{
		Tier: shared.TierConsolidated, ShipBy: T0.Add(4 * time.Hour), Basis: shared.BasisScheduled, PolicyVersion: "v0", ConsolidationEligible: eligible,
		Window: shared.Window{Earliest: T0.Add(latest - 12*time.Hour), Latest: T0.Add(latest), TimeZone: "America/Chicago"},
	}
}

func consolidatedPromiseRoundTrip(t *testing.T, f Fixture) {
	w := shared.Witness{NodeID: "N-AUS3", PickupDepartureID: "dep-1800", Legs: []string{"dep-2200"}, DeliveryWaveID: "w-3"}
	p, _, err := promise.Make(promise.Making{
		ID: "prm-c", OfferID: "off-c", DestinationPrefix: "787", Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
		Terms: consolidatedMaking(flagPtr(true), 78*time.Hour), Witness: w, BookID: string(book.MakeID("N-AUS3", "dep-1800")), HoldTTL: 15 * time.Minute,
		Stock: map[string][]shared.OwnerStock{"ci-1001": {{OwnerRef: shared.OperatorOwned, Available: 2}}},
	}, T0)
	noErr(t, err)
	noErr(t, f.Promises.Save(ctx, p))
	got, err := f.Promises.Get(ctx, "prm-c")
	noErr(t, err)
	eq(t, "tier", got.Terms().Tier, shared.TierConsolidated)
	eq(t, "consolidationEligible", flagText(got.Terms().ConsolidationEligible), "true")
	for _, step := range []func(*promise.Promise) error{
		func(p *promise.Promise) error { _, err := p.Confirm("ord-c", 1, T0.Add(time.Minute)); return err },
		func(p *promise.Promise) error { _, err := p.FlagAtRisk("WAVE_CANCELED", T0.Add(time.Hour)); return err },
		func(p *promise.Promise) error {
			_, err := p.Revise(promise.Revising{Terms: consolidatedMaking(flagPtr(false), 90*time.Hour), Witness: w, BookID: p.BookID(), Reason: "WAVE_CANCELED"}, T0.Add(2*time.Hour))
			return err
		},
	} {
		cur, err := f.Promises.Get(ctx, "prm-c")
		noErr(t, err)
		noErr(t, step(cur))
		noErr(t, f.Promises.Save(ctx, cur))
	}
	got, err = f.Promises.Get(ctx, "prm-c")
	noErr(t, err)
	eq(t, "revised flag", flagText(got.Terms().ConsolidationEligible), "false")
	eq(t, "previous flag", flagText(got.Revisions()[0].PreviousTerms.ConsolidationEligible), "true")
}
