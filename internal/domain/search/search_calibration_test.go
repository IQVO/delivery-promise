package search

import (
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// buffered gives every tier the same calibration answer.
func buffered(in Input, c Calibration) Input {
	in.CalibrationBuffer = func(shared.Tier, shared.Basis, Grade) Calibration { return c }
	return in
}

// withNextWave adds the next day's wave to the worked example: its induct
// cutoff is 20:00 on the 23rd and its window the 24th 09:00-21:00.
func withNextWave(in Input) Input {
	return in.withWave(Wave{ID: "W-DAU1-1024-B", Station: "N-DAU1", InductCutoffAt: at(23, 20, 0), WindowStart: at(24, 9, 0),
		WindowEnd: at(24, 21, 0), Remaining: 320, Scheduled: true})
}

func width(w shared.Window) time.Duration { return w.Latest.Sub(w.Earliest) }

func TestABufferPastTheInductCutoffLandsInTheNextWindowOfTheSameWidth(t *testing.T) {
	// the timetable arrives at 23:30; 400 minutes more is 06:10, ten minutes
	// after wave A's induct cutoff
	in := withNextWave(measuredFixture())
	plain := best(t, onlyGroup(t, Run(in)), shared.TierTwoDay)
	c := best(t, onlyGroup(t, Run(buffered(in, Calibration{BufferMinutes: 400, Confidence: shared.NewConfidence(0.95)}))), shared.TierTwoDay)
	if !c.Window.Earliest.Equal(at(24, 9, 0)) || !c.Window.Latest.Equal(at(24, 21, 0)) {
		t.Fatalf("window = %+v, want the 24th 09:00-21:00", c.Window)
	}
	if width(c.Window) != width(plain.Window) {
		t.Fatalf("the quoted window width changed: %v vs %v", width(c.Window), width(plain.Window))
	}
	if c.Witness.DeliveryWaveID != "W-DAU1-1024-B" || c.BufferMinutes != 400 || c.Confidence.Value() != 0.95 {
		t.Fatalf("wave/buffer/confidence = %s/%d/%v", c.Witness.DeliveryWaveID, c.BufferMinutes, c.Confidence)
	}
	if c.Basis != shared.BasisScheduled {
		t.Fatalf("a buffer does not change the basis: %s", c.Basis)
	}
}

func TestABufferThatFitsBeforeTheInductCutoffKeepsTheWindowButCarriesTheConfidence(t *testing.T) {
	r := Run(buffered(withNextWave(measuredFixture()), Calibration{BufferMinutes: 60, Confidence: shared.NewConfidence(0.97)}))
	c := best(t, onlyGroup(t, r), shared.TierNextDay)
	assertWorkedWindow(t, c.Window)
	if c.BufferMinutes != 60 || c.Confidence.Value() != 0.97 {
		t.Fatalf("buffer/confidence = %d/%v", c.BufferMinutes, c.Confidence)
	}
}

func TestABufferThatLeavesTheTierHorizonDropsTheCandidate(t *testing.T) {
	// NEXT_DAY allows the 23rd only: the 24th window is beyond it
	g := onlyGroup(t, Run(buffered(withNextWave(measuredFixture()), Calibration{BufferMinutes: 400})))
	if cs := g.Candidates[shared.TierNextDay]; len(cs) != 0 {
		t.Fatalf("NEXT_DAY must lose its candidate, got %+v", cs)
	}
	if g.Unavailable[shared.TierNextDay] != DeliverySLAUnavailable {
		t.Fatalf("constraint = %q", g.Unavailable[shared.TierNextDay])
	}
}

func TestABufferPastTheLastWaveLeavesNoCandidate(t *testing.T) {
	g := onlyGroup(t, Run(buffered(measuredFixture(), Calibration{BufferMinutes: 400}))) // only wave A exists
	if len(g.Candidates[shared.TierNextDay]) != 0 || g.Unavailable[shared.TierNextDay] != DeliverySLAUnavailable {
		t.Fatalf("no wave left: %+v %v", g.Candidates, g.Unavailable)
	}
}

func TestNoBufferIsExactlyTheF2Behaviour(t *testing.T) {
	in := withNextWave(measuredFixture())
	want := Run(in)
	for name, c := range map[string]func(shared.Tier, shared.Basis, Grade) Calibration{
		"a closure that never knows": func(shared.Tier, shared.Basis, Grade) Calibration { return Calibration{} },
		"a negative buffer":          func(shared.Tier, shared.Basis, Grade) Calibration { return Calibration{BufferMinutes: -500} },
	} {
		in.CalibrationBuffer = c
		if got := Run(in); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the result differs from the unbuffered search:\n got %+v\nwant %+v", name, got, want)
		}
	}
}

func TestTheBufferIsLookedUpBySegmentTierBasisAndGrade(t *testing.T) {
	type seg struct {
		tier  shared.Tier
		basis shared.Basis
		grade Grade
	}
	seen := map[seg]bool{}
	in := withNextWave(measuring(measuredFixture(), map[[2]string]int{{"N-SAU1", "N-DAU1"}: 135}))
	in.Grades = map[string]Grade{"N-AUS3": GradeB}
	in.CalibrationBuffer = func(t shared.Tier, b shared.Basis, g Grade) Calibration {
		seen[seg{t, b, g}] = true
		return Calibration{}
	}
	Run(in)
	for _, tier := range []shared.Tier{shared.TierNextDay, shared.TierTwoDay, shared.TierStandard} {
		if !seen[seg{tier, shared.BasisMeasured, GradeB}] {
			t.Errorf("tier %s was not looked up as MEASURED/B: %v", tier, seen)
		}
	}
}

func TestANodeWithoutAScorecardIsTheProvisionalSegment(t *testing.T) {
	var got Grade
	in := measuredFixture()
	in.CalibrationBuffer = func(_ shared.Tier, _ shared.Basis, g Grade) Calibration { got = g; return Calibration{} }
	Run(in)
	if got != GradeProvisional {
		t.Fatalf("grade = %q, want PROVISIONAL", got)
	}
}

func TestEachTierGetsItsOwnBuffer(t *testing.T) {
	in := withNextWave(measuredFixture())
	in.CalibrationBuffer = func(tier shared.Tier, _ shared.Basis, _ Grade) Calibration {
		if tier == shared.TierTwoDay {
			return Calibration{BufferMinutes: 400}
		}
		return Calibration{}
	}
	g := onlyGroup(t, Run(in))
	assertWorkedWindow(t, best(t, g, shared.TierNextDay).Window)
	if c := best(t, g, shared.TierTwoDay); !c.Window.Latest.Equal(at(24, 21, 0)) {
		t.Fatalf("TWO_DAY must carry its own buffer: %+v", c.Window)
	}
}

func TestStandardIsBufferedToo(t *testing.T) {
	in := buffered(withNextWave(measuredFixture()), Calibration{BufferMinutes: 400})
	if c := best(t, onlyGroup(t, Run(in)), shared.TierStandard); !c.Window.Latest.Equal(at(24, 21, 0)) {
		t.Fatalf("STANDARD window = %+v", c.Window)
	}
}

func TestTheNominalFallbackIsBufferedAndStaysNominal(t *testing.T) {
	in := measuredFixture()
	in.Departures, in.OnlyNode = nil, "N-AUS3"
	plain := best(t, onlyGroup(t, Run(in)), shared.TierStandard)
	if plain.Basis != shared.BasisNominal {
		t.Fatalf("basis = %s", plain.Basis)
	}
	// the 18:00 cutoff plus 285 nominal minutes arrives at 22:45; two more
	// hours cross midnight, so the nominal window moves one day on
	c := best(t, onlyGroup(t, Run(buffered(in, Calibration{BufferMinutes: 120, Confidence: shared.NewConfidence(0.9)}))), shared.TierStandard)
	if c.Basis != shared.BasisNominal || !c.Window.Latest.Equal(plain.Window.Latest.Add(24*time.Hour)) {
		t.Fatalf("nominal window = %+v (plain %+v)", c.Window, plain.Window)
	}
	if width(c.Window) != width(plain.Window) || c.Confidence.Value() != 0.9 {
		t.Fatalf("width/confidence: %v %v", width(c.Window), c.Confidence)
	}
}
