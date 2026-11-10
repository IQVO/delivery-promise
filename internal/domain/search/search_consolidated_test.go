package search

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// consolidatedFixture is the worked example with one delivery wave per day from
// the 23rd to the 27th (induct cutoff 06:00, window 09:00-21:00 of that day), so
// the fastest STANDARD window ends on the 23rd and the days after it have a wave.
func consolidatedFixture(days ...int) Input {
	in := fixture()
	if len(days) == 0 {
		days = []int{24, 25, 26, 27}
	}
	for _, d := range days {
		in = in.withWave(dayWave(d))
	}
	in.Tiers = []shared.Tier{shared.TierConsolidated}
	return in
}

// dayWave is a wave of day d of October (or, past the 31st, of November) with
// an induct cutoff at 06:00 and the 09:00-21:00 window of that day.
func dayWave(d int) Wave {
	month := time.October
	if d > 31 {
		month, d = time.November, d-31
	}
	local := func(h int) time.Time { return time.Date(2026, month, d, h, 0, 0, 0, cdt).UTC() }
	return Wave{ID: "W-DAU1-" + local(0).Format("0102"), Station: "N-DAU1", InductCutoffAt: local(6), WindowStart: local(9), WindowEnd: local(21), Remaining: 320, Scheduled: true}
}

func consolidatedBest(t *testing.T, in Input) Candidate {
	t.Helper()
	return best(t, onlyGroup(t, Run(in)), shared.TierConsolidated)
}

func eligible(t *testing.T, c Candidate) bool {
	t.Helper()
	if c.ConsolidationEligible == nil {
		t.Fatalf("a CONSOLIDATED candidate carries consolidationEligible: %v", c.Window)
	}
	return *c.ConsolidationEligible
}

func TestWithoutADayTheChosenDayIsTwoLocalDaysAfterTheFastestWindow(t *testing.T) {
	c := consolidatedBest(t, consolidatedFixture())
	if c.Tier != shared.TierConsolidated || !c.Window.Latest.Equal(at(25, 21, 0)) || !c.Window.Earliest.Equal(at(25, 9, 0)) {
		t.Fatalf("the fastest window ends on the 23rd, so the chosen day is the 25th: %+v", c.Window)
	}
	if c.Window.TimeZone != "America/Chicago" || c.Basis != shared.BasisScheduled || c.Witness.DeliveryWaveID != "W-DAU1-1025" || c.Witness.PickupDepartureID != "dep-AUS3-1800" {
		t.Fatalf("the witness path ends on the wave of the chosen day: %+v", c)
	}
	if !eligible(t, c) {
		t.Fatal("two days after the fastest window leaves time to merge")
	}
}

func TestTheChosenDayIsClampedUpToTheFastestWindow(t *testing.T) {
	for _, day := range []string{"2026-10-20", "2026-10-22", "2026-10-23"} {
		in := consolidatedFixture()
		in.ConsolidatedDay = day
		c := consolidatedBest(t, in)
		if !c.Window.Latest.Equal(at(23, 21, 0)) {
			t.Errorf("day %s must clamp up to the 23rd: %v", day, c.Window.Latest)
		}
		if eligible(t, c) {
			t.Errorf("day %s: the fastest day leaves no hold", day)
		}
	}
}

func TestConsolidationEligibleNeedsOneLocalDayOfHold(t *testing.T) {
	for _, c := range []struct {
		day  string
		want bool
	}{{"2026-10-23", false}, {"2026-10-24", true}, {"2026-10-26", true}} {
		in := consolidatedFixture()
		in.ConsolidatedDay = c.day
		got := consolidatedBest(t, in)
		if eligible(t, got) != c.want {
			t.Errorf("day %s: consolidationEligible = %v, want %v", c.day, *got.ConsolidationEligible, c.want)
		}
	}
}

func TestADayBeyondTheHorizonIsUnavailable(t *testing.T) {
	in := consolidatedFixture(24, 36, 37) // 36 is November 5th (14 days), 37 the 6th (15 days)
	in.ConsolidatedDay = "2026-11-05"
	c := consolidatedBest(t, in)
	if !c.Window.Latest.Equal(time.Date(2026, time.November, 5, 21, 0, 0, 0, cdt).UTC()) {
		t.Fatalf("14 local days away is the last day: %v", c.Window.Latest)
	}
	in.ConsolidatedDay = "2026-11-06"
	if g := onlyGroup(t, Run(in)); g.Unavailable[shared.TierConsolidated] != DeliverySLAUnavailable || hasCandidates(g, shared.TierConsolidated) {
		t.Fatalf("15 days away is past the horizon: %+v", g.Unavailable)
	}
}

func TestADayWithoutAWaveAndWithoutANominalPathIsUnavailable(t *testing.T) {
	in := consolidatedFixture(24)
	in.Lanes = nil // no lane, so no NOMINAL fallback either
	in.ConsolidatedDay = "2026-10-27"
	if g := onlyGroup(t, Run(in)); g.Unavailable[shared.TierConsolidated] != DeliverySLAUnavailable {
		t.Fatalf("no wave ends on the chosen day: %+v", g.Unavailable)
	}
}

func TestNoStandardPathMeansNoConsolidatedOption(t *testing.T) {
	in := consolidatedFixture()
	in.Waves, in.Departures, in.Lanes = nil, nil, nil
	if g := onlyGroup(t, Run(in)); g.Unavailable[shared.TierConsolidated] != DeliverySLAUnavailable {
		t.Fatalf("without a fastest window there is nothing to hold: %+v", g.Unavailable)
	}
}

func TestFullBooksMeanCapacityExhaustedForConsolidated(t *testing.T) {
	in := consolidatedFixture()
	in.Lanes = nil
	full := BookView{Open: true, Room: 0, HoldDeadlineAt: at(22, 16, 25), Share: 10, Used: 10}
	in.Books = map[string]BookView{scheduledBook: full, nominalBook: full}
	if g := onlyGroup(t, Run(in)); g.Unavailable[shared.TierConsolidated] != NodeCapacityExhausted {
		t.Fatalf("the paths exist but the books are full: %+v", g.Unavailable)
	}
}

func TestConsolidatedFallsBackToANominalWindowOnTheChosenDay(t *testing.T) {
	in := consolidatedFixture()
	in.Waves, in.Departures = nil, nil
	c := consolidatedBest(t, in)
	if c.Basis != shared.BasisNominal || c.Witness.PickupDepartureID != "" || c.Witness.DeliveryWaveID != "" {
		t.Fatalf("a nominal candidate carries no departure or wave: %+v", c)
	}
	if !c.Window.Earliest.Equal(at(25, 9, 0)) || !c.Window.Latest.Equal(at(25, 21, 0)) || !eligible(t, c) {
		t.Fatalf("the nominal window is the chosen day 09:00-21:00: %+v", c.Window)
	}
	in.ConsolidatedDay = "2026-10-23"
	if c = consolidatedBest(t, in); !c.Window.Latest.Equal(at(23, 21, 0)) || eligible(t, c) {
		t.Fatalf("the fastest nominal day has no hold: %+v", c.Window)
	}
}

func TestTheLaterPickupWinsTheHold(t *testing.T) {
	in := consolidatedFixture().withDeparture(Departure{ID: "dep-AUS3-1830", LaneID: "lane-1", Kind: KindPickup, Origin: "N-AUS3", Dest: "N-SAU1",
		CutoffAt: at(22, 18, 30), ArrivesAt: at(22, 19, 45), Remaining: 40, Scheduled: true})
	g := onlyGroup(t, Run(in))
	if got := best(t, g, shared.TierConsolidated); got.Witness.PickupDepartureID != "dep-AUS3-1830" {
		t.Fatalf("the later pickup holds the group longest: %+v", got.Witness)
	}
	in.Tiers = []shared.Tier{shared.TierStandard}
	if got := best(t, onlyGroup(t, Run(in)), shared.TierStandard); got.Witness.PickupDepartureID != "dep-AUS3-1800" {
		t.Fatalf("other tiers rank as before: %+v", got.Witness)
	}
}

func TestAMinimumDayWalksForwardDayByDay(t *testing.T) {
	in := consolidatedFixture()
	in.Lanes = nil
	in.ConsolidatedNotBefore = at(25, 12, 0)
	cs := onlyGroup(t, Run(in)).Candidates[shared.TierConsolidated]
	if len(cs) != 3 || !cs[0].Window.Latest.Equal(at(25, 21, 0)) || !cs[1].Window.Latest.Equal(at(26, 21, 0)) || !cs[2].Window.Latest.Equal(at(27, 21, 0)) {
		t.Fatalf("one candidate per day from the 25th, got %d", len(cs))
	}
	in.ConsolidatedNotBefore = at(21, 12, 0)
	if first := consolidatedBest(t, in); !first.Window.Latest.Equal(at(23, 21, 0)) {
		t.Fatalf("a minimum before the fastest day clamps up to it: %v", first.Window)
	}
}

func TestConsolidatedIsOnlyAskedFor(t *testing.T) {
	in := consolidatedFixture()
	in.Tiers = nil
	g := onlyGroup(t, Run(in))
	if _, ok := g.Candidates[shared.TierConsolidated]; ok {
		t.Fatal("the default tier set never includes CONSOLIDATED")
	}
	if _, ok := g.Unavailable[shared.TierConsolidated]; ok {
		t.Fatal("the default tier set never reports CONSOLIDATED")
	}
	for _, tier := range shared.AllTiers() {
		for _, c := range g.Candidates[tier] {
			if c.ConsolidationEligible != nil {
				t.Fatalf("%s carries consolidationEligible", tier)
			}
		}
	}
}
