package usecases_test

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

const nextDayBook = "N-AUS3:dep-AUS3-1800"

func TestCancelPromiseOnRequestReleasesTheConfirmedHold(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	eq(t, "confirmed before", e.bookOf(nextDayBook).Confirmed(), 1)
	got, err := e.svc.CancelPromise(ctx, id)
	must(t, err)
	eq(t, "state", got.State, "CANCELLED")
	eq(t, "reason", got.CancelReason, usecases.CancelReasonRequested)
	eq(t, "order kept on the view", got.OrderID, "ord-1")
	eq(t, "confirmed released", e.bookOf(nextDayBook).Confirmed(), 0)
	eq(t, "provisional untouched", e.bookOf(nextDayBook).Provisional(), 0)
	eq(t, "PromiseCancelled", count(e.eventNames(), "PromiseCancelled"), 1)
	stored, err := e.svc.GetPromise(ctx, id)
	must(t, err)
	eq(t, "persisted", stored.State, "CANCELLED")
}

func TestCancelPromiseOnAnAlreadyCancelledPromiseIsANoOp(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	first, err := e.svc.CancelPromise(ctx, id)
	must(t, err)
	events := len(e.store.Outbox().Events())
	again, err := e.svc.CancelPromise(ctx, id)
	must(t, err)
	eq(t, "same state", again.State, "CANCELLED")
	eq(t, "same reason", again.CancelReason, first.CancelReason)
	eq(t, "no second event", len(e.store.Outbox().Events()), events)
	eq(t, "capacity not released twice", e.bookOf(nextDayBook).Confirmed(), 0)

	// a promise cancelled by the order keeps its own reason and is also a no-op
	other := newEnv(t)
	oid := other.confirmedNextDay("ord-9", 1)
	must(t, other.svc.OnCustomerOrderCancelled(ctx, other.meta("CustomerOrderCancelled"), usecases.CustomerOrderCancelled{OrderID: "ord-9", Reason: "CUSTOMER_REQUEST"}))
	got, err := other.svc.CancelPromise(ctx, oid)
	must(t, err)
	eq(t, "order reason kept", got.CancelReason, "CUSTOMER_REQUEST")
}

func TestCancelPromiseCancelsAnAtRiskPromise(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	must(t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeSuspended"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, readmodel.NodeSuspended))
	must(t, e.svc.OnDepartureCancelled(ctx, e.meta("DepartureCancelled"), usecases.DepartureCancelled{DepartureID: "dep-AUS3-1800"}))
	eq(t, "at risk", e.state(id), "AT_RISK")
	got, err := e.svc.CancelPromise(ctx, id)
	must(t, err)
	eq(t, "state", got.State, "CANCELLED")
	eq(t, "confirmed released", e.bookOf(nextDayBook).Confirmed(), 0)
}

func TestCancelPromiseRefusesEveryOtherStateAsIllegalTransition(t *testing.T) {
	refused := func(t *testing.T, e *env, id string) {
		t.Helper()
		_, err := e.svc.CancelPromise(ctx, id)
		if !errors.Is(err, shared.ErrIllegalTransition) {
			t.Fatalf("want illegal transition, got %v", err)
		}
		if errors.Is(err, promise.ErrPromiseClosed) {
			t.Fatalf("the closed sentinel must not leak (problem slug stays illegal-transition): %v", err)
		}
	}
	t.Run("kept", func(t *testing.T) {
		e := newEnv(t)
		id := e.confirmedNextDay("ord-1", 1)
		lockAndShip(t, e, id, "TRK-1")
		must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", AssignmentID: "asg-1", DeliveredAt: at(23, 12, 0)}))
		eq(t, "kept", e.state(id), "KEPT")
		refused(t, e, id)
		eq(t, "still kept", e.state(id), "KEPT")
	})
	t.Run("broken", func(t *testing.T) {
		e := newEnv(t)
		id := e.confirmedNextDay("ord-1", 1)
		lockAndShip(t, e, id, "TRK-1")
		must(t, e.svc.OnPackageLost(ctx, e.meta("PackageLost"), usecases.PackageLost{TrackingNumber: "TRK-1", DeclaredAt: at(23, 10, 0)}))
		eq(t, "broken", e.state(id), "BROKEN")
		refused(t, e, id)
	})
	t.Run("released", func(t *testing.T) {
		e := newEnv(t)
		p := e.make(offerOf(t, e.quote(), "NEXT_DAY").OfferID).Promise
		e.clock.Advance(15 * time.Minute)
		n, err := e.svc.ReleaseExpiredHolds(ctx, 10)
		must(t, err)
		eq(t, "released", n, 1)
		refused(t, e, p.PromiseID)
	})
	t.Run("provisional", func(t *testing.T) {
		e := newEnv(t)
		p := e.make(offerOf(t, e.quote(), "NEXT_DAY").OfferID).Promise
		refused(t, e, p.PromiseID)
		eq(t, "hold untouched", e.bookOf(nextDayBook).Provisional(), 1)
		eq(t, "still provisional", e.state(p.PromiseID), "PROVISIONAL")
	})
}

func TestCancelPromiseOfAnUnknownPromiseIsNotFound(t *testing.T) {
	e := newEnv(t)
	if _, err := e.svc.CancelPromise(ctx, "prm-nope"); !errors.Is(err, ports.ErrNotFound) {
		t.Fatalf("want not found, got %v", err)
	}
}
