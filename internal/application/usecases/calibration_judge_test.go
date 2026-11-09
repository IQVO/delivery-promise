package usecases_test

import (
	"testing"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// segments reads every calibration segment of the store, the way a search does.
func (e *env) segments() map[readmodel.CalibrationKey][]int {
	e.t.Helper()
	got, err := e.store.Calibration().Segments(ctx, 28, e.clock.Now())
	must(e.t, err)
	return got
}

var nextDayScheduled = func(grade search.Grade) readmodel.CalibrationKey {
	return readmodel.CalibrationKey{Tier: shared.TierNextDay, Basis: shared.BasisScheduled, NodeGrade: grade}
}

func TestAKeptPromiseRecordsZeroMinutesInItsSegment(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	lockAndShip(t, e, id, "TRK-1")
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", DeliveredAt: at(23, 12, 0)}))
	got := e.segments()
	// the worked example's node has no scorecard yet: the PROVISIONAL segment
	if len(got) != 1 || len(got[nextDayScheduled(search.GradeProvisional)]) != 1 || got[nextDayScheduled(search.GradeProvisional)][0] != 0 {
		t.Fatalf("segments = %v, want one 0-minute observation in NEXT_DAY/SCHEDULED/PROVISIONAL", got)
	}
}

func TestALatePromiseRecordsItsLateByAndTheGradeOfItsNode(t *testing.T) {
	e := newEnv(t)
	must(t, e.svc.OnScorecardUpdated(ctx, e.meta("NodeScorecardUpdated"), usecases.ScorecardUpdated{
		NodeID: "N-AUS3", Window: usecases.ScorecardWindow, Grade: "B", Observations: 80, ComputedAt: at(22, 12, 0),
	}))
	id := e.confirmedNextDay("ord-1", 1)
	lockAndShip(t, e, id, "TRK-1")
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", DeliveredAt: at(23, 22, 0)}))
	got := e.segments()[nextDayScheduled(search.GradeB)]
	if len(got) != 1 || got[0] != 60 { // the window ends 21:00; delivered 22:00
		t.Fatalf("segment B = %v, want one observation of 60 minutes", got)
	}
}

func TestALostOrReturnedPackageIsNotALatenessObservation(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	lockAndShip(t, e, id, "TRK-1")
	must(t, e.svc.OnPackageLost(ctx, e.meta("PackageLost"), usecases.PackageLost{TrackingNumber: "TRK-1", DeclaredAt: at(24, 10, 0)}))
	if state := e.state(id); state != "BROKEN" {
		t.Fatalf("state = %s", state)
	}
	if got := e.segments(); len(got) != 0 {
		t.Fatalf("a loss must not count as an on-time or late delivery, segments = %v", got)
	}
}

func TestAnObservationIsRecordedOncePerJudgement(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	lockAndShip(t, e, id, "TRK-1")
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", DeliveredAt: at(23, 12, 0)}))
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", DeliveredAt: at(23, 12, 0)}))
	if n := len(e.segments()[nextDayScheduled(search.GradeProvisional)]); n != 1 {
		t.Fatalf("a second delivery of the same fact judged nothing new, observations = %d", n)
	}
}
