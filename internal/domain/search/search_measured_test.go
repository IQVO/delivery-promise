package search

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// measuredFixture is the worked example with the departures' departsAt set, so
// every departure has a scheduled transit to measure against (ADR 0014): the
// pickup runs 60 minutes and the linehaul 75.
func measuredFixture() Input {
	in := fixture()
	in.Departures = []Departure{
		{ID: "dep-AUS3-1800", LaneID: "lane-1", Kind: KindPickup, Origin: "N-AUS3", Dest: "N-SAU1", CutoffAt: at(22, 18, 0),
			DepartsAt: at(22, 18, 15), ArrivesAt: at(22, 19, 15), Remaining: 40, Scheduled: true},
		{ID: "dep-SAU1-DAU1-2200", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-SAU1", Dest: "N-DAU1", CutoffAt: at(22, 22, 0),
			DepartsAt: at(22, 22, 15), ArrivesAt: at(22, 23, 30), Remaining: 140, Scheduled: true},
	}
	return in
}

func measuring(in Input, minutes map[[2]string]int) Input {
	in.MeasuredTransit = func(origin, dest string) (bool, int) {
		if m, ok := minutes[[2]string{origin, dest}]; ok {
			return true, m
		}
		return false, 0
	}
	return in
}

func TestMeasuredTransitQuotesMEASUREDOnTheSameWave(t *testing.T) {
	// the linehaul usually takes 135 minutes, an hour more than scheduled
	r := Run(measuring(measuredFixture(), map[[2]string]int{{"N-SAU1", "N-DAU1"}: 135}))
	c := best(t, onlyGroup(t, r), shared.TierNextDay)
	if c.Basis != shared.BasisMeasured {
		t.Fatalf("basis = %s, want MEASURED", c.Basis)
	}
	// 23:30 + 60 minutes is still before the wave's 06:00 induct cutoff: the
	// window keeps the timetable's wave, only later than scheduled
	assertWorkedWindow(t, c.Window)
	if c.Witness.DeliveryWaveID != "W-DAU1-1023-A" {
		t.Fatalf("wave = %s", c.Witness.DeliveryWaveID)
	}
	if err := c.Witness.Validate(c.Basis); err != nil {
		t.Fatalf("a MEASURED witness must validate like a SCHEDULED one: %v", err)
	}
}

func TestMeasuredTransitNeverPromisesEarlier(t *testing.T) {
	// the linehaul usually takes 30 minutes, less than the 75 scheduled: the
	// timetable stands and the basis stays SCHEDULED
	r := Run(measuring(measuredFixture(), map[[2]string]int{{"N-SAU1", "N-DAU1"}: 30}))
	c := best(t, onlyGroup(t, r), shared.TierNextDay)
	if c.Basis != shared.BasisScheduled || !c.Window.Latest.Equal(at(23, 21, 0)) {
		t.Fatalf("an early lane must keep the timetable: %+v", c)
	}
}

func TestMeasuredTransitBelowMinObservationsKeepsSCHEDULED(t *testing.T) {
	in := measuredFixture()
	in.MeasuredTransit = func(string, string) (bool, int) { return false, 0 } // never enough observations
	c := best(t, onlyGroup(t, Run(in)), shared.TierNextDay)
	if c.Basis != shared.BasisScheduled {
		t.Fatalf("basis = %s, want SCHEDULED below minObservations", c.Basis)
	}
}

func TestAMeasuredPickupLegPropagatesToTheCandidate(t *testing.T) {
	// the pickup usually takes 90 minutes instead of 60: it still makes the
	// linehaul's cutoff, the arrival stands, but the path is measured
	r := Run(measuring(measuredFixture(), map[[2]string]int{{"N-AUS3", "N-SAU1"}: 90}))
	c := best(t, onlyGroup(t, r), shared.TierNextDay)
	if c.Basis != shared.BasisMeasured {
		t.Fatalf("basis = %s, want MEASURED from the pickup leg", c.Basis)
	}
	assertWorkedWindow(t, c.Window)
}

func TestMeasuredTransitPushesTheWaveAndCanFlipATier(t *testing.T) {
	// 480 measured minutes push the linehaul's arrival to 06:15, past wave A's
	// 06:00 induct cutoff: the next wave, on the day after, is the first
	// feasible one, which is beyond NEXT_DAY
	in := measuring(measuredFixture(), map[[2]string]int{{"N-SAU1", "N-DAU1"}: 480})
	in = in.withWave(Wave{ID: "W-DAU1-1024-B", Station: "N-DAU1", InductCutoffAt: at(23, 20, 0), WindowStart: at(24, 9, 0),
		WindowEnd: at(24, 21, 0), Remaining: 320, Scheduled: true})
	g := onlyGroup(t, Run(in))
	if cs := g.Candidates[shared.TierNextDay]; len(cs) != 0 {
		t.Fatalf("NEXT_DAY must be infeasible, got %d candidates", len(cs))
	}
	if g.Unavailable[shared.TierNextDay] != DeliverySLAUnavailable {
		t.Fatalf("NEXT_DAY constraint = %q", g.Unavailable[shared.TierNextDay])
	}
	c := best(t, g, shared.TierTwoDay)
	if c.Basis != shared.BasisMeasured || !c.Window.Earliest.Equal(at(24, 9, 0)) || !c.Window.Latest.Equal(at(24, 21, 0)) {
		t.Fatalf("TWO_DAY must ride the later wave measured: %+v", c)
	}
	if c.Witness.DeliveryWaveID != "W-DAU1-1024-B" {
		t.Fatalf("wave = %s", c.Witness.DeliveryWaveID)
	}
}

func TestAMeasuredPickupThatMissesTheLinehaulLosesTheCandidate(t *testing.T) {
	// 180 measured pickup minutes push the sort's ready instant past the
	// linehaul's cutoff: no scheduled path is left
	in := measuring(measuredFixture(), map[[2]string]int{{"N-AUS3", "N-SAU1"}: 180})
	g := onlyGroup(t, Run(in))
	if cs := g.Candidates[shared.TierNextDay]; len(cs) != 0 {
		t.Fatalf("the missed linehaul must leave no NEXT_DAY candidate, got %+v", cs)
	}
}

func TestMeasuredTransitLeavesTheNominalFallbackNOMINAL(t *testing.T) {
	// a STANDARD quote with no scheduled path falls back to nominal lane times
	// whatever the lanes measure (ADR 0014: no early shortening, and the
	// nominal path has no departures to adjust)
	in := measuring(measuredFixture(), map[[2]string]int{{"N-AUS3", "N-SAU1"}: 90, {"N-SAU1", "N-DAU1"}: 480})
	in.Departures = nil
	in.OnlyNode = "N-AUS3"
	c := best(t, onlyGroup(t, Run(in)), shared.TierStandard)
	if c.Basis != shared.BasisNominal {
		t.Fatalf("basis = %s, want NOMINAL", c.Basis)
	}
	if c.Window.Latest.Before(in.Now.Add(24 * time.Hour)) {
		t.Fatalf("nominal window = %+v", c.Window)
	}
}
