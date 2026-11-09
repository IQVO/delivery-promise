package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

const tierEligibilityType = "com.retailnetwork.partner.node-performance.scorecard.NodeTierEligibilityChanged"

// changed feeds one NodeTierEligibilityChanged through the real consumer use case.
func (e *env) changed(node, tier string, eligible bool, changedAt time.Time) {
	e.t.Helper()
	must(e.t, e.svc.OnNodeTierEligibilityChanged(ctx, e.meta(tierEligibilityType),
		usecases.NodeTierEligibilityChanged{NodeID: node, Tier: tier, Eligible: &eligible, ChangedAt: changedAt}))
}

func hasOffer(q views.QuoteResult, tier string) bool {
	for _, o := range q.Offers {
		if o.Tier == tier {
			return true
		}
	}
	return false
}

func constraintOf(q views.QuoteResult, tier string) string {
	for _, u := range q.Unavailable {
		if u.Tier == tier {
			return u.Constraint
		}
	}
	return ""
}

func TestAnIneligibleNodeLosesNextDayButKeepsTheSlowerTiers(t *testing.T) {
	e := newEnv(t)
	before := e.quote()
	for _, tier := range []string{"NEXT_DAY", "TWO_DAY", "STANDARD"} {
		if !hasOffer(before, tier) {
			t.Fatalf("worked example lacks %s: %+v", tier, before)
		}
	}
	e.changed("N-AUS3", "NEXT_DAY", false, at(22, 14, 0))
	q := e.quote()
	if hasOffer(q, "NEXT_DAY") {
		t.Fatalf("an ineligible node must not be quoted NEXT_DAY: %+v", q.Offers)
	}
	eq(t, "NEXT_DAY is simply infeasible", constraintOf(q, "NEXT_DAY"), "DELIVERY_SLA_UNAVAILABLE")
	for _, tier := range []string{"TWO_DAY", "STANDARD"} {
		if !hasOffer(q, tier) {
			t.Fatalf("%s must still be quoted from the same node: %+v", tier, q.Offers)
		}
	}
}

func TestReinstatingTheNodeBringsTheFastTierBack(t *testing.T) {
	e := newEnv(t)
	e.changed("N-AUS3", "NEXT_DAY", false, at(22, 14, 0))
	if hasOffer(e.quote(), "NEXT_DAY") {
		t.Fatal("suspended")
	}
	e.changed("N-AUS3", "NEXT_DAY", true, at(22, 14, 1))
	if !hasOffer(e.quote(), "NEXT_DAY") {
		t.Fatal("reinstated node must be quoted NEXT_DAY again")
	}
}

func TestAStaleEventDoesNotRevokeOrReinstate(t *testing.T) {
	e := newEnv(t)
	e.changed("N-AUS3", "NEXT_DAY", false, at(22, 14, 0))
	e.changed("N-AUS3", "NEXT_DAY", true, at(22, 13, 0)) // older: dropped
	if hasOffer(e.quote(), "NEXT_DAY") {
		t.Fatal("an older reinstatement must not apply")
	}
	row, err := e.store.TierEligibility().Get(ctx, "N-AUS3", "NEXT_DAY")
	must(t, err)
	if row.Eligible || !row.ChangedAt.Equal(at(22, 14, 0)) {
		t.Fatalf("row = %+v", row)
	}
	e.changed("N-AUS3", "NEXT_DAY", true, at(22, 14, 0)) // same instant: applied
	if !hasOffer(e.quote(), "NEXT_DAY") {
		t.Fatal("an event with an equal changedAt applies")
	}
}

func TestEligibilityIsPerTierAndAnEstimateFollowsTheQuote(t *testing.T) {
	e := newEnv(t)
	e.changed("N-AUS3", "SAME_DAY", false, at(22, 14, 0)) // SAME_DAY is not offered here anyway
	if !hasOffer(e.quote(), "NEXT_DAY") {
		t.Fatal("a SAME_DAY revocation must not touch NEXT_DAY")
	}
	e.changed("N-AUS3", "NEXT_DAY", false, at(22, 14, 1))
	est := e.estimate(lines()).Estimates[0]
	for _, o := range est.Options {
		if o.Tier == "NEXT_DAY" {
			t.Fatalf("the estimate must filter like the quote: %+v", est.Options)
		}
	}
	optionOf(t, est, "STANDARD")
}

func TestAnUnfedReadModelFiltersNothing(t *testing.T) {
	e := newEnv(t) // no eligibility event at all: every node is eligible
	if !hasOffer(e.quote(), "NEXT_DAY") {
		t.Fatal("a missing row means ELIGIBLE")
	}
}

func TestAServiceWithoutTheReadModelFiltersNothing(t *testing.T) {
	e := newEnv(t)
	e.changed("N-AUS3", "NEXT_DAY", false, at(22, 14, 0))
	svc := usecases.New(usecases.Deps{
		UoW: e.store.UnitOfWork(), Offers: e.store.Offers(), Promises: e.store.Promises(), Books: e.store.Books(), Policies: e.store.Policies(),
		Network: e.store.Network(), Shipments: e.store.Shipments(), Outbox: e.store.Outbox(), Inbox: e.store.Inbox(), Clock: e.clock, IDs: e.ids,
	})
	q, err := svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{DestinationPostalPrefix: "787", Lines: lines(), Tiers: []shared.Tier{shared.TierNextDay}})
	must(t, err)
	if !hasOffer(q, "NEXT_DAY") {
		t.Fatal("a nil port never filters")
	}
}

func TestExistingPromisesAreNeverTouchedByARevocation(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	e.changed("N-AUS3", "NEXT_DAY", false, at(22, 14, 6))
	eq(t, "the revocation moves nothing", e.state(id), "CONFIRMED")
	eq(t, "no event", count(e.eventNames(), "PromiseAtRisk"), 0)
	// the pickup is cancelled: the promise is re-promised on the same NEXT_DAY
	// tier from the same (ineligible) node, because existing promises are kept
	e.departure("dep-AUS3-1730", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(22, 17, 30), at(22, 18, 45), 40)
	must(t, e.svc.OnDepartureCancelled(ctx, e.meta("DepartureCancelled"), usecases.DepartureCancelled{DepartureID: "dep-AUS3-1800"}))
	got, err := e.svc.GetPromise(ctx, id)
	must(t, err)
	eq(t, "revised in place", got.State, "CONFIRMED")
	eq(t, "tier kept", got.Terms.Tier, "NEXT_DAY")
	eq(t, "not unkeepable", count(e.eventNames(), "PromiseUnkeepable"), 0)
}

func TestExcludedCandidatesAreCountedOncePerNodeTierAndRequest(t *testing.T) {
	e := newEnv(t)
	e.changed("N-AUS3", "NEXT_DAY", false, at(22, 14, 0))
	m := &recordingMetrics{}
	svc := e.meteredService(m)
	cmd := usecases.QuoteCommand{DestinationPostalPrefix: "787", Lines: lines()}
	_, err := svc.QuoteDeliveryOffers(ctx, cmd)
	must(t, err)
	eq(t, "one node, one tier, one request", m.excluded["NEXT_DAY"], 1)
	eq(t, "only fast tiers are counted", len(m.excluded), 1)
	_, err = svc.QuoteDeliveryOffers(ctx, cmd)
	must(t, err)
	eq(t, "a second request counts again", m.excluded["NEXT_DAY"], 2)
	_, err = svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{DestinationPostalPrefix: "787", Lines: lines(), Tiers: []shared.Tier{shared.TierStandard}})
	must(t, err)
	eq(t, "a STANDARD-only request excludes nothing", m.excluded["NEXT_DAY"], 2)
}

func TestUnusableEventsAreIgnoredAndCountedNeverStored(t *testing.T) {
	e := newEnv(t)
	m := &recordingMetrics{}
	svc := e.meteredService(m)
	yes := true
	bad := []usecases.NodeTierEligibilityChanged{
		{NodeID: "N-AUS3", Tier: "TWO_DAY", Eligible: &yes, ChangedAt: at(22, 14, 0)},  // unknown tier
		{NodeID: "N-AUS3", Tier: "next_day", Eligible: &yes, ChangedAt: at(22, 14, 0)}, // unknown tier
		{NodeID: "", Tier: "NEXT_DAY", Eligible: &yes, ChangedAt: at(22, 14, 0)},       // missing nodeId
		{NodeID: "N-AUS3", Tier: "", Eligible: &yes, ChangedAt: at(22, 14, 0)},         // missing tier
		{NodeID: "N-AUS3", Tier: "NEXT_DAY", Eligible: nil, ChangedAt: at(22, 14, 0)},  // missing eligible
		{NodeID: "N-AUS3", Tier: "NEXT_DAY", Eligible: &yes, ChangedAt: time.Time{}},   // missing changedAt
	}
	for _, ev := range bad {
		must(t, svc.OnNodeTierEligibilityChanged(ctx, e.meta(tierEligibilityType), ev))
	}
	eq(t, "every unusable event is counted", len(m.ignored), len(bad))
	eq(t, "the unknown tiers", m.ignored[0]+","+m.ignored[1], "unknown-tier,unknown-tier")
	eq(t, "the missing fields", m.ignored[2]+m.ignored[3]+m.ignored[4]+m.ignored[5], "missing-fieldmissing-fieldmissing-fieldmissing-field")
	if _, err := e.store.TierEligibility().Get(ctx, "N-AUS3", "NEXT_DAY"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("nothing may be stored, got %v", err)
	}
}

func TestAReplayedEventIdChangesNothingEvenWhenTheStateMovedOn(t *testing.T) {
	e := newEnv(t)
	no := false
	revoke := e.meta(tierEligibilityType)
	must(t, e.svc.OnNodeTierEligibilityChanged(ctx, revoke, usecases.NodeTierEligibilityChanged{NodeID: "N-AUS3", Tier: "NEXT_DAY", Eligible: &no, ChangedAt: at(22, 14, 0)}))
	e.changed("N-AUS3", "NEXT_DAY", true, at(22, 14, 5))
	// the revocation is redelivered with its own id: the inbox drops it
	must(t, e.svc.OnNodeTierEligibilityChanged(ctx, revoke, usecases.NodeTierEligibilityChanged{NodeID: "N-AUS3", Tier: "NEXT_DAY", Eligible: &no, ChangedAt: at(22, 14, 9)}))
	row, err := e.store.TierEligibility().Get(ctx, "N-AUS3", "NEXT_DAY")
	must(t, err)
	if !row.Eligible || !row.ChangedAt.Equal(at(22, 14, 5)) {
		t.Fatalf("row = %+v", row)
	}
}
