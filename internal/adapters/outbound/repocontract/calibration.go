package repocontract

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

var calibrationCases = []named{
	{"a fresh view has no segment", calibrationEmpty},
	{"judgements land in their own segment and read back oldest first", calibrationSegments},
	{"the window prunes on read and the ring keeps the latest 500", calibrationRingAndWindow},
	{"a recorded judgement commits and rolls back with its unit of work", calibrationUnitOfWork},
}

var (
	segNextDay = readmodel.CalibrationKey{Tier: shared.TierNextDay, Basis: shared.BasisScheduled, NodeGrade: search.GradeA}
	segTwoDay  = readmodel.CalibrationKey{Tier: shared.TierTwoDay, Basis: shared.BasisMeasured, NodeGrade: search.GradeProvisional}
)

func judged(lateBy int, at time.Time) readmodel.CalibrationObservation {
	return readmodel.CalibrationObservation{LateByMinutes: lateBy, ObservedAt: at}
}

func calibrationEmpty(t *testing.T, f Fixture) {
	got, err := f.Calibration.Segments(ctx, 28, T0)
	noErr(t, err)
	eq(t, "segments", len(got), 0)
}

func calibrationSegments(t *testing.T, f Fixture) {
	for i, lateBy := range []int{0, 45, 0, 120} {
		noErr(t, f.Calibration.RecordJudgement(ctx, segNextDay, judged(lateBy, T0.Add(time.Duration(i)*time.Hour))))
	}
	noErr(t, f.Calibration.RecordJudgement(ctx, segTwoDay, judged(30, T0)))
	got, err := f.Calibration.Segments(ctx, 28, T0.Add(24*time.Hour))
	noErr(t, err)
	eq(t, "segments", len(got), 2)
	want := []int{0, 45, 0, 120}
	eq(t, "observations", len(got[segNextDay]), len(want))
	for i, w := range want {
		eq(t, "lateBy", got[segNextDay][i], w)
	}
	eq(t, "other segment", len(got[segTwoDay]), 1)
	eq(t, "other segment lateBy", got[segTwoDay][0], 30)
}

func calibrationRingAndWindow(t *testing.T, f Fixture) {
	// 520 judgements an hour apart: only the latest 500 stay
	for i := range 520 {
		noErr(t, f.Calibration.RecordJudgement(ctx, segNextDay, judged(i, T0.Add(time.Duration(i)*time.Minute))))
	}
	now := T0.Add(10 * time.Hour)
	got, err := f.Calibration.Segments(ctx, 28, now)
	noErr(t, err)
	eq(t, "the ring is capped", len(got[segNextDay]), readmodel.MaxCalibrationObservations)
	eq(t, "the oldest 20 were dropped", got[segNextDay][0], 20)
	eq(t, "the newest is last", got[segNextDay][len(got[segNextDay])-1], 519)
	// a window of one day, read two days after the last judgement, sees none
	got, err = f.Calibration.Segments(ctx, 1, T0.Add(48*time.Hour))
	noErr(t, err)
	eq(t, "nothing inside a 1-day window two days later", len(got), 0)
	// 27 days after, the 28-day window still holds them
	got, err = f.Calibration.Segments(ctx, 28, T0.Add(27*24*time.Hour))
	noErr(t, err)
	eq(t, "inside the 28-day window", len(got[segNextDay]), readmodel.MaxCalibrationObservations)
}

func calibrationUnitOfWork(t *testing.T, f Fixture) {
	err := f.UoW.Do(ctx, func(c context.Context) error {
		noErr(t, f.Calibration.RecordJudgement(c, segNextDay, judged(15, T0)))
		return errBoom
	})
	wantErr(t, err, errBoom)
	got, err := f.Calibration.Segments(ctx, 28, T0)
	noErr(t, err)
	eq(t, "a rolled-back judgement leaves no observation", len(got), 0)
	noErr(t, f.UoW.Do(ctx, func(c context.Context) error { return f.Calibration.RecordJudgement(c, segNextDay, judged(15, T0)) }))
	got, err = f.Calibration.Segments(ctx, 28, T0)
	noErr(t, err)
	eq(t, "a committed one stays", len(got[segNextDay]), 1)
}
