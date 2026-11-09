package policy

import (
	"errors"
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func intp(v int) *int { return &v }

func TestDefaultCalibrationParameters(t *testing.T) {
	p := Default()
	eq(t, "target", p.TargetOnTimeProbabilityPercent(), 95)
	eq(t, "min observations", p.MinCalibrationObservations(), 50)
	eq(t, "window days", p.CalibrationWindowDays(), 28)
	eq(t, "max buffer", p.MaxCalibrationBufferMinutes(), 720)
}

func TestCalibrationRangesAreEnforced(t *testing.T) {
	cases := map[string]func(*Input){
		"target low":       func(i *Input) { i.TargetOnTimeProbabilityPercent = MinTargetOnTimePercent - 1 },
		"target high":      func(i *Input) { i.TargetOnTimeProbabilityPercent = MaxTargetOnTimePercent + 1 },
		"min obs low":      func(i *Input) { i.MinCalibrationObservations = MinCalibrationObservationsFloor - 1 },
		"min obs high":     func(i *Input) { i.MinCalibrationObservations = MaxCalibrationObservationsCeiling + 1 },
		"window low":       func(i *Input) { i.CalibrationWindowDays = -1 }, // 0 means omitted
		"window high":      func(i *Input) { i.CalibrationWindowDays = MaxCalibrationWindowDays + 1 },
		"max buffer below": func(i *Input) { i.MaxCalibrationBufferMinutes = intp(-1) },
		"max buffer above": func(i *Input) { i.MaxCalibrationBufferMinutes = intp(MaxCalibrationBufferMinutesCeil + 1) },
	}
	for name, mut := range cases {
		in := valid()
		mut(&in)
		if _, _, err := Publish(0, in, t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
}

func TestCalibrationBoundsAreLegalAndStored(t *testing.T) {
	for _, in := range []Input{
		{TargetOnTimeProbabilityPercent: MinTargetOnTimePercent, MinCalibrationObservations: MinCalibrationObservationsFloor,
			CalibrationWindowDays: MinCalibrationWindowDays, MaxCalibrationBufferMinutes: intp(0)},
		{TargetOnTimeProbabilityPercent: MaxTargetOnTimePercent, MinCalibrationObservations: MaxCalibrationObservationsCeiling,
			CalibrationWindowDays: MaxCalibrationWindowDays, MaxCalibrationBufferMinutes: intp(MaxCalibrationBufferMinutesCeil)},
	} {
		base := valid()
		base.TargetOnTimeProbabilityPercent, base.MinCalibrationObservations = in.TargetOnTimeProbabilityPercent, in.MinCalibrationObservations
		base.CalibrationWindowDays, base.MaxCalibrationBufferMinutes = in.CalibrationWindowDays, in.MaxCalibrationBufferMinutes
		p, evs, err := Publish(0, base, t0)
		if err != nil {
			t.Fatalf("bounds refused: %v", err)
		}
		eq(t, "target", p.TargetOnTimeProbabilityPercent(), in.TargetOnTimeProbabilityPercent)
		eq(t, "max buffer", p.MaxCalibrationBufferMinutes(), *in.MaxCalibrationBufferMinutes)
		ev := evs[0].(PromisePolicyPublished)
		eq(t, "event target", ev.TargetOnTimeProbabilityPercent, in.TargetOnTimeProbabilityPercent)
		eq(t, "event min obs", ev.MinCalibrationObservations, in.MinCalibrationObservations)
		eq(t, "event window", ev.CalibrationWindowDays, in.CalibrationWindowDays)
		eq(t, "event max buffer", ev.MaxCalibrationBufferMinutes, *in.MaxCalibrationBufferMinutes)
	}
}

func TestOmittedCalibrationFieldsTakeTheirDefaultsAndZeroCapSticks(t *testing.T) {
	p, _, err := Publish(0, valid(), t0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "target", p.TargetOnTimeProbabilityPercent(), DefaultTargetOnTimePercent)
	eq(t, "min obs", p.MinCalibrationObservations(), DefaultMinCalibrationObservations)
	eq(t, "window", p.CalibrationWindowDays(), DefaultCalibrationWindowDays)
	eq(t, "omitted cap", p.MaxCalibrationBufferMinutes(), DefaultMaxCalibrationBuffer)
	in := valid()
	in.MaxCalibrationBufferMinutes = intp(0)
	off, _, err := Publish(0, in, t0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "an explicit 0 cap is not an omitted one", off.MaxCalibrationBufferMinutes(), 0)
}

func TestCalibrationSurvivesSnapshotRestore(t *testing.T) {
	in := valid()
	in.TargetOnTimeProbabilityPercent, in.MinCalibrationObservations = 90, 25
	in.CalibrationWindowDays, in.MaxCalibrationBufferMinutes = 14, intp(0)
	p, _, _ := Publish(0, in, t0)
	got, err := Restore(p.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "target", got.TargetOnTimeProbabilityPercent(), 90)
	eq(t, "min obs", got.MinCalibrationObservations(), 25)
	eq(t, "window", got.CalibrationWindowDays(), 14)
	eq(t, "cap 0 round-trips", got.MaxCalibrationBufferMinutes(), 0)
}

func TestSnapshotBeforeTheADRRestoresWithDefaults(t *testing.T) {
	s := Snapshot{Version: 3, OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100, PublishedAt: t0}
	got, err := Restore(s)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "target", got.TargetOnTimeProbabilityPercent(), DefaultTargetOnTimePercent)
	eq(t, "min obs", got.MinCalibrationObservations(), DefaultMinCalibrationObservations)
	eq(t, "window", got.CalibrationWindowDays(), DefaultCalibrationWindowDays)
	eq(t, "cap", got.MaxCalibrationBufferMinutes(), DefaultMaxCalibrationBuffer)
	s.TargetOnTimeProbabilityPercent = 100
	if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("a stored value out of range is corrupt: %v", err)
	}
}
