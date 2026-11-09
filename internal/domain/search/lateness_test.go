package search

import (
	"testing"
	"time"
)

// latePath is the worked example's witness path: the pickup arrives at the
// sortation center at 19:15 (60 minutes of transit), the linehaul at the
// delivery station at 23:30 (75 minutes).
func latePath() []PathLeg {
	return []PathLeg{
		{Departure: Departure{ID: "dep-AUS3-1800", Origin: "N-AUS3", Dest: "N-SAU1", DepartsAt: at(22, 18, 15), ArrivesAt: at(22, 19, 15)}},
		{Departure: Departure{ID: "dep-SAU1-DAU1-2200", Origin: "N-SAU1", Dest: "N-DAU1", DepartsAt: at(22, 22, 15), ArrivesAt: at(22, 23, 30)}},
	}
}

func sortDwell(node string) time.Duration {
	if node == "N-SAU1" {
		return 120 * time.Minute
	}
	return 0
}

func TestExpectedArrivalIsOneSided(t *testing.T) {
	d := latePath()[0].Departure
	cases := []struct {
		name     string
		measured MeasuredTransitFunc
		want     time.Time
		adjusted bool
	}{
		{"nil closure", nil, d.ArrivesAt, false},
		{"too few observations", func(string, string) (bool, int) { return false, 0 }, d.ArrivesAt, false},
		{"faster than the timetable", func(string, string) (bool, int) { return true, 45 }, d.ArrivesAt, false},
		{"as fast as the timetable", func(string, string) (bool, int) { return true, 60 }, d.ArrivesAt, false},
		{"slower than the timetable", func(string, string) (bool, int) { return true, 95 }, d.ArrivesAt.Add(35 * time.Minute), true},
	}
	for _, c := range cases {
		got, adjusted := ExpectedArrival(d, c.measured)
		if !got.Equal(c.want) || adjusted != c.adjusted {
			t.Errorf("%s: got %s %v, want %s %v", c.name, got, adjusted, c.want, c.adjusted)
		}
	}
}

func TestAssessPathGraceIsInclusive(t *testing.T) {
	legs := latePath()[:1]
	if r := AssessPath(legs, nil, sortDwell, at(22, 19, 30)); r.Late {
		t.Fatalf("exactly at expected + grace is not late: %+v", r)
	}
	r := AssessPath(legs, nil, sortDwell, at(22, 19, 31))
	if !r.Late || r.LateDepartureID != "dep-AUS3-1800" || !r.LateExpected.Equal(at(22, 19, 15)) {
		t.Fatalf("one minute past the grace is late: %+v", r)
	}
}

func TestAssessPathSkipsArrivedAndReportsTheFirstLate(t *testing.T) {
	legs := latePath()
	legs[0].Arrived = true
	r := AssessPath(legs, nil, sortDwell, at(23, 23, 0))
	if !r.Late || r.LateDepartureID != "dep-SAU1-DAU1-2200" {
		t.Fatalf("the pickup arrived, the linehaul is late: %+v", r)
	}
	legs[1].Arrived = true
	if r := AssessPath(legs, nil, sortDwell, at(23, 23, 0)); r.Late {
		t.Fatalf("every leg arrived: %+v", r)
	}
	if r := AssessPath(nil, nil, sortDwell, at(23, 23, 0)); r.Late {
		t.Fatalf("a path without departures is never late: %+v", r)
	}
}

func TestAssessPathAppliesTheMeasuredAdjustment(t *testing.T) {
	legs := latePath()[:1]
	slow := func(string, string) (bool, int) { return true, 90 } // pickup expected 19:45
	if r := AssessPath(legs, slow, sortDwell, at(22, 19, 59)); r.Late {
		t.Fatalf("19:59 is inside 19:45 + 15 minutes: %+v", r)
	}
	r := AssessPath(legs, slow, sortDwell, at(22, 20, 1))
	if !r.Late || !r.LateExpected.Equal(at(22, 19, 45)) {
		t.Fatalf("20:01 is late against the measured 19:45: %+v", r)
	}
}

func TestAssessPathProjectsTheLatenessDownstream(t *testing.T) {
	// The pickup is 45 minutes behind (expected 19:15, now 20:00): the linehaul's
	// expected 23:30 shifts to 00:15 and the sort at N-SAU1 still lies ahead.
	r := AssessPath(latePath(), nil, sortDwell, at(22, 20, 0))
	if !r.Late || r.LateDepartureID != "dep-AUS3-1800" {
		t.Fatalf("verdict: %+v", r)
	}
	if want := at(23, 0, 15); !r.Projected.Equal(want) {
		t.Fatalf("projected %s, want %s", r.Projected, want)
	}
	if r.Dwell != 120*time.Minute {
		t.Fatalf("dwell %s, want the 120 minutes of N-SAU1 (the last leg reaches the station: no sort there)", r.Dwell)
	}
}

func TestAssessPathLastLegProjectsToNow(t *testing.T) {
	legs := latePath()
	legs[0].Arrived = true
	r := AssessPath(legs, nil, sortDwell, at(23, 0, 30)) // 60 minutes past the linehaul's 23:30
	if want := at(23, 0, 30); !r.Projected.Equal(want) || r.Dwell != 0 {
		t.Fatalf("a late last leg arrives no earlier than now: projected %s dwell %s", r.Projected, r.Dwell)
	}
}

func TestAssessPathWithoutDwellClosure(t *testing.T) {
	if r := AssessPath(latePath(), nil, nil, at(22, 20, 0)); !r.Late || r.Dwell != 0 {
		t.Fatalf("a nil dwell closure adds no dwell: %+v", r)
	}
}

func lateWave() Wave {
	// induct cutoff 06:00 on the 23rd, delivery window ends 21:00: 15 hours of last mile.
	return Wave{ID: "W-DAU1-1023-A", Station: "N-DAU1", InductCutoffAt: at(23, 6, 0), WindowStart: at(23, 9, 0), WindowEnd: at(23, 21, 0), Scheduled: true}
}

func TestReachableNotLateIsAlwaysReachable(t *testing.T) {
	if !(PathRisk{}).Reachable(Wave{}, at(22, 0, 0)) {
		t.Fatal("a path that is not late is reachable even without a wave")
	}
}

func TestReachableBoundary(t *testing.T) {
	r := PathRisk{Late: true, Projected: at(23, 0, 15), Dwell: 120 * time.Minute} // 02:15 + 15 h of last mile = 17:15
	if !r.Reachable(lateWave(), at(23, 17, 15)) {
		t.Fatal("equal to the window end is reachable")
	}
	if r.Reachable(lateWave(), at(23, 17, 14)) {
		t.Fatal("a minute past the window end is not")
	}
}

func TestReachableRefusesAWaveThatCannotBeUsed(t *testing.T) {
	r := PathRisk{Late: true, Projected: at(23, 0, 15)}
	cancelled, unscheduled := lateWave(), lateWave()
	cancelled.Cancelled, unscheduled.Scheduled = true, false
	for name, w := range map[string]Wave{"cancelled": cancelled, "unscheduled": unscheduled, "unknown": {}} {
		if r.Reachable(w, at(30, 0, 0)) {
			t.Errorf("a %s wave is never reachable", name)
		}
	}
}

func TestReachableNegativeLastMileCountsAsZero(t *testing.T) {
	w := lateWave()
	w.WindowEnd = w.InductCutoffAt.Add(-time.Hour)
	r := PathRisk{Late: true, Projected: at(23, 0, 15)}
	if !r.Reachable(w, at(23, 0, 15)) {
		t.Fatal("a window ending before the induct cutoff adds no allowance, never a negative one")
	}
}
