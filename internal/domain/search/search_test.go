package search

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/calendar"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func onlyGroup(t *testing.T, r Result) Group {
	t.Helper()
	if len(r.Groups) != 1 {
		t.Fatalf("want one group, got %d (whole quote %v)", len(r.Groups), r.WholeQuote)
	}
	return r.Groups[0]
}

func best(t *testing.T, g Group, tier shared.Tier) Candidate {
	t.Helper()
	cs := g.Candidates[tier]
	if len(cs) == 0 {
		t.Fatalf("no candidate for %s (constraint %q)", tier, g.Unavailable[tier])
	}
	return cs[0]
}

// assertWorkedWindow checks the worked example's window: the station's next
// local day 09:00-21:00, naming the station's zone (ADR 0011).
func assertWorkedWindow(t *testing.T, w shared.Window) {
	t.Helper()
	if !w.Earliest.Equal(at(23, 9, 0)) || !w.Latest.Equal(at(23, 21, 0)) || w.TimeZone != "America/Chicago" {
		t.Fatalf("window = %+v", w)
	}
}

func assertWorkedExample(t *testing.T, c Candidate) {
	t.Helper()
	if c.NodeID != "N-AUS3" || c.Basis != shared.BasisScheduled || !c.ShipBy.Equal(at(22, 18, 0)) {
		t.Fatalf("node/basis/shipBy wrong: %+v", c)
	}
	assertWorkedWindow(t, c.Window)
	w := c.Witness
	if w.NodeID != "N-AUS3" || w.PickupDepartureID != "dep-AUS3-1800" || len(w.Legs) != 1 || w.Legs[0] != "dep-SAU1-DAU1-2200" || w.DeliveryWaveID != "W-DAU1-1023-A" {
		t.Fatalf("witness = %+v", w)
	}
	if c.BookID != "N-AUS3:dep-AUS3-1800" || c.LegCount != 1 || !c.InRegion || c.Direct {
		t.Fatalf("book/legs/region wrong: %+v", c)
	}
}

func TestWorkedExampleOfThePromiseModel(t *testing.T) {
	r := Run(fixture())
	g := onlyGroup(t, r)
	c := best(t, g, shared.TierNextDay)
	assertWorkedExample(t, c)
	// TTL 10 minutes (14:15) is earlier than the 16:25 the cutoff would allow.
	if !c.ExpiresAt.Equal(at(22, 14, 15)) || !c.LatestMeetableAt.Equal(at(22, 16, 25)) {
		t.Fatalf("expires %v meet %v", c.ExpiresAt, c.LatestMeetableAt)
	}
	for _, tier := range []shared.Tier{shared.TierTwoDay, shared.TierStandard} {
		if got := best(t, g, tier); got.Tier != tier || got.Basis != shared.BasisScheduled || !got.Window.Latest.Equal(at(23, 21, 0)) {
			t.Errorf("%s: %+v", tier, got)
		}
	}
	if g.Unavailable[shared.TierSameDay] != DeliverySLAUnavailable || len(g.Candidates[shared.TierSameDay]) != 0 {
		t.Fatalf("SAME_DAY: %v", g.Unavailable)
	}
	if g.No != 1 || len(g.Lines) != 1 || len(r.WholeQuote) != 0 {
		t.Fatalf("group = %+v whole = %v", g, r.WholeQuote)
	}
}

func TestExpiryIsCappedByTheMeetableCutoff(t *testing.T) {
	in := fixture()
	in.Now = at(22, 16, 20) // ready 17:55, still before the 18:00 cutoff; 16:25 is the latest meetable instant
	g := onlyGroup(t, Run(in))
	c := best(t, g, shared.TierNextDay)
	if !c.ExpiresAt.Equal(at(22, 16, 25)) {
		t.Fatalf("expiresAt = %v, want 16:25", c.ExpiresAt)
	}
	in.Now = at(22, 16, 26) // ready 18:01: the 18:00 pickup is missed
	g = onlyGroup(t, Run(in))
	if len(g.Candidates[shared.TierNextDay]) != 0 {
		t.Fatalf("the missed pickup must not be offered: %+v", g.Candidates[shared.TierNextDay])
	}
}

func TestNoServiceAreaMeansDestinationNotServed(t *testing.T) {
	in := fixture()
	in.Station = nil
	r := Run(in)
	if len(r.Groups) != 0 || len(r.WholeQuote) != 4 {
		t.Fatalf("result = %+v", r)
	}
	for _, tier := range shared.AllTiers() {
		if r.WholeQuote[tier] != DestinationNotServed {
			t.Errorf("%s: %q", tier, r.WholeQuote[tier])
		}
	}
	in.Tiers = []shared.Tier{shared.TierStandard, shared.TierNextDay, shared.TierNextDay}
	if r := Run(in); len(r.WholeQuote) != 2 {
		t.Fatalf("requested tiers are deduplicated: %v", r.WholeQuote)
	}
}

func TestItemQuantityNotAvailable(t *testing.T) {
	in := fixture().stock("N-AUS3", "ci-1001", 1)
	r := Run(in)
	if len(r.Groups) != 0 {
		t.Fatalf("groups = %+v", r.Groups)
	}
	for _, tier := range shared.AllTiers() {
		if r.WholeQuote[tier] != ItemQuantityNotAvailable {
			t.Errorf("%s: %q", tier, r.WholeQuote[tier])
		}
	}
	// exactly the quantity is available
	if g := onlyGroup(t, Run(fixture().stock("N-AUS3", "ci-1001", 2))); len(g.Candidates[shared.TierNextDay]) == 0 {
		t.Fatal("2 of 2 is enough")
	}
	// an inactive node, or one without a profile, or a non-fulfillment node, is not a candidate
	inactive := fixture().withNode(Node{ID: "N-AUS3", Type: TypeFulfillmentCenter, Active: false, Loc: cdt, Profile: fcProfile(95)})
	noProfile := fixture().withNode(Node{ID: "N-AUS3", Type: TypeFulfillmentCenter, Active: true, Loc: cdt})
	wrongType := fixture().withNode(Node{ID: "N-AUS3", Type: "SORTATION_CENTER", Active: true, Loc: cdt, Profile: fcProfile(95)})
	for name, in := range map[string]Input{"inactive": inactive, "no profile": noProfile, "wrong type": wrongType} {
		if r := Run(in); len(r.Groups) != 0 || r.WholeQuote[shared.TierNextDay] != ItemQuantityNotAvailable {
			t.Errorf("%s: %+v", name, r)
		}
	}
}

func TestPastOrderDeadline(t *testing.T) {
	pol, _, err := policy.Publish(0, policy.Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100, Deadlines: []policy.Deadline{
		{Tier: shared.TierNextDay, RegionID: "R-SC", Minute: 14 * 60},
		{Tier: shared.TierStandard, Minute: 23*60 + 59},
	}}, at(1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	in := fixture()
	in.Policy = pol
	r := Run(in) // 14:05 local is past 14:00
	if r.WholeQuote[shared.TierNextDay] != PastOrderDeadline || len(r.WholeQuote) != 1 {
		t.Fatalf("whole quote = %v", r.WholeQuote)
	}
	g := onlyGroup(t, r)
	if len(g.Candidates[shared.TierNextDay]) != 0 || len(g.Candidates[shared.TierTwoDay]) == 0 || len(g.Candidates[shared.TierStandard]) == 0 {
		t.Fatalf("other tiers are still searched: %+v", g.Candidates)
	}
	in.Now = at(22, 14, 0) // exactly at the deadline is not past
	if r := Run(in); len(r.WholeQuote) != 0 {
		t.Fatalf("at the deadline: %v", r.WholeQuote)
	}
	in.Tiers = []shared.Tier{shared.TierNextDay}
	in.Now = at(22, 14, 5)
	if r := Run(in); len(r.Groups) != 0 || r.WholeQuote[shared.TierNextDay] != PastOrderDeadline {
		t.Fatalf("every requested tier past its deadline: %+v", r)
	}
}

func TestCapacityExhaustedVersusNoPath(t *testing.T) {
	cases := map[string]func(*Input){
		"book full": func(in *Input) {
			in.Books = map[string]BookView{"N-AUS3:dep-AUS3-1800": {Open: true, Room: 0, HoldDeadlineAt: at(22, 16, 25)}}
		},
		"book closed": func(in *Input) {
			in.Books = map[string]BookView{"N-AUS3:dep-AUS3-1800": {Open: false, Room: 5, HoldDeadlineAt: at(22, 16, 25)}}
		},
		"book past its hold deadline": func(in *Input) {
			in.Books = map[string]BookView{"N-AUS3:dep-AUS3-1800": {Open: true, Room: 5, HoldDeadlineAt: at(22, 14, 5)}}
		},
		"pickup full": func(in *Input) { in.Departures[0].Remaining = 0 },
		"leg full":    func(in *Input) { in.Departures[1].Remaining = 0 },
		"wave full":   func(in *Input) { in.Waves[0].Remaining = 0 },
	}
	for name, mut := range cases {
		in := fixture()
		in.Departures = append([]Departure{}, in.Departures...)
		in.Waves = append([]Wave{}, in.Waves...)
		mut(&in)
		g := onlyGroup(t, Run(in))
		for _, tier := range []shared.Tier{shared.TierNextDay, shared.TierTwoDay} {
			if g.Unavailable[tier] != NodeCapacityExhausted {
				t.Errorf("%s/%s: %q, want NODE_CAPACITY_EXHAUSTED", name, tier, g.Unavailable[tier])
			}
		}
	}
	// no path at all (no wave) is DELIVERY_SLA_UNAVAILABLE, not a capacity problem
	in := fixture()
	in.Waves = nil
	g := onlyGroup(t, Run(in))
	if g.Unavailable[shared.TierNextDay] != DeliverySLAUnavailable {
		t.Fatalf("no wave: %q", g.Unavailable[shared.TierNextDay])
	}
}

func TestPackagesPerOrderNeedRoomOnEveryDeparture(t *testing.T) {
	pol, _, _ := policy.Publish(0, policy.Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 41, SharePercent: 100}, at(1, 0, 0))
	in := fixture()
	in.Policy = pol // the pickup has 40 packages left
	if g := onlyGroup(t, Run(in)); g.Unavailable[shared.TierNextDay] != NodeCapacityExhausted {
		t.Fatalf("41 packages do not fit 40: %v", g.Unavailable)
	}
	pol40, _, _ := policy.Publish(0, policy.Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 40, SharePercent: 100}, at(1, 0, 0))
	in.Policy = pol40
	if g := onlyGroup(t, Run(in)); len(g.Candidates[shared.TierNextDay]) == 0 {
		t.Fatal("40 packages fit 40")
	}
}

func TestNominalFallbackIsStandardOnly(t *testing.T) {
	in := fixture()
	in.Waves = nil
	in.Departures = nil
	g := onlyGroup(t, Run(in))
	c := best(t, g, shared.TierStandard)
	if c.Basis != shared.BasisNominal || !c.ShipBy.Equal(at(22, 18, 0)) || c.Witness.PickupDepartureID != "" || c.Witness.DeliveryWaveID != "" || len(c.Witness.Legs) != 0 || c.Witness.NodeID != "N-AUS3" {
		t.Fatalf("nominal candidate = %+v", c)
	}
	// 18:00 + 75 min lane + 120 min dwell + 90 min lane = 22:45, so the next local day 09:00-21:00
	// of the station's calendar, which the window names (ADR 0011)
	assertWorkedWindow(t, c.Window)
	if c.BookID != "N-AUS3:nominal-20261022T2300Z" || c.Opening.PickupDepartureID != "nominal-20261022T2300Z" || c.Opening.Share != 2500 {
		t.Fatalf("book = %q opening %+v", c.BookID, c.Opening)
	}
	for _, tier := range []shared.Tier{shared.TierSameDay, shared.TierNextDay, shared.TierTwoDay} {
		if g.Unavailable[tier] != DeliverySLAUnavailable {
			t.Errorf("%s must never be nominal: %q", tier, g.Unavailable[tier])
		}
	}
}

func TestNominalNeedsAPathAndAWindowWithinSevenDays(t *testing.T) {
	noLane := fixture()
	noLane.Waves, noLane.Departures = nil, nil
	noLane.Lanes = []Lane{{ID: "lane-1", Origin: "N-AUS3", Dest: "N-SAU1", NominalMinutes: 75, Open: true}}
	if g := onlyGroup(t, Run(noLane)); g.Unavailable[shared.TierStandard] != DeliverySLAUnavailable {
		t.Fatalf("no lane to the station: %v", g.Unavailable)
	}
	closed := fixture()
	closed.Waves, closed.Departures = nil, nil
	closed.Lanes = []Lane{{ID: "lane-1", Origin: "N-AUS3", Dest: "N-SAU1", NominalMinutes: 75, Open: true}, {ID: "lane-2", Origin: "N-SAU1", Dest: "N-DAU1", NominalMinutes: 90, Open: false}}
	if g := onlyGroup(t, Run(closed)); g.Unavailable[shared.TierStandard] != DeliverySLAUnavailable {
		t.Fatalf("a closed lane is not a path: %v", g.Unavailable)
	}
	slow := fixture()
	slow.Waves, slow.Departures = nil, nil
	slow.Lanes = []Lane{{ID: "lane-1", Origin: "N-AUS3", Dest: "N-DAU1", NominalMinutes: 8 * 24 * 60, Open: true}}
	if g := onlyGroup(t, Run(slow)); g.Unavailable[shared.TierStandard] != DeliverySLAUnavailable {
		t.Fatalf("8 days is past STANDARD: %v", g.Unavailable)
	}
	direct := fixture()
	direct.Waves, direct.Departures = nil, nil
	direct.Lanes = []Lane{{ID: "lane-1", Origin: "N-AUS3", Dest: "N-DAU1", NominalMinutes: 60, Open: true}}
	c := best(t, onlyGroup(t, Run(direct)), shared.TierStandard)
	if !c.Window.Earliest.Equal(at(23, 9, 0)) {
		t.Fatalf("a direct lane: %+v", c)
	}
}

func TestNominalBookIsFullIsCapacityExhausted(t *testing.T) {
	in := fixture()
	in.Waves, in.Departures = nil, nil
	in.Books = map[string]BookView{"N-AUS3:nominal-20261022T2300Z": {Open: true, Room: 0, HoldDeadlineAt: at(22, 16, 25)}}
	g := onlyGroup(t, Run(in))
	// the first cutoff is full, the next one (Friday 11:00) is offered instead
	c := best(t, g, shared.TierStandard)
	if !c.ShipBy.Equal(at(23, 11, 0)) {
		t.Fatalf("the next cutoff: %+v", c)
	}
	in.Books = map[string]BookView{}
	for _, id := range []string{"nominal-20261022T2300Z", "nominal-20261023T1600Z", "nominal-20261023T2300Z", "nominal-20261024T1600Z", "nominal-20261024T2300Z", "nominal-20261025T1600Z"} {
		in.Books["N-AUS3:"+id] = BookView{Open: true, Room: 0, HoldDeadlineAt: at(30, 0, 0)}
	}
	if g := onlyGroup(t, Run(in)); g.Unavailable[shared.TierStandard] != NodeCapacityExhausted {
		t.Fatalf("every nominal book full: %v", g.Unavailable)
	}
}

func TestHandicapByGrade(t *testing.T) {
	cases := []struct {
		grade    Grade
		handling int
		eff      int
		skip     int
	}{
		{GradeA, 95, 95, 0}, {"", 95, 95, 0}, {GradeProvisional, 95, 95, 0},
		{GradeB, 100, 125, 0}, {GradeB, 95, 119, 0}, {GradeB, 1, 2, 0}, {GradeB, 0, 0, 0},
		{GradeC, 95, 95, 1}, {GradeD, 95, 95, 2},
	}
	for _, c := range cases {
		if eff, skip := Handicap(c.grade, c.handling); eff != c.eff || skip != c.skip {
			t.Errorf("%q %d: got %d,%d want %d,%d", c.grade, c.handling, eff, skip, c.eff, c.skip)
		}
	}
	for g, rank := range map[Grade]int{GradeA: 0, "": 0, GradeProvisional: 0, GradeB: 1, GradeC: 2, GradeD: 3} {
		if g.Rank() != rank {
			t.Errorf("rank(%q) = %d", g, g.Rank())
		}
	}
}

// withMorningPickup adds the next day's 11:00 pickup and its linehaul and wave.
func withMorningPickup(in Input) Input {
	in = in.withDeparture(Departure{ID: "dep-AUS3-1100", LaneID: "lane-1", Kind: KindPickup, Origin: "N-AUS3", Dest: "N-SAU1", CutoffAt: at(23, 11, 0), ArrivesAt: at(23, 12, 15), Remaining: 40, Scheduled: true})
	in = in.withDeparture(Departure{ID: "dep-SAU1-DAU1-1600", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-SAU1", Dest: "N-DAU1", CutoffAt: at(23, 16, 0), ArrivesAt: at(23, 17, 30), Remaining: 140, Scheduled: true})
	return in.withWave(Wave{ID: "W-DAU1-1024-A", Station: "N-DAU1", InductCutoffAt: at(24, 6, 0), WindowStart: at(24, 9, 0), WindowEnd: at(24, 21, 0), Remaining: 100, Scheduled: true})
}

func TestScorecardHandicapDelaysTheNodeButNeverDropsIt(t *testing.T) {
	base := withMorningPickup(fixture())
	// grade A gets the 18:00 pickup
	if c := best(t, onlyGroup(t, Run(base)), shared.TierNextDay); c.Witness.PickupDepartureID != "dep-AUS3-1800" {
		t.Fatalf("grade A: %+v", c.Witness)
	}
	// grade B: handling 95 -> 119, ready 16:04: still the 18:00 pickup, and the expiry cutoff is earlier
	b := base
	b.Grades = map[string]Grade{"N-AUS3": GradeB}
	cb := best(t, onlyGroup(t, Run(b)), shared.TierNextDay)
	if cb.Witness.PickupDepartureID != "dep-AUS3-1800" || !cb.LatestMeetableAt.Equal(at(22, 18, 0).Add(-119*time.Minute)) {
		t.Fatalf("grade B: %+v meet %v", cb.Witness, cb.LatestMeetableAt)
	}
	// grade C skips one feasible pickup: the 18:00 one; grade D skips two: nothing is left for NEXT_DAY
	c := base
	c.Grades = map[string]Grade{"N-AUS3": GradeC}
	cc := onlyGroup(t, Run(c))
	if got := best(t, cc, shared.TierTwoDay); got.Witness.PickupDepartureID != "dep-AUS3-1100" || got.Grade != GradeC {
		t.Fatalf("grade C: %+v", got)
	}
	d := base
	d.Grades = map[string]Grade{"N-AUS3": GradeD}
	if g := onlyGroup(t, Run(d)); len(g.Candidates[shared.TierStandard]) != 0 && g.Candidates[shared.TierStandard][0].Basis == shared.BasisScheduled {
		t.Fatalf("grade D with two pickups skips both: %+v", g.Candidates[shared.TierStandard])
	}
}

func TestHandlingTimeDecidesTheFirstFeasiblePickup(t *testing.T) {
	in := withMorningPickup(fixture())
	in.Now = at(22, 17, 0) // 17:00 + 95 = 18:35 misses 18:00; Saturday is two days away
	c := best(t, onlyGroup(t, Run(in)), shared.TierTwoDay)
	if c.Witness.PickupDepartureID != "dep-AUS3-1100" || !c.Window.Latest.Equal(at(24, 21, 0)) {
		t.Fatalf("the morning pickup: %+v", c)
	}
	// one minute before the latest meetable instant (16:25) the 18:00 pickup is still offered, for one minute
	in.Now = at(22, 16, 24)
	if c := best(t, onlyGroup(t, Run(in)), shared.TierNextDay); c.Witness.PickupDepartureID != "dep-AUS3-1800" || !c.ExpiresAt.Equal(at(22, 16, 25)) {
		t.Fatalf("ready at 17:59: %+v", c)
	}
	// at 16:25 sharp the offer would expire the instant it is quoted, so the pickup is skipped
	in.Now = at(22, 16, 25)
	if c := best(t, onlyGroup(t, Run(in)), shared.TierTwoDay); c.Witness.PickupDepartureID != "dep-AUS3-1100" {
		t.Fatalf("ready exactly at 18:00 but not meetable: %+v", c.Witness)
	}
}

func TestSameDayNeedsASubSameDayCentreWithADirectLane(t *testing.T) {
	in := fixture()
	n := in.Nodes["N-AUS3"]
	n.Subtype = SubtypeSubSameDay
	in = in.withNode(n)
	in.Now = at(22, 8, 0)
	in = in.withDeparture(Departure{ID: "dep-direct", LaneID: "lane-3", Kind: KindPickup, Origin: "N-AUS3", Dest: "N-DAU1", CutoffAt: at(22, 11, 0), ArrivesAt: at(22, 12, 0), Remaining: 10, Scheduled: true})
	in = in.withWave(Wave{ID: "W-same", Station: "N-DAU1", InductCutoffAt: at(22, 13, 0), WindowStart: at(22, 15, 0), WindowEnd: at(22, 21, 0), Remaining: 10, Scheduled: true})
	g := onlyGroup(t, Run(in))
	c := best(t, g, shared.TierSameDay)
	if !c.Direct || c.Witness.PickupDepartureID != "dep-direct" || len(c.Witness.Legs) != 0 || c.Witness.DeliveryWaveID != "W-same" {
		t.Fatalf("same day: %+v", c)
	}
	// the same path from a plain SORTABLE centre is not SAME_DAY
	plain := fixture()
	plain.Now = in.Now
	plain = plain.withDeparture(in.Departures[2]).withWave(in.Waves[1])
	if g := onlyGroup(t, Run(plain)); len(g.Candidates[shared.TierSameDay]) != 0 || len(g.Candidates[shared.TierNextDay]) == 0 {
		t.Fatalf("SORTABLE node: %+v", g.Candidates)
	}
	// a MICRO centre qualifies too
	n.Subtype = SubtypeMicro
	if g := onlyGroup(t, Run(in.withNode(n))); len(g.Candidates[shared.TierSameDay]) == 0 {
		t.Fatal("MICRO")
	}
}

func TestExclusionsAndLockedNode(t *testing.T) {
	cases := map[string]func(*Input){
		"excluded node":      func(in *Input) { in.ExcludeNodes = map[string]bool{"N-AUS3": true} },
		"excluded pickup":    func(in *Input) { in.ExcludeDepartures = map[string]bool{"dep-AUS3-1800": true} },
		"excluded leg":       func(in *Input) { in.ExcludeDepartures = map[string]bool{"dep-SAU1-DAU1-2200": true} },
		"excluded wave":      func(in *Input) { in.ExcludeWaves = map[string]bool{"W-DAU1-1023-A": true} },
		"only another node":  func(in *Input) { in.OnlyNode = "N-OTHER" },
		"cancelled pickup":   func(in *Input) { in.Departures[0].Cancelled = true },
		"cancelled leg":      func(in *Input) { in.Departures[1].Cancelled = true },
		"cancelled wave":     func(in *Input) { in.Waves[0].Cancelled = true },
		"unscheduled pickup": func(in *Input) { in.Departures[0].Scheduled = false },
		"unscheduled wave":   func(in *Input) { in.Waves[0].Scheduled = false },
		"closed lane":        func(in *Input) { in.Lanes = []Lane{{ID: "lane-2", Origin: "N-SAU1", Dest: "N-DAU1", Open: false}} },
	}
	for name, mut := range cases {
		in := fixture()
		in.Departures = append([]Departure{}, in.Departures...)
		in.Waves = append([]Wave{}, in.Waves...)
		mut(&in)
		r := Run(in)
		if len(r.Groups) == 1 && len(r.Groups[0].Candidates[shared.TierNextDay]) != 0 {
			t.Errorf("%s: NEXT_DAY must be unavailable, got %+v", name, r.Groups[0].Candidates[shared.TierNextDay])
		}
	}
	in := fixture()
	in.OnlyNode = "N-AUS3"
	if c := best(t, onlyGroup(t, Run(in)), shared.TierNextDay); c.NodeID != "N-AUS3" {
		t.Fatalf("the locked node: %+v", c)
	}
	// a lane that LaneView does not know is tolerated
	in = fixture()
	in.Lanes = nil
	if g := onlyGroup(t, Run(in)); len(g.Candidates[shared.TierNextDay]) == 0 {
		t.Fatal("an unknown lane is tolerated")
	}
}

// twoHopFixture: a cheap but slow route and a fast route through another sort centre.
func twoHopFixture() Input {
	in := fixture()
	hub := Node{ID: "N-HUB", Type: "SORTATION_CENTER", Active: true, Loc: cdt, Profile: &Profile{Schedule: calendar.Schedule{Loc: cdt, Hours: allDays(0, 24*60-1)}, SortDwellMinutes: 30}}
	in = in.withNode(hub)
	in.Departures = []Departure{
		{ID: "pick", Kind: KindPickup, Origin: "N-AUS3", Dest: "N-SAU1", CutoffAt: at(22, 18, 0), ArrivesAt: at(22, 19, 0), Remaining: 9, Scheduled: true},
		{ID: "slow", Kind: KindLinehaul, Origin: "N-SAU1", Dest: "N-DAU1", CutoffAt: at(22, 21, 30), ArrivesAt: at(23, 4, 0), Remaining: 9, Scheduled: true},
		{ID: "hop1", Kind: KindLinehaul, Origin: "N-SAU1", Dest: "N-HUB", CutoffAt: at(22, 21, 30), ArrivesAt: at(22, 22, 0), Remaining: 9, Scheduled: true},
		{ID: "hop2", Kind: KindAir, Origin: "N-HUB", Dest: "N-DAU1", CutoffAt: at(22, 22, 30), ArrivesAt: at(23, 1, 0), Remaining: 9, Scheduled: true},
		{ID: "late-pickup-as-leg", Kind: KindPickup, Origin: "N-SAU1", Dest: "N-DAU1", CutoffAt: at(22, 21, 30), ArrivesAt: at(22, 21, 45), Remaining: 9, Scheduled: true},
	}
	in.Waves[0].InductCutoffAt = at(23, 6, 0)
	return in
}

func TestEarliestArrivalTakesTheFasterMultiHopAndIgnoresPickupsAsLegs(t *testing.T) {
	c := best(t, onlyGroup(t, Run(twoHopFixture())), shared.TierNextDay)
	if got := c.Witness.Legs; len(got) != 2 || got[0] != "hop1" || got[1] != "hop2" || c.LegCount != 2 {
		t.Fatalf("legs = %v", got)
	}
	// the dwell at SAU1 (120 min) is respected: arrival 19:00 + 120 = 21:00, so the 21:30 cutoffs are reachable but a 20:30 cutoff is not
	in := twoHopFixture()
	in.Departures[1].CutoffAt = at(22, 20, 30)
	in.Departures[2].CutoffAt = at(22, 20, 30)
	if g := onlyGroup(t, Run(in)); len(g.Candidates[shared.TierNextDay]) != 0 {
		t.Fatalf("a departure before the sort dwell ends is missed: %+v", g.Candidates[shared.TierNextDay])
	}
	// the dwell at the hub (30 min) is respected too: 22:00 + 30 = 22:30 is exactly reachable, 22:29 is not
	in = twoHopFixture()
	in.Departures[3].CutoffAt = at(22, 22, 29)
	c = best(t, onlyGroup(t, Run(in)), shared.TierNextDay)
	if len(c.Witness.Legs) != 1 || c.Witness.Legs[0] != "slow" {
		t.Fatalf("hub too tight, fall back to the slow leg: %v", c.Witness.Legs)
	}
}

func TestEqualArrivalPrefersFewerLegs(t *testing.T) {
	in := twoHopFixture()
	in.Departures[1].ArrivesAt = at(23, 1, 0) // the direct leg now arrives together with the two-hop path
	c := best(t, onlyGroup(t, Run(in)), shared.TierNextDay)
	if len(c.Witness.Legs) != 1 || c.Witness.Legs[0] != "slow" {
		t.Fatalf("one leg beats two at the same arrival: %v", c.Witness.Legs)
	}
}

func TestWaveSelection(t *testing.T) {
	in := fixture()
	in = in.withWave(Wave{ID: "W-too-early", Station: "N-DAU1", InductCutoffAt: at(22, 23, 0), WindowStart: at(23, 5, 0), WindowEnd: at(23, 8, 0), Remaining: 9, Scheduled: true})
	in = in.withWave(Wave{ID: "W-later", Station: "N-DAU1", InductCutoffAt: at(23, 12, 0), WindowStart: at(23, 15, 0), WindowEnd: at(24, 1, 0), Remaining: 9, Scheduled: true})
	in = in.withWave(Wave{ID: "W-other-station", Station: "N-ELSE", InductCutoffAt: at(23, 6, 0), WindowStart: at(23, 7, 0), WindowEnd: at(23, 8, 0), Remaining: 9, Scheduled: true})
	if c := best(t, onlyGroup(t, Run(in)), shared.TierNextDay); c.Witness.DeliveryWaveID != "W-DAU1-1023-A" {
		t.Fatalf("the first wave whose induct cutoff is not before the arrival: %q", c.Witness.DeliveryWaveID)
	}
	// a wave whose induct cutoff equals the arrival still takes the package
	in = fixture()
	in.Waves[0].InductCutoffAt = at(22, 23, 30)
	if c := best(t, onlyGroup(t, Run(in)), shared.TierNextDay); c.Witness.DeliveryWaveID != "W-DAU1-1023-A" {
		t.Fatalf("induct cutoff == arrival: %+v", c.Witness)
	}
	in.Waves[0].InductCutoffAt = at(22, 23, 30).Add(-time.Nanosecond)
	if g := onlyGroup(t, Run(in)); len(g.Candidates[shared.TierNextDay]) != 0 {
		t.Fatalf("induct cutoff just before the arrival: %+v", g.Candidates[shared.TierNextDay])
	}
	// a wave that opens before the shipBy can not be promised
	in = fixture()
	in.Waves[0].WindowStart = at(22, 18, 0)
	if g := onlyGroup(t, Run(in)); len(g.Candidates[shared.TierNextDay]) != 0 {
		t.Fatalf("shipBy must be before deliverByEarliest: %+v", g.Candidates[shared.TierNextDay])
	}
}

func TestTierFitByCalendarDays(t *testing.T) {
	cases := []struct {
		windowEnd time.Time
		fits      map[shared.Tier]bool
	}{
		{at(22, 23, 0), map[shared.Tier]bool{shared.TierNextDay: true, shared.TierTwoDay: true, shared.TierStandard: true}},
		{at(23, 23, 59), map[shared.Tier]bool{shared.TierNextDay: true, shared.TierTwoDay: true, shared.TierStandard: true}},
		{at(24, 0, 1), map[shared.Tier]bool{shared.TierNextDay: false, shared.TierTwoDay: true, shared.TierStandard: true}},
		{at(25, 0, 1), map[shared.Tier]bool{shared.TierNextDay: false, shared.TierTwoDay: false, shared.TierStandard: true}},
		{at(29, 23, 0), map[shared.Tier]bool{shared.TierNextDay: false, shared.TierTwoDay: false, shared.TierStandard: true}},
		{at(30, 0, 1), map[shared.Tier]bool{shared.TierNextDay: false, shared.TierTwoDay: false, shared.TierStandard: false}},
	}
	for _, c := range cases {
		in := fixture()
		in.Waves = []Wave{{ID: "W", Station: "N-DAU1", InductCutoffAt: at(23, 6, 0), WindowStart: at(22, 23, 45), WindowEnd: c.windowEnd, Remaining: 9, Scheduled: true}}
		if !c.windowEnd.After(at(22, 23, 45)) {
			in.Waves[0].WindowStart = c.windowEnd
		}
		g := onlyGroup(t, Run(in))
		for tier, want := range c.fits {
			if got := scheduledFits(g, tier); got != want {
				t.Errorf("window ends %v, %s: got %v want %v", c.windowEnd, tier, got, want)
			}
		}
	}
}

func TestShipmentGroupsPreferOneNodeAndSplitMinimally(t *testing.T) {
	in := fixture()
	in.Lines = []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}, {CatalogItemID: "ci-2002", Quantity: 1}}
	// AUS3 has only ci-1001: the cart needs a second node for ci-2002
	dfw := Node{ID: "N-DFW7", Type: TypeFulfillmentCenter, RegionID: "R-SC", Active: true, Loc: cdt, Profile: fcProfile(240, calendar.Cutoff{Minute: 16 * 60})}
	in = in.withNode(dfw).stock("N-DFW7", "ci-2002", 5)
	r := Run(in)
	if len(r.Groups) != 2 || r.Groups[0].No != 1 || r.Groups[1].No != 2 {
		t.Fatalf("groups = %+v", r.Groups)
	}
	if r.Groups[0].Lines[0].CatalogItemID != "ci-1001" || r.Groups[1].Lines[0].CatalogItemID != "ci-2002" {
		t.Fatalf("lines per group = %+v / %+v", r.Groups[0].Lines, r.Groups[1].Lines)
	}
	// when DFW7 can also ship ci-1001, one group suffices
	in = in.stock("N-DFW7", "ci-1001", 2)
	if r := Run(in); len(r.Groups) != 1 || len(r.Groups[0].Lines) != 2 {
		t.Fatalf("one node covers the cart: %+v", r.Groups)
	}
}

func TestGroupLinesGreedyCover(t *testing.T) {
	lines := []shared.Line{{CatalogItemID: "a", Quantity: 1}, {CatalogItemID: "b", Quantity: 1}, {CatalogItemID: "c", Quantity: 1}, {CatalogItemID: "d", Quantity: 1}}
	cover := func(items ...string) map[string]bool {
		m := map[string]bool{}
		for _, i := range items {
			m[i] = true
		}
		return m
	}
	cases := []struct {
		name   string
		covers map[string]map[string]bool
		want   []int
		ok     bool
	}{
		{"one node covers all", map[string]map[string]bool{"n1": cover("a", "b", "c", "d"), "n2": cover("a")}, []int{4}, true},
		{"split 3+1", map[string]map[string]bool{"n1": cover("a", "b", "c"), "n2": cover("d")}, []int{3, 1}, true},
		{"split 2+2 tie by id", map[string]map[string]bool{"n2": cover("c", "d"), "n1": cover("a", "b")}, []int{2, 2}, true},
		{"greedy takes the larger first", map[string]map[string]bool{"n1": cover("a", "b"), "n2": cover("b", "c", "d"), "n3": cover("a")}, []int{3, 1}, true},
		{"a line nobody covers", map[string]map[string]bool{"n1": cover("a", "b", "c")}, nil, false},
		{"no nodes", map[string]map[string]bool{}, nil, false},
	}
	for _, c := range cases {
		groups, ok := GroupLines(lines, c.covers)
		if ok != c.ok || len(groups) != len(c.want) {
			t.Errorf("%s: ok=%v groups=%v", c.name, ok, groups)
			continue
		}
		for i, n := range c.want {
			if len(groups[i]) != n {
				t.Errorf("%s: group %d has %d lines, want %d", c.name, i, len(groups[i]), n)
			}
		}
	}
	if g, ok := GroupLines(nil, nil); !ok || len(g) != 0 {
		t.Error("an empty cart has no groups")
	}
}

func TestSingleGroupKeepsTheWholeGroupOnOneNode(t *testing.T) {
	in := fixture()
	in.SingleGroup = true
	in.Lines = []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}, {CatalogItemID: "ci-2002", Quantity: 1}}
	if r := Run(in); len(r.Groups) != 0 || r.WholeQuote[shared.TierNextDay] != ItemQuantityNotAvailable {
		t.Fatalf("no single node covers both lines: %+v", r)
	}
	in = in.stock("N-AUS3", "ci-2002", 1)
	if r := Run(in); len(r.Groups) != 1 || len(r.Groups[0].Lines) != 2 {
		t.Fatalf("AUS3 covers both: %+v", r)
	}
}

func TestRankOrdersByWindowRegionLegsGradeAndNode(t *testing.T) {
	base := Candidate{NodeID: "b", Window: shared.Window{Latest: at(23, 21, 0)}, InRegion: true, LegCount: 1, Grade: GradeA, ShipBy: at(22, 18, 0)}
	mut := func(f func(*Candidate)) Candidate { c := base; f(&c); return c }
	cases := []struct {
		name string
		a, b Candidate
	}{
		{"earlier window", mut(func(c *Candidate) { c.Window.Latest = at(23, 20, 0) }), base},
		{"in region", base, mut(func(c *Candidate) { c.InRegion = false })},
		{"fewer legs", mut(func(c *Candidate) { c.LegCount = 0 }), base},
		{"better grade", base, mut(func(c *Candidate) { c.Grade = GradeC })},
		{"node id", mut(func(c *Candidate) { c.NodeID = "a" }), base},
		{"earlier shipBy", mut(func(c *Candidate) { c.ShipBy = at(22, 11, 0) }), base},
	}
	for _, c := range cases {
		if rank(c.a, c.b) >= 0 || rank(c.b, c.a) <= 0 {
			t.Errorf("%s: a must rank before b", c.name)
		}
	}
	if rank(base, base) != 0 {
		t.Error("equal candidates tie")
	}
}

func TestCandidatesAreCappedAndRanked(t *testing.T) {
	in := fixture()
	for i := 0; i < 8; i++ {
		day := 22 + i/2
		hour := 6 + 12*(i%2)
		in = in.withDeparture(Departure{ID: "pick" + string(rune('a'+i)), Kind: KindPickup, Origin: "N-AUS3", Dest: "N-DAU1", CutoffAt: at(day+1, hour, 0), ArrivesAt: at(day+1, hour+1, 0), Remaining: 9, Scheduled: true})
		in = in.withWave(Wave{ID: "w" + string(rune('a'+i)), Station: "N-DAU1", InductCutoffAt: at(day+1, hour+2, 0), WindowStart: at(day+1, hour+3, 0), WindowEnd: at(day+1, hour+9, 0), Remaining: 9, Scheduled: true})
	}
	cs := onlyGroup(t, Run(in)).Candidates[shared.TierStandard]
	if len(cs) != MaxCandidates {
		t.Fatalf("candidates = %d, want %d", len(cs), MaxCandidates)
	}
	for i := 1; i < len(cs); i++ {
		if cs[i].Window.Latest.Before(cs[i-1].Window.Latest) {
			t.Fatalf("candidates are ranked by window end: %v", cs)
		}
	}
}

func TestOpeningForComputesTheShareFromCutoffSlotAndThroughput(t *testing.T) {
	n := fixture().Nodes["N-AUS3"]
	pol := policy.Default()
	op, ok := OpeningFor(n, "dep-AUS3-1800", at(22, 18, 0), pol)
	// 11:00 -> 18:00 is 7 operating hours x 400/h = 2800 orders; the slot says 2500
	if !ok || op.Share != 2500 || !op.CutoffAt.Equal(at(22, 18, 0)) || !op.HoldDeadlineAt.Equal(at(22, 16, 25)) || op.NodeID != "N-AUS3" || op.PickupDepartureID != "dep-AUS3-1800" {
		t.Fatalf("opening = %+v", op)
	}
	// the 11:00 cutoff: previous cutoff is yesterday 18:00; operating minutes 06:00-11:00 plus 18:00-22:00 = 540 min -> 3600; no slot cap
	op, _ = OpeningFor(n, "dep-1100", at(22, 11, 0), pol)
	if op.Share != 3600 {
		t.Fatalf("share = %d, want 3600", op.Share)
	}
	// a share percent of 50 halves it
	half, _, _ := policy.Publish(0, policy.Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 50}, at(1, 0, 0))
	if op, _ = OpeningFor(n, "dep-AUS3-1800", at(22, 18, 0), half); op.Share != 1250 {
		t.Fatalf("share at 50%% = %d", op.Share)
	}
	// a node without a cutoff before: 24 hours back
	lone := n
	lone.Profile = fcProfile(95, calendar.Cutoff{Minute: 18 * 60, Days: []time.Weekday{time.Thursday}})
	lone.Profile.Cutoffs[0].Days = nil
	if op, _ = OpeningFor(lone, "x", at(22, 18, 0), pol); op.Share != 6400 {
		t.Fatalf("a single daily cutoff covers the whole 16 operating hours: %d", op.Share)
	}
	if _, ok := OpeningFor(Node{ID: "x"}, "p", at(22, 18, 0), pol); ok {
		t.Fatal("no profile, no opening")
	}
}

func TestBookIDFor(t *testing.T) {
	if got := BookIDFor(shared.Witness{NodeID: "n", PickupDepartureID: "d"}, at(22, 18, 0)); got != "n:d" {
		t.Errorf("scheduled: %q", got)
	}
	if got := BookIDFor(shared.Witness{NodeID: "n"}, at(22, 18, 0)); got != "n:nominal-20261022T2300Z" {
		t.Errorf("nominal: %q", got)
	}
}

func TestSearchWithoutAPolicyUsesTheDefault(t *testing.T) {
	in := fixture()
	in.Policy = nil
	c := best(t, onlyGroup(t, Run(in)), shared.TierNextDay)
	if !c.ExpiresAt.Equal(at(22, 14, 15)) {
		t.Fatalf("default TTL: %v", c.ExpiresAt)
	}
	in.Station.Loc = nil
	in.Tiers = []shared.Tier{shared.TierStandard}
	g := onlyGroup(t, Run(in))
	if len(g.Candidates) == 0 {
		t.Fatal("a station without a zone searches in UTC")
	}
	if z := best(t, g, shared.TierStandard).Window.TimeZone; z != "UTC" {
		t.Fatalf("a station without a zone names UTC, got %q", z)
	}
}

// scheduledFits reports whether a SCHEDULED candidate fits the tier; STANDARD
// may also be answered by a nominal fallback, which is not what is asked here.
func scheduledFits(g Group, tier shared.Tier) bool {
	return len(g.Candidates[tier]) > 0 && g.Candidates[tier][0].Basis == shared.BasisScheduled
}
