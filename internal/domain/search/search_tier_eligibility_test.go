package search

import (
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// ineligibleFor is a TierIneligible closure over (node, tier) pairs that also
// records every tier it was asked about.
func ineligibleFor(asked map[shared.Tier]bool, pairs ...[2]string) func(string, shared.Tier) bool {
	return func(node string, tier shared.Tier) bool {
		if asked != nil {
			asked[tier] = true
		}
		for _, p := range pairs {
			if p[0] == node && p[1] == string(tier) {
				return true
			}
		}
		return false
	}
}

func hasCandidates(g Group, tier shared.Tier) bool { return len(g.Candidates[tier]) > 0 }

// sameDayFixture is the same-day example: a SUB_SAME_DAY centre with a direct
// pickup to the station and a wave it can make.
func sameDayFixture() Input {
	in := fixture()
	n := in.Nodes["N-AUS3"]
	n.Subtype = SubtypeSubSameDay
	in = in.withNode(n)
	in.Now = at(22, 8, 0)
	in = in.withDeparture(Departure{ID: "dep-direct", LaneID: "lane-3", Kind: KindPickup, Origin: "N-AUS3", Dest: "N-DAU1", CutoffAt: at(22, 11, 0), ArrivesAt: at(22, 12, 0), Remaining: 10, Scheduled: true})
	return in.withWave(Wave{ID: "W-same", Station: "N-DAU1", InductCutoffAt: at(22, 13, 0), WindowStart: at(22, 15, 0), WindowEnd: at(22, 21, 0), Remaining: 10, Scheduled: true})
}

// secondCentre adds a plain fulfillment centre N-AUS4 that stocks the item and
// ships on the same timetable as N-AUS3.
func secondCentre(in Input) Input {
	aus3 := in.Nodes["N-AUS3"]
	aus4 := aus3
	aus4.ID = "N-AUS4"
	in = in.withNode(aus4).withDeparture(Departure{ID: "dep-AUS4-1800", LaneID: "lane-1", Kind: KindPickup, Origin: "N-AUS4", Dest: "N-SAU1", CutoffAt: at(22, 18, 0), ArrivesAt: at(22, 19, 15), Remaining: 40, Scheduled: true})
	stock := in.Stock
	in.Stock = func(node, item string) []shared.OwnerStock {
		if node == "N-AUS4" {
			node = "N-AUS3"
		}
		return stock(node, item)
	}
	return in
}

func TestTheUnfilteredDefaultKeepsEveryNode(t *testing.T) {
	g := onlyGroup(t, Run(fixture())) // TierIneligible is nil
	for _, tier := range []shared.Tier{shared.TierNextDay, shared.TierTwoDay, shared.TierStandard} {
		if !hasCandidates(g, tier) {
			t.Fatalf("%s lost its candidate without a filter", tier)
		}
	}
	sd := onlyGroup(t, Run(sameDayFixture()))
	if !hasCandidates(sd, shared.TierSameDay) {
		t.Fatal("SAME_DAY lost its candidate without a filter")
	}
}

func TestIneligibleNodeIsSkippedForNextDayButUsedForSlowerTiers(t *testing.T) {
	in := fixture()
	in.TierIneligible = ineligibleFor(nil, [2]string{"N-AUS3", "NEXT_DAY"})
	g := onlyGroup(t, Run(in))
	if hasCandidates(g, shared.TierNextDay) {
		t.Fatalf("an ineligible node must not be offered NEXT_DAY: %+v", g.Candidates[shared.TierNextDay])
	}
	if g.Unavailable[shared.TierNextDay] != DeliverySLAUnavailable {
		t.Fatalf("the tier is simply infeasible: constraint = %q", g.Unavailable[shared.TierNextDay])
	}
	for _, tier := range []shared.Tier{shared.TierTwoDay, shared.TierStandard} {
		if c := best(t, g, tier); c.NodeID != "N-AUS3" || c.Basis != shared.BasisScheduled {
			t.Fatalf("%s must still use the node: %+v", tier, c)
		}
	}
}

func TestIneligibleNodeIsSkippedForSameDay(t *testing.T) {
	in := sameDayFixture()
	in.TierIneligible = ineligibleFor(nil, [2]string{"N-AUS3", "SAME_DAY"})
	g := onlyGroup(t, Run(in))
	if hasCandidates(g, shared.TierSameDay) {
		t.Fatalf("an ineligible node must not be offered SAME_DAY: %+v", g.Candidates[shared.TierSameDay])
	}
	if !hasCandidates(g, shared.TierNextDay) {
		t.Fatal("eligibility is per tier: NEXT_DAY stays open for a node revoked only for SAME_DAY")
	}
}

func TestOnlyTheFastTiersAreEverAskedAbout(t *testing.T) {
	asked := map[shared.Tier]bool{}
	in := fixture()
	in.TierIneligible = ineligibleFor(asked)
	onlyGroup(t, Run(in))
	if asked[shared.TierTwoDay] || asked[shared.TierStandard] {
		t.Fatalf("slower tiers must never be filtered, asked = %v", asked)
	}
	if !asked[shared.TierNextDay] {
		t.Fatalf("NEXT_DAY must be asked, asked = %v", asked)
	}
}

func TestAnIneligibleNodeLeavesTheOtherCentreCompeting(t *testing.T) {
	in := secondCentre(fixture())
	both := onlyGroup(t, Run(in))
	if len(both.Candidates[shared.TierNextDay]) != 2 {
		t.Fatalf("both centres compete for NEXT_DAY: %+v", both.Candidates[shared.TierNextDay])
	}
	in.TierIneligible = ineligibleFor(nil, [2]string{"N-AUS3", "NEXT_DAY"})
	g := onlyGroup(t, Run(in))
	cs := g.Candidates[shared.TierNextDay]
	if len(cs) != 1 || cs[0].NodeID != "N-AUS4" {
		t.Fatalf("only N-AUS4 may remain for NEXT_DAY: %+v", cs)
	}
	if st := g.Candidates[shared.TierStandard]; len(st) != 2 {
		t.Fatalf("STANDARD keeps both centres: %+v", st)
	}
}

func TestFastTierIsOnlySameDayAndNextDay(t *testing.T) {
	want := map[shared.Tier]bool{shared.TierSameDay: true, shared.TierNextDay: true, shared.TierTwoDay: false, shared.TierStandard: false}
	for tier, w := range want {
		if FastTier(tier) != w {
			t.Errorf("FastTier(%s) = %v, want %v", tier, !w, w)
		}
	}
}
