package usecases_test

import (
	"errors"
	"testing"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// consolidatedEnv is the worked example with a delivery wave on each of the
// 24th to the 27th: the fastest window ends on the 23rd.
func consolidatedEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	for d := 24; d <= 27; d++ {
		e.wave("W-DAU1-10"+itoa(d), at(d, 6, 0), at(d, 9, 0), at(d, 21, 0), 320)
	}
	return e
}

func itoa(d int) string { return string(rune('0'+d/10)) + string(rune('0'+d%10)) }

func (e *env) quoteDay(day string, tiers ...shared.Tier) views.QuoteResult {
	e.t.Helper()
	res, err := e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		DestinationPostalPrefix: "787", Lines: lines(), Tiers: tiers, ConsolidatedDeliveryDay: day,
	})
	must(e.t, err)
	return res
}

func tiersOf(q views.QuoteResult) []string {
	var out []string
	for _, o := range q.Offers {
		out = append(out, o.Tier)
	}
	return out
}

func TestADefaultQuoteNeverOffersConsolidated(t *testing.T) {
	e := consolidatedEnv(t)
	q := e.quote()
	eq(t, "tiers", len(q.Offers), 3)
	for _, o := range q.Offers {
		if o.Tier == "CONSOLIDATED" || o.ConsolidationEligible != nil {
			t.Fatalf("a default quote carries no CONSOLIDATED option nor flag: %+v", o)
		}
	}
	for _, u := range q.Unavailable {
		if u.Tier == "CONSOLIDATED" {
			t.Fatalf("a default quote does not report CONSOLIDATED: %+v", u)
		}
	}
}

func TestAChosenDayAddsTheConsolidatedOfferToTheClassicTiers(t *testing.T) {
	e := consolidatedEnv(t)
	q := e.quoteDay("2026-10-26")
	o := offerOf(t, q, "CONSOLIDATED")
	if o.Window.Latest != at(26, 21, 0) || o.Window.TimeZone != "America/Chicago" || o.ConsolidationEligible == nil || !*o.ConsolidationEligible {
		t.Fatalf("the offer ends on the chosen day with room to merge: %+v", o)
	}
	eq(t, "the classic tiers stay", len(q.Offers), 4)
	eq(t, "the offer is stored", e.store.Counts().Offers, 4)
	stored, err := e.svc.GetOffer(ctx, o.OfferID)
	must(t, err)
	if stored.Tier != "CONSOLIDATED" || stored.ConsolidationEligible == nil || !*stored.ConsolidationEligible {
		t.Fatalf("the stored offer keeps the flag: %+v", stored)
	}
}

func TestADayAddsConsolidatedToTheTiersNamed(t *testing.T) {
	e := consolidatedEnv(t)
	got := tiersOf(e.quoteDay("2026-10-26", shared.TierStandard))
	eq(t, "tiers", len(got), 2)
	eq(t, "first", got[0], "STANDARD")
	eq(t, "second", got[1], "CONSOLIDATED")
}

func TestListingConsolidatedWithoutADayUsesTheDefaultDay(t *testing.T) {
	e := consolidatedEnv(t)
	o := offerOf(t, e.quote(shared.TierConsolidated), "CONSOLIDATED")
	if o.Window.Latest != at(25, 21, 0) || o.ConsolidationEligible == nil || !*o.ConsolidationEligible {
		t.Fatalf("two days after the fastest window: %+v", o.Window)
	}
}

func TestTheFastestDayIsQuotableButNotEligible(t *testing.T) {
	e := consolidatedEnv(t)
	o := offerOf(t, e.quoteDay("2026-10-21", shared.TierConsolidated), "CONSOLIDATED")
	if o.Window.Latest != at(23, 21, 0) || o.ConsolidationEligible == nil || *o.ConsolidationEligible {
		t.Fatalf("a day before the fastest window clamps up to it and leaves no hold: %+v", o)
	}
}

func TestAWaveFourteenDaysOutIsFoundBeyondTheClassicHorizon(t *testing.T) {
	e := newEnv(t)
	e.wave("W-DAU1-1105", at(36, 6, 0), at(36, 9, 0), at(36, 21, 0), 320)
	o := offerOf(t, e.quoteDay("2026-11-05"), "CONSOLIDATED")
	if o.Window.Latest != at(36, 21, 0) {
		t.Fatalf("the 14th local day: %+v", o.Window)
	}
}

func TestAnInvalidDayOrAnEstimateOfConsolidatedIsRefused(t *testing.T) {
	e := consolidatedEnv(t)
	for _, day := range []string{"26/10/2026", "2026-13-01", "tomorrow"} {
		_, err := e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{DestinationPostalPrefix: "787", Lines: lines(), ConsolidatedDeliveryDay: day})
		if !errors.Is(err, shared.ErrValidation) {
			t.Errorf("day %q: err = %v, want ErrValidation", day, err)
		}
	}
	_, err := e.svc.EstimateDeliveries(ctx, usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: lines(), Tiers: []shared.Tier{shared.TierConsolidated}})
	if !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("an estimate of CONSOLIDATED: err = %v, want ErrValidation", err)
	}
	eq(t, "nothing was stored", e.store.Counts().Offers, 0)
}

func TestThePromiseAndTheEventsCarryConsolidationEligible(t *testing.T) {
	e := consolidatedEnv(t)
	o := offerOf(t, e.quoteDay("2026-10-26", shared.TierConsolidated), "CONSOLIDATED")
	res := e.make(o.OfferID)
	terms := res.Promise.Terms
	if terms.Tier != "CONSOLIDATED" || terms.ConsolidationEligible == nil || !*terms.ConsolidationEligible {
		t.Fatalf("the promise copies the flag: %+v", terms)
	}
	var quoted, made int
	for _, ev := range e.store.Outbox().Events() {
		switch x := ev.(type) {
		case offer.DeliveryOfferQuoted:
			quoted++
			eq(t, "quoted flag", x.ConsolidationEligible != nil && *x.ConsolidationEligible, true)
		case promise.PromiseMade:
			made++
			eq(t, "made flag", x.Terms.ConsolidationEligible != nil && *x.Terms.ConsolidationEligible, true)
		}
	}
	eq(t, "events", quoted+made, 2)
}

func TestACancelledWaveRePromisesAConsolidatedPromiseOnTheSameDayNominally(t *testing.T) {
	e := consolidatedEnv(t)
	o := offerOf(t, e.quoteDay("2026-10-25", shared.TierConsolidated), "CONSOLIDATED")
	p := e.make(o.OfferID).Promise
	_, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-1", 1)
	must(t, err)
	must(t, e.svc.OnWaveCancelled(ctx, e.meta("DeliveryWaveCancelled"), usecases.WaveCancelled{WaveID: "W-DAU1-1025"}))
	got, err := e.svc.GetPromise(ctx, p.PromiseID)
	must(t, err)
	if got.State != "CONFIRMED" || len(got.Revisions) != 1 || got.Terms.Tier != "CONSOLIDATED" || got.Terms.Basis != "NOMINAL" {
		t.Fatalf("re-promised on the spot, still CONSOLIDATED, on the nominal fallback: %+v", got)
	}
	if got.Terms.DeliverByLatest != at(25, 21, 0) || got.Terms.ConsolidationEligible == nil || !*got.Terms.ConsolidationEligible {
		t.Fatalf("the chosen day is kept and never moves earlier: %+v", got.Terms)
	}
}

func TestAConsolidatedPromiseWithNoPathBecomesUnkeepableNotStandard(t *testing.T) {
	e := consolidatedEnv(t)
	o := offerOf(t, e.quoteDay("2026-10-25", shared.TierConsolidated), "CONSOLIDATED")
	p := e.make(o.OfferID).Promise
	_, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-1", 1)
	must(t, err)
	must(t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeSuspended"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, readmodel.NodeSuspended))
	must(t, e.svc.OnWaveCancelled(ctx, e.meta("DeliveryWaveCancelled"), usecases.WaveCancelled{WaveID: "W-DAU1-1025"}))
	got, err := e.svc.GetPromise(ctx, p.PromiseID)
	must(t, err)
	eq(t, "stays AT_RISK", got.State, "AT_RISK")
	eq(t, "still CONSOLIDATED", got.Terms.Tier, "CONSOLIDATED")
	eq(t, "unkeepable raised", count(e.eventNames(), "PromiseUnkeepable"), 1)
}
