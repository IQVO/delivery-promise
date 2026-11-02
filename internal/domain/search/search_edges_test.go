package search

import (
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/calendar"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// nominalOnly strips the scheduled network so the tier answer can only come
// from the nominal path search.
func nominalOnly() Input {
	in := fixture()
	in.Waves, in.Departures = nil, nil
	return in
}

// TestNominalTransitIsTheShortestOpenPath pins the nominal transit itself: the
// direct lane is 500 minutes while AUS3 -> SAU1 (10) + SAU1 dwell (120) +
// SAU1 -> DAU1 (20) is 150 minutes, so the 21:00 cutoff arrives 23:30 the same
// day (window one day later) and the 23:00 cutoff arrives 01:30 the next day
// (window two days later). A transit that dropped the intermediate dwell,
// subtracted lane minutes instead of adding them, or kept the longer path
// moves one of the two windows by a day.
func TestNominalTransitIsTheShortestOpenPath(t *testing.T) {
	in := nominalOnly()
	in.Lanes = []Lane{
		{ID: "long", Origin: "N-AUS3", Dest: "N-DAU1", NominalMinutes: 500, Open: true},
		{ID: "s1", Origin: "N-AUS3", Dest: "N-SAU1", NominalMinutes: 10, Open: true},
		{ID: "s2", Origin: "N-SAU1", Dest: "N-DAU1", NominalMinutes: 20, Open: true},
	}
	aus3 := in.Nodes["N-AUS3"]
	aus3.Profile = fcProfile(95, calendar.Cutoff{Minute: 21 * 60}, calendar.Cutoff{Minute: 23 * 60})
	in = in.withNode(aus3)
	cs := onlyGroup(t, Run(in)).Candidates[shared.TierStandard]
	if len(cs) < 2 {
		t.Fatalf("want the 21:00 and 23:00 cutoffs, got %d candidates", len(cs))
	}
	if !cs[0].ShipBy.Equal(at(22, 21, 0)) || !cs[0].Window.Earliest.Equal(at(23, 9, 0)) || !cs[0].Window.Latest.Equal(at(23, 21, 0)) {
		t.Fatalf("21:00 cutoff (10+120+20 = 150 min, arrives 23:30): %+v", cs[0])
	}
	if !cs[1].ShipBy.Equal(at(22, 23, 0)) || !cs[1].Window.Earliest.Equal(at(24, 9, 0)) || !cs[1].Window.Latest.Equal(at(24, 21, 0)) {
		t.Fatalf("23:00 cutoff (arrives 01:30 next day): %+v", cs[1])
	}
	if cs[0].BookID != "N-AUS3:nominal-20261023T0200Z" || cs[1].BookID != "N-AUS3:nominal-20261023T0400Z" {
		t.Fatalf("books = %q %q", cs[0].BookID, cs[1].BookID)
	}
}

// TestNominalCandidateNamesTheRegion pins InRegion of a NOMINAL candidate both
// ways: the fixture node is in the area region, a node of another region is not.
func TestNominalCandidateNamesTheRegion(t *testing.T) {
	c := best(t, onlyGroup(t, Run(nominalOnly())), shared.TierStandard)
	if !c.InRegion {
		t.Fatalf("N-AUS3 is in R-SC: %+v", c)
	}
	far := nominalOnly()
	aus3 := far.Nodes["N-AUS3"]
	aus3.RegionID = "R-OTHER"
	far = far.withNode(aus3)
	if c := best(t, onlyGroup(t, Run(far)), shared.TierStandard); c.InRegion {
		t.Fatalf("a node of R-OTHER is not in region: %+v", c)
	}
}

// TestHandicapSkipPastEveryPickupStillAnswers: grade D skips two feasible
// pickups; with a single pickup left the node simply has nothing scheduled and
// STANDARD falls back to nominal — and the skip must stay in bounds.
func TestHandicapSkipPastEveryPickupStillAnswers(t *testing.T) {
	in := fixture()
	in.Grades = map[string]Grade{"N-AUS3": GradeD}
	g := onlyGroup(t, Run(in))
	if scheduledFits(g, shared.TierNextDay) {
		t.Fatalf("grade D skips the only pickup: %+v", g.Candidates[shared.TierNextDay])
	}
	if len(g.Candidates[shared.TierStandard]) == 0 {
		t.Fatalf("STANDARD still has the nominal fallback: %+v", g.Unavailable)
	}
}

// TestLastStopOnWaveAndBookIsStillOffered pins the exact-fit boundaries: a wave
// with one stop left is used, and so is a book with one room left.
func TestLastStopOnWaveAndBookIsStillOffered(t *testing.T) {
	in := fixture()
	in.Waves[0].Remaining = 1
	if len(onlyGroup(t, Run(in)).Candidates[shared.TierNextDay]) == 0 {
		t.Fatal("a wave with one stop left still carries the package")
	}
	in = fixture()
	in.Books = map[string]BookView{"N-AUS3:dep-AUS3-1800": {Open: true, Room: 1, HoldDeadlineAt: at(22, 16, 25)}}
	if c := best(t, onlyGroup(t, Run(in)), shared.TierNextDay); c.BookID != "N-AUS3:dep-AUS3-1800" {
		t.Fatalf("a book with one room left still holds: %+v", c)
	}
}

// tieHub is a pass-through sort centre with no dwell.
func tieHub(id string) Node {
	return Node{ID: id, Type: "SORTATION_CENTER", Active: true, Loc: cdt,
		Profile: &Profile{Schedule: calendar.Schedule{Loc: cdt, Hours: allDays(0, 24*60-1)}}}
}

// tieInput: the pickup reaches SAU1 at 19:15 (dwell 120, ready 21:15); from
// SAU1 two chains — SAU1->XA->DAU1 and SAU1->XB->DAU1 — arrive together at
// 23:00 with the same leg count. The first chain relaxes first (cutoff 21:30
// beats 21:31), so its legs are the witness.
func tieInput() Input {
	in := fixture()
	in = in.withNode(tieHub("N-XA")).withNode(tieHub("N-XB"))
	in.Departures = []Departure{
		in.Departures[0],
		{ID: "legA", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-SAU1", Dest: "N-XA", CutoffAt: at(22, 21, 30), ArrivesAt: at(22, 22, 0), Remaining: 9, Scheduled: true},
		{ID: "legX", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-XA", Dest: "N-DAU1", CutoffAt: at(22, 22, 30), ArrivesAt: at(22, 23, 0), Remaining: 9, Scheduled: true},
		{ID: "legB", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-SAU1", Dest: "N-XB", CutoffAt: at(22, 21, 31), ArrivesAt: at(22, 22, 0), Remaining: 9, Scheduled: true},
		{ID: "legY", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-XB", Dest: "N-DAU1", CutoffAt: at(22, 22, 30), ArrivesAt: at(22, 23, 0), Remaining: 9, Scheduled: true},
	}
	return in
}

func TestEqualArrivalEqualLegsKeepsTheFirstRelaxedPath(t *testing.T) {
	c := best(t, onlyGroup(t, Run(tieInput())), shared.TierNextDay)
	if got := c.Witness.Legs; len(got) != 2 || got[0] != "legA" || got[1] != "legX" || c.LegCount != 2 {
		t.Fatalf("the first relaxed chain wins the tie: %v", got)
	}
}

// TestALongerChainNeverDisplacesAShorterOneAtEqualArrival: SAU1->DAU1 direct
// arrives 23:00 with one leg; the four-leg chain SAU1->X1->X2->XB->DAU1 also
// arrives 23:00. The shorter path was relaxed first and must stay.
func TestALongerChainNeverDisplacesAShorterOneAtEqualArrival(t *testing.T) {
	in := fixture()
	in = in.withNode(tieHub("N-X1")).withNode(tieHub("N-X2")).withNode(tieHub("N-XB"))
	in.Departures = []Departure{
		in.Departures[0],
		{ID: "direct", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-SAU1", Dest: "N-DAU1", CutoffAt: at(22, 21, 30), ArrivesAt: at(22, 23, 0), Remaining: 9, Scheduled: true},
		{ID: "c1", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-SAU1", Dest: "N-X1", CutoffAt: at(22, 21, 30), ArrivesAt: at(22, 21, 45), Remaining: 9, Scheduled: true},
		{ID: "c2", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-X1", Dest: "N-X2", CutoffAt: at(22, 22, 0), ArrivesAt: at(22, 22, 15), Remaining: 9, Scheduled: true},
		{ID: "c3", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-X2", Dest: "N-XB", CutoffAt: at(22, 22, 30), ArrivesAt: at(22, 22, 45), Remaining: 9, Scheduled: true},
		{ID: "c4", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-XB", Dest: "N-DAU1", CutoffAt: at(22, 22, 45), ArrivesAt: at(22, 23, 0), Remaining: 9, Scheduled: true},
	}
	c := best(t, onlyGroup(t, Run(in)), shared.TierNextDay)
	if got := c.Witness.Legs; len(got) != 1 || got[0] != "direct" {
		t.Fatalf("the one-leg path stays the witness: %v", got)
	}
}
