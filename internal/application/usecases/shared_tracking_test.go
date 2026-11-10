package usecases_test

import (
	"testing"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// Shared tracking numbers (ADR 0025): a merged package is shipped under the
// tracking number of every member, each member keeping its own assignment,
// order and promise.

// sharedPackage is a member of a merged package: the promise, its assignment.
type sharedPackage struct{ promiseID, assignmentID string }

// lockAndShipShared locks and ships the assignment of one member under tracking.
func lockAndShipShared(t *testing.T, e *env, m sharedPackage, order, tracking string) {
	t.Helper()
	must(t, e.svc.OnFulfillmentLocked(ctx, e.meta("FulfillmentLocked"), usecases.FulfillmentLocked{AssignmentID: m.assignmentID, OrderID: order, GroupNo: 1, PromiseID: m.promiseID, NodeID: "N-AUS3"}))
	must(t, e.svc.OnFulfillmentShipped(ctx, e.meta("FulfillmentShipped"), usecases.FulfillmentShipped{AssignmentID: m.assignmentID, TrackingNumbers: []string{tracking}}))
}

// twoMembersOnePackage confirms two next-day promises and ships both under TRK-S.
func twoMembersOnePackage(t *testing.T) (*env, sharedPackage, sharedPackage) {
	t.Helper()
	e := newEnv(t)
	a := sharedPackage{promiseID: e.confirmedNextDay("ord-1", 1), assignmentID: "asg-1"}
	b := sharedPackage{promiseID: e.confirmedNextDay("ord-2", 1), assignmentID: "asg-2"}
	lockAndShipShared(t, e, a, "ord-1", "TRK-S")
	lockAndShipShared(t, e, b, "ord-2", "TRK-S")
	return e, a, b
}

func TestOneDeliveryOfASharedPackageKeepsEverySharingPromise(t *testing.T) {
	e, a, b := twoMembersOnePackage(t)
	// the event names only the carrier's assignment; the follower is found by tracking number
	m := e.meta("PackageDelivered")
	ev := usecases.PackageDelivered{TrackingNumber: "TRK-S", AssignmentID: "asg-1", DeliveredAt: at(23, 12, 0)}
	must(t, e.svc.OnPackageDelivered(ctx, m, ev))
	eq(t, "carrier", e.state(a.promiseID), "KEPT")
	eq(t, "follower", e.state(b.promiseID), "KEPT")
	eq(t, "one verdict per promise", count(e.eventNames(), "PromiseKept"), 2)
	must(t, e.svc.OnPackageDelivered(ctx, m, ev))                          // the same CloudEvents id again: the inbox skips it
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), ev)) // the same fact under a new id judges nothing new
	eq(t, "replay", count(e.eventNames(), "PromiseKept"), 2)
}

func TestOneLostOrReturnedSharedPackageBreaksEverySharingPromise(t *testing.T) {
	for cause, trigger := range map[string]func(*env){
		"LOST": func(e *env) {
			must(t, e.svc.OnPackageLost(ctx, e.meta("PackageLost"), usecases.PackageLost{TrackingNumber: "TRK-S", DeclaredAt: at(23, 10, 0)}))
		},
		"RETURNED": func(e *env) {
			must(t, e.svc.OnPackageReturned(ctx, e.meta("PackageReturned"), usecases.PackageReturned{TrackingNumber: "TRK-S", ReturnedAt: at(23, 10, 0)}))
		},
	} {
		e, a, b := twoMembersOnePackage(t)
		trigger(e)
		for _, id := range []string{a.promiseID, b.promiseID} {
			if s, got := judgement(t, e, id); s != "BROKEN" || got != cause {
				t.Errorf("%s: state %s cause %s", cause, s, got)
			}
		}
		eq(t, cause+" events", count(e.eventNames(), "PromiseBroken"), 2)
	}
}

func TestSharingPromisesAreJudgedOnTheirOwnWindows(t *testing.T) {
	e := newEnv(t)
	// the later window (ends 21:00 local) is promised first, then an earlier
	// wave appears and the second promise lands on it (ends 12:00 local)
	later := sharedPackage{promiseID: e.confirmedNextDay("ord-2", 1), assignmentID: "asg-2"}
	e.wave("W-early", at(23, 5, 0), at(23, 7, 0), at(23, 12, 0), 100)
	earlier := sharedPackage{promiseID: e.confirmedNextDay("ord-1", 1), assignmentID: "asg-1"}
	pe, _ := e.svc.GetPromise(ctx, earlier.promiseID)
	pl, _ := e.svc.GetPromise(ctx, later.promiseID)
	if !pe.Terms.DeliverByLatest.Before(pl.Terms.DeliverByLatest) {
		t.Fatalf("setup: windows end %v and %v, want the first promise to end later", pe.Terms.DeliverByLatest, pl.Terms.DeliverByLatest)
	}
	lockAndShipShared(t, e, earlier, "ord-1", "TRK-S")
	lockAndShipShared(t, e, later, "ord-2", "TRK-S")
	// one delivery at 13:00 local: after the earlier window, inside the later one
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-S", AssignmentID: "asg-1", DeliveredAt: at(23, 13, 0)}))
	if s, cause := judgement(t, e, earlier.promiseID); s != "BROKEN" || cause != "LATE" {
		t.Fatalf("earlier promise: state %s cause %s, want BROKEN LATE", s, cause)
	}
	eq(t, "later promise", e.state(later.promiseID), "KEPT")
}

func TestAMemberThatShipsAfterTheDeliveryIsJudgedWhenItsShipmentArrives(t *testing.T) {
	e := newEnv(t)
	a := sharedPackage{promiseID: e.confirmedNextDay("ord-1", 1), assignmentID: "asg-1"}
	b := sharedPackage{promiseID: e.confirmedNextDay("ord-2", 1), assignmentID: "asg-2"}
	lockAndShipShared(t, e, a, "ord-1", "TRK-S")
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-S", AssignmentID: "asg-1", DeliveredAt: at(23, 12, 0)}))
	eq(t, "carrier", e.state(a.promiseID), "KEPT")
	eq(t, "follower not shipped yet", e.state(b.promiseID), "CONFIRMED")
	lockAndShipShared(t, e, b, "ord-2", "TRK-S")
	eq(t, "follower judged by its own shipment", e.state(b.promiseID), "KEPT")
}

func TestARiskOnASharedPackageFlagsEverySharingPromise(t *testing.T) {
	for name, fire := range map[string]func(*env){
		"induction deferred, tracking number only": func(e *env) {
			must(t, e.svc.OnInductionDeferred(ctx, e.meta("PackageInductionDeferred"), usecases.InductionDeferred{TrackingNumber: "TRK-S"}))
		},
		"package exception on the carrier": func(e *env) {
			must(t, e.svc.OnPackageException(ctx, e.meta("PackageExceptionRaised"), usecases.PackageException{TrackingNumber: "TRK-S", AssignmentID: "asg-1"}))
		},
	} {
		e, a, b := twoMembersOnePackage(t)
		fire(e)
		_, _ = a, b
		eq(t, name, count(e.eventNames(), "PromiseAtRisk"), 2) // each promise is flagged once, then re-promised
	}
}
