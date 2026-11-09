package usecases_test

import (
	"testing"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// flaggedLate makes the promise PREDICTED_LATE with the pickup arrived and the
// linehaul (expected 23:30 on the 22nd) running late at the given instant.
func flaggedLate(t *testing.T, e *env, id string, hour, minute int) {
	t.Helper()
	e.arrived("ld-1", "dep-AUS3-1800", "N-SAU1", at(22, 19, 10))
	e.clock.Set(at(23, hour, minute))
	eq(t, "flagged", e.flagLate(100), 1)
	eq(t, "reason", e.reason(id), "PREDICTED_LATE")
}

func TestRepriceAtRiskLeavesAReachablePredictedLatePromiseAlone(t *testing.T) {
	e, _, id := newLateEnv(t)
	// two hours and a half behind: the load reaches the station by 02:00, the
	// 06:00 induct cutoff of the wave still holds, so does the 21:00 window
	flaggedLate(t, e, id, 2, 0)
	n, err := e.svc.RepriceAtRisk(ctx, 10)
	must(t, err)
	eq(t, "nothing examined", n, 0)
	eq(t, "still AT_RISK", e.state(id), "AT_RISK")
	names := e.eventNames()
	eq(t, "not revised", count(names, "PromiseRevised"), 0)
	eq(t, "not unkeepable", count(names, "PromiseUnkeepable"), 0)
	// and again: idempotent
	n, err = e.svc.RepriceAtRisk(ctx, 10)
	must(t, err)
	eq(t, "second pass", n, 0)
	eq(t, "events unchanged", len(e.eventNames()), len(names))
}

func TestRepriceAtRiskRevisesOnceTheWindowIsNoLongerReachable(t *testing.T) {
	e, _, id := newLateEnv(t)
	flaggedLate(t, e, id, 2, 0)
	n, err := e.svc.RepriceAtRisk(ctx, 10)
	must(t, err)
	eq(t, "reachable at 02:00", n, 0)
	// a pickup, a linehaul and a wave one day later
	e.departure("dep-AUS3-1800-b", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(23, 18, 0), at(23, 19, 15), 40)
	e.departure("dep-SAU1-DAU1-2200-b", "lane-2", "LINEHAUL", "N-SAU1", "N-DAU1", at(23, 22, 0), at(23, 23, 30), 140)
	e.wave("W-DAU1-1024-A", at(24, 6, 0), at(24, 9, 0), at(24, 21, 0), 320)
	// 08:00: the load is eight and a half hours behind, past the induct cutoff
	e.clock.Set(at(23, 8, 0))
	n, err = e.svc.RepriceAtRisk(ctx, 10)
	must(t, err)
	eq(t, "examined", n, 1)
	got, err := e.svc.GetPromise(ctx, id)
	must(t, err)
	eq(t, "re-promised", got.State, "CONFIRMED")
	eq(t, "one revision", len(got.Revisions), 1)
	eq(t, "revision reason", got.Revisions[0].Reason, "PREDICTED_LATE")
	eq(t, "PromiseRevised raised", count(e.eventNames(), "PromiseRevised"), 1)
}

func TestRepriceAtRiskLimitCountsOnlyThePromisesItRevises(t *testing.T) {
	e, _, a := newLateEnv(t)
	b := e.confirmedNextDay("ord-2", 1)
	// b is at risk for another reason, with its node suspended: no repair yet
	must(t, e.svc.OnFulfillmentLocked(ctx, e.meta("FulfillmentLocked"), usecases.FulfillmentLocked{AssignmentID: "asg-b", OrderID: "ord-2", GroupNo: 1, PromiseID: b, NodeID: "N-AUS3"}))
	must(t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeSuspended"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, readmodel.NodeSuspended))
	must(t, e.svc.OnPackageException(ctx, e.meta("PackageExceptionRaised"), usecases.PackageException{AssignmentID: "asg-b", TrackingNumber: "TRK-b"}))
	eq(t, "b is AT_RISK", e.state(b), "AT_RISK")
	// a (made first, so listed first) is flagged PREDICTED_LATE and reachable
	e.arrived("ld-1", "dep-AUS3-1800", "N-SAU1", at(22, 19, 10))
	e.clock.Set(at(23, 2, 0))
	eq(t, "only a is CONFIRMED", e.flagLate(100), 1)
	eq(t, "a", e.reason(a), "PREDICTED_LATE")
	// the node is back and a later path exists: b can be repaired
	must(t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, readmodel.NodeActive))
	e.departure("dep-AUS3-1800-b", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(23, 18, 0), at(23, 19, 15), 40)
	e.departure("dep-SAU1-DAU1-2200-b", "lane-2", "LINEHAUL", "N-SAU1", "N-DAU1", at(23, 22, 0), at(23, 23, 30), 140)
	e.wave("W-DAU1-1024-A", at(24, 6, 0), at(24, 9, 0), at(24, 21, 0), 320)
	n, err := e.svc.RepriceAtRisk(ctx, 1)
	must(t, err)
	eq(t, "one promise revised", n, 1)
	eq(t, "b was reached past the held a", e.state(b), "CONFIRMED")
	eq(t, "a is left alone", e.state(a), "AT_RISK")
}

func TestRepriceAtRiskRevisesAPredictedLatePromiseWhoseWavePathWasCancelled(t *testing.T) {
	e, _, id := newLateEnv(t)
	flaggedLate(t, e, id, 2, 0)
	// the held promise is not held against any other risk: a cancelled wave re-promises it
	must(t, e.svc.OnWaveCancelled(ctx, e.meta("DeliveryWaveCancelled"), usecases.WaveCancelled{WaveID: "W-DAU1-1023-A"}))
	eq(t, "revised by the cancellation", e.state(id), "CONFIRMED")
	eq(t, "PromiseRevised raised", count(e.eventNames(), "PromiseRevised"), 1)
}
