package search

import (
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/policy"
)

// zeros returns n kept-promise observations.
func zeros(n int) []int { return make([]int, n) }

// lateAfter returns a run of n on-time observations followed by the late ones.
func lateAfter(n int, late ...int) []int { return append(zeros(n), late...) }

func TestCalibrationBufferIsTheSmallestBThatCoversTheTarget(t *testing.T) {
	// 100 observations: 90 kept, then 10, 20, ... 100 minutes late
	obs := lateAfter(90, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100)
	cases := []struct{ target, want int }{
		{90, 0},  // the kept share (90%) already meets the target
		{91, 10}, // one more observation is needed: the smallest late one
		{95, 50}, // the 95th observation
		{99, 90}, // the 99th observation
		{100, 100},
	}
	for _, c := range cases {
		if got := CalibrationBuffer(obs, c.target); got != c.want {
			t.Errorf("target %d%%: buffer = %d, want %d", c.target, got, c.want)
		}
	}
}

func TestCalibrationBufferQuantileBoundaryIsInclusive(t *testing.T) {
	// 20 observations, 5% each: the 95% quantile is the 19th, not the 20th
	obs := lateAfter(18, 15, 400)
	if got := CalibrationBuffer(obs, 95); got != 15 {
		t.Fatalf("buffer = %d, want 15: exactly 95%% of the observations are <= 15", got)
	}
	if got := CalibrationBuffer(obs, 96); got != 400 {
		t.Fatalf("buffer = %d, want 400: 95%% is no longer enough", got)
	}
}

func TestCalibrationBufferIsOrderIndependentAndDoesNotMutateItsInput(t *testing.T) {
	obs := []int{50, 0, 30, 0, 10, 0, 0, 0, 0, 0}
	before := append([]int(nil), obs...)
	if got := CalibrationBuffer(obs, 90); got != 30 {
		t.Fatalf("buffer = %d, want 30", got)
	}
	for i := range obs {
		if obs[i] != before[i] {
			t.Fatalf("the input was reordered: %v", obs)
		}
	}
}

func TestCalibrationBufferIsNeverNegativeAndNeverEarlier(t *testing.T) {
	if got := CalibrationBuffer(nil, 95); got != 0 {
		t.Fatalf("no observations: buffer = %d, want 0", got)
	}
	if got := CalibrationBuffer([]int{-30, -10, -5, -1}, 95); got != 0 {
		t.Fatalf("early observations must not buy an earlier promise: buffer = %d", got)
	}
	if got := CalibrationBuffer(zeros(80), 99); got != 0 {
		t.Fatalf("all kept: buffer = %d, want 0", got)
	}
}

func calibrationPolicy(t *testing.T, target, minObs int, maxBuffer int) *policy.Policy {
	t.Helper()
	p, _, err := policy.Publish(0, policy.Input{
		OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100,
		TargetOnTimeProbabilityPercent: target, MinCalibrationObservations: minObs, MaxCalibrationBufferMinutes: &maxBuffer,
	}, at(22, 12, 0))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCalibrateBelowTheMinimumIsNoBufferAndUnknownConfidence(t *testing.T) {
	pol := calibrationPolicy(t, 95, 50, 720)
	c := Calibrate(lateAfter(0, 300, 400, 500, 600, 700, 800, 900, 900, 900, 900, 900, 900), pol) // 12 late ones, n < 50
	if c.BufferMinutes != 0 || c.Confidence.Known() {
		t.Fatalf("below the minimum the segment must be silent: %+v", c)
	}
	if c := Calibrate(nil, pol); c.BufferMinutes != 0 || c.Confidence.Known() {
		t.Fatalf("no observations: %+v", c)
	}
}

func TestCalibrateAtTheMinimumAppliesTheBufferWithItsConfidence(t *testing.T) {
	pol := calibrationPolicy(t, 90, 50, 720)
	obs := lateAfter(40, 20, 20, 20, 20, 20, 20, 20, 20, 20, 60) // n = 50: 40 kept, 9 x 20, 1 x 60
	c := Calibrate(obs, pol)
	if c.BufferMinutes != 20 {
		t.Fatalf("buffer = %d, want 20", c.BufferMinutes)
	}
	if !c.Confidence.Known() || c.Confidence.Value() != 0.98 {
		t.Fatalf("confidence = %+v, want 0.98 (49 of 50 within 20 minutes)", c.Confidence)
	}
	if got := Calibrate(obs[:49], pol); got.Confidence.Known() {
		t.Fatalf("one observation short of the minimum must stay unknown: %+v", got)
	}
}

func TestCalibrateCapsTheBufferAndTheConfidenceFallsBelowTheTarget(t *testing.T) {
	pol := calibrationPolicy(t, 95, 50, 120)
	obs := lateAfter(40, 90, 90, 90, 500, 500, 500, 500, 500, 500, 500) // 95th = 500, cap 120
	c := Calibrate(obs, pol)
	if c.BufferMinutes != 120 {
		t.Fatalf("buffer = %d, want the cap 120", c.BufferMinutes)
	}
	if c.Confidence.Value() != 0.86 { // 43 of 50 are <= 120
		t.Fatalf("confidence = %v, want 0.86: the cap trades confidence for a bounded promise", c.Confidence.Value())
	}
	off := calibrationPolicy(t, 95, 50, 0)
	if c := Calibrate(obs, off); c.BufferMinutes != 0 || !c.Confidence.Known() {
		t.Fatalf("a cap of 0 switches the buffer off but still reports the confidence: %+v", c)
	}
}

func TestCalibrateAlreadyMetKeepsABufferOfZero(t *testing.T) {
	pol := calibrationPolicy(t, 95, 50, 720)
	c := Calibrate(lateAfter(96, 200, 200, 200, 200), pol)
	if c.BufferMinutes != 0 || c.Confidence.Value() != 0.96 {
		t.Fatalf("kept share 96%% >= target: %+v", c)
	}
}

func TestSegmentGradeSpellsNoScorecardAsProvisional(t *testing.T) {
	for g, want := range map[Grade]Grade{"": GradeProvisional, GradeA: GradeA, GradeD: GradeD, GradeProvisional: GradeProvisional} {
		if got := SegmentGrade(g); got != want {
			t.Errorf("SegmentGrade(%q) = %q, want %q", g, got, want)
		}
	}
}
