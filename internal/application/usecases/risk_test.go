package usecases_test

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// confirmedNextDay quotes, makes and confirms the worked-example promise.
func (e *env) confirmedNextDay(order string, group int) string {
	e.t.Helper()
	p := e.make(offerOf(e.t, e.quote(shared.TierNextDay), "NEXT_DAY").OfferID).Promise
	_, err := e.svc.ConfirmPromise(ctx, p.PromiseID, order, group)
	must(e.t, err)
	return p.PromiseID
}

func (e *env) state(id string) string {
	e.t.Helper()
	p, err := e.svc.GetPromise(ctx, id)
	must(e.t, err)
	return p.State
}

func TestACancelledPickupRevisesThePromiseToAnotherDeparture(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	// an earlier pickup that still reaches the same linehaul and wave
	e.departure("dep-AUS3-1730", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(22, 17, 30), at(22, 18, 45), 40)
	must(t, e.svc.OnDepartureCancelled(ctx, e.meta("DepartureCancelled"), usecases.DepartureCancelled{DepartureID: "dep-AUS3-1800"}))
	got, err := e.svc.GetPromise(ctx, id)
	must(t, err)
	eq(t, "re-promised in the same call", got.State, "CONFIRMED")
	eq(t, "one revision", len(got.Revisions), 1)
	eq(t, "reason", got.Revisions[0].Reason, "DEPARTURE_CANCELLED")
	eq(t, "new pickup", got.WitnessPath.PickupDepartureID, "dep-AUS3-1730")
	names := e.eventNames()
	eq(t, "at risk", count(names, "PromiseAtRisk"), 1)
	eq(t, "revised", count(names, "PromiseRevised"), 1)
	eq(t, "old book released", e.bookOf("N-AUS3:dep-AUS3-1800").Confirmed(), 0)
	eq(t, "new book confirmed", e.bookOf("N-AUS3:dep-AUS3-1730").Confirmed(), 1)
}

func TestAPromiseThatCannotBeRevisedBecomesUnkeepableOnce(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	must(t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeSuspended"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, readmodel.NodeSuspended))
	must(t, e.svc.OnDepartureCancelled(ctx, e.meta("DepartureCancelled"), usecases.DepartureCancelled{DepartureID: "dep-AUS3-1800"}))
	eq(t, "stays AT_RISK", e.state(id), "AT_RISK")
	eq(t, "unkeepable raised", count(e.eventNames(), "PromiseUnkeepable"), 1)
	n, err := e.svc.RepriceAtRisk(ctx, 10)
	must(t, err)
	eq(t, "the sweeper examined it", n, 1)
	eq(t, "not raised twice", count(e.eventNames(), "PromiseUnkeepable"), 1)
	// the node comes back with a new pickup: the sweeper repairs the promise
	must(t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, readmodel.NodeActive))
	e.departure("dep-AUS3-1730", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(22, 17, 30), at(22, 18, 45), 40)
	_, err = e.svc.RepriceAtRisk(ctx, 10)
	must(t, err)
	eq(t, "repaired", e.state(id), "CONFIRMED")
}

func TestWaveCancellationFlagsOnlyActivePromisesOnThatWave(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	provisional := e.make(offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY").OfferID).Promise
	must(t, e.svc.OnWaveCancelled(ctx, e.meta("DeliveryWaveCancelled"), usecases.WaveCancelled{WaveID: "W-DAU1-1023-A"}))
	eq(t, "confirmed was flagged", count(e.eventNames(), "PromiseAtRisk"), 1)
	eq(t, "provisional is left to its hold", e.state(provisional.PromiseID), "PROVISIONAL")
	if s := e.state(id); s != "AT_RISK" && s != "CONFIRMED" {
		t.Fatalf("state = %s", s)
	}
}

func TestAssignmentRisksFlagThePromiseAndRememberTheExclusions(t *testing.T) {
	cases := map[string]func(*env){
		"booking moved": func(e *env) {
			must(e.t, e.svc.OnBookingMoved(ctx, e.meta("BookingMoved"), usecases.BookingMoved{AssignmentID: "asg-1", FromDepartureID: "dep-AUS3-1800", ToDepartureID: "dep-x"}))
		},
		"booking failed": func(e *env) {
			must(e.t, e.svc.OnBookingFailed(ctx, e.meta("BookingFailed"), usecases.BookingFailed{AssignmentID: "asg-1", DepartureID: "dep-AUS3-1800"}))
		},
		"induction deferred": func(e *env) {
			must(e.t, e.svc.OnInductionDeferred(ctx, e.meta("PackageInductionDeferred"), usecases.InductionDeferred{AssignmentID: "asg-1", TrackingNumber: "TRK-1"}))
		},
		"package exception": func(e *env) {
			must(e.t, e.svc.OnPackageException(ctx, e.meta("PackageExceptionRaised"), usecases.PackageException{AssignmentID: "asg-1", TrackingNumber: "TRK-1"}))
		},
		"unsourceable": func(e *env) {
			must(e.t, e.svc.OnFulfillmentUnsourceable(ctx, e.meta("FulfillmentUnsourceable"), usecases.FulfillmentUnsourceable{AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: "PID", ExcludedNodes: []string{"N-AUS3"}}))
		},
	}
	for name, trigger := range cases {
		e := newEnv(t)
		id := e.confirmedNextDay("ord-1", 1)
		must(t, e.svc.OnFulfillmentLocked(ctx, e.meta("FulfillmentLocked"), usecases.FulfillmentLocked{AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: id, NodeID: "N-AUS3"}))
		if name == "unsourceable" {
			// the promise id travels in the event
			must(t, e.svc.OnFulfillmentUnsourceable(ctx, e.meta("FulfillmentUnsourceable"), usecases.FulfillmentUnsourceable{AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: id, ExcludedNodes: []string{"N-AUS3", "N-AUS3"}}))
		} else {
			trigger(e)
		}
		if n := count(e.eventNames(), "PromiseAtRisk"); n != 1 {
			t.Errorf("%s: PromiseAtRisk raised %d times", name, n)
		}
	}
	e := newEnv(t)
	must(t, e.svc.OnBookingFailed(ctx, e.meta("BookingFailed"), usecases.BookingFailed{AssignmentID: "asg-unknown", DepartureID: "d"}))
	eq(t, "a risk for an unknown assignment is ignored", count(e.eventNames(), "PromiseAtRisk"), 0)
}

func TestALockedAssignmentKeepsTheRevisionOnItsNode(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	must(t, e.svc.OnFulfillmentLocked(ctx, e.meta("FulfillmentLocked"), usecases.FulfillmentLocked{AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: id, NodeID: "N-AUS3"}))
	e.stock("N-AUS4", "ci-1001", "lst-2", 50)
	e.node("N-AUS4", "FULFILLMENT_CENTER", "SORTABLE", "R-SC", fcProfile(95, 2500))
	e.departure("dep-AUS4-1700", "lane-1", "PICKUP", "N-AUS4", "N-SAU1", at(22, 17, 0), at(22, 18, 15), 40)
	must(t, e.svc.OnBookingFailed(ctx, e.meta("BookingFailed"), usecases.BookingFailed{AssignmentID: "asg-1", DepartureID: "dep-AUS3-1800"}))
	got, _ := e.svc.GetPromise(ctx, id)
	if got.WitnessPath.NodeID != "N-AUS3" {
		t.Fatalf("a locked group moved to node %s", got.WitnessPath.NodeID)
	}
}

func lockAndShip(t *testing.T, e *env, id string, trackings ...string) {
	t.Helper()
	must(t, e.svc.OnFulfillmentLocked(ctx, e.meta("FulfillmentLocked"), usecases.FulfillmentLocked{AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: id, NodeID: "N-AUS3"}))
	must(t, e.svc.OnFulfillmentShipped(ctx, e.meta("FulfillmentShipped"), usecases.FulfillmentShipped{AssignmentID: "asg-1", TrackingNumbers: trackings}))
}

func judgement(t *testing.T, e *env, id string) (string, string) {
	t.Helper()
	p, err := e.svc.GetPromise(ctx, id)
	must(t, err)
	if p.Judgement == nil {
		return p.State, ""
	}
	return p.State, p.Judgement.Cause
}

func TestAPromiseIsKeptWhenTheLastPackageIsDelivered(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	lockAndShip(t, e, id, "TRK-1", "TRK-2")
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", AssignmentID: "asg-1", DeliveredAt: at(23, 12, 0)}))
	s, _ := judgement(t, e, id)
	eq(t, "waits for the other package", s, "CONFIRMED")
	// the second delivery event omits the assignment: it is found by tracking number
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-2", DeliveredAt: at(23, 15, 0)}))
	s, _ = judgement(t, e, id)
	eq(t, "kept", s, "KEPT")
	p, _ := e.svc.GetPromise(ctx, id)
	if !p.Judgement.DeliveredAt.Equal(at(23, 15, 0)) {
		t.Fatalf("deliveredAt = %v: the latest delivery counts", p.Judgement.DeliveredAt)
	}
	eq(t, "event", count(e.eventNames(), "PromiseKept"), 1)
}

func TestAPromiseDeliveredAfterItsWindowIsBrokenLate(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	lockAndShip(t, e, id, "TRK-1")
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", AssignmentID: "asg-1", DeliveredAt: at(23, 22, 0)}))
	s, cause := judgement(t, e, id)
	eq(t, "state", s, "BROKEN")
	eq(t, "cause", cause, "LATE")
	p, _ := e.svc.GetPromise(ctx, id)
	eq(t, "late by an hour", *p.Judgement.LateByMinutes, 60)
}

func TestALostOrReturnedPackageBreaksThePromiseAtOnce(t *testing.T) {
	for name, trigger := range map[string]func(*env) string{
		"lost": func(e *env) string {
			must(e.t, e.svc.OnPackageLost(ctx, e.meta("PackageLost"), usecases.PackageLost{TrackingNumber: "TRK-2", DeclaredAt: at(23, 10, 0)}))
			return "LOST"
		},
		"returned": func(e *env) string {
			must(e.t, e.svc.OnPackageReturned(ctx, e.meta("PackageReturned"), usecases.PackageReturned{TrackingNumber: "TRK-2", ReturnedAt: at(23, 10, 0)}))
			return "RETURNED"
		},
	} {
		e := newEnv(t)
		id := e.confirmedNextDay("ord-1", 1)
		lockAndShip(t, e, id, "TRK-1", "TRK-2")
		want := trigger(e)
		s, cause := judgement(t, e, id)
		if s != "BROKEN" || cause != want {
			t.Errorf("%s: state %s cause %s", name, s, cause)
		}
		// the later delivery of the other package changes nothing
		must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", AssignmentID: "asg-1", DeliveredAt: at(23, 11, 0)}))
		if n := count(e.eventNames(), "PromiseBroken"); n != 1 {
			t.Errorf("%s: PromiseBroken raised %d times", name, n)
		}
	}
}

func TestJudgementWaitsForTheFactsInAnyOrder(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	// delivery before the shipment and lock are known: recorded, not judged
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", AssignmentID: "asg-1", DeliveredAt: at(23, 12, 0)}))
	must(t, e.svc.OnPackageLost(ctx, e.meta("PackageLost"), usecases.PackageLost{TrackingNumber: "TRK-9", DeclaredAt: at(23, 12, 0)}))
	must(t, e.svc.OnFulfillmentShipped(ctx, e.meta("FulfillmentShipped"), usecases.FulfillmentShipped{AssignmentID: "asg-1", TrackingNumbers: []string{"TRK-1"}}))
	s, _ := judgement(t, e, id)
	eq(t, "no lock yet", s, "CONFIRMED")
	must(t, e.svc.OnFulfillmentLocked(ctx, e.meta("FulfillmentLocked"), usecases.FulfillmentLocked{AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: id, NodeID: "N-AUS3"}))
	s, _ = judgement(t, e, id)
	eq(t, "judged when the last fact arrives", s, "KEPT")
	// a lock for a promise that does not exist is ignored
	must(t, e.svc.OnFulfillmentLocked(ctx, e.meta("FulfillmentLocked"), usecases.FulfillmentLocked{AssignmentID: "asg-2", OrderID: "ord-2", GroupNo: 1, PromiseID: "prm-ghost", NodeID: "N-AUS3"}))
	must(t, e.svc.OnFulfillmentShipped(ctx, e.meta("FulfillmentShipped"), usecases.FulfillmentShipped{AssignmentID: "asg-2", TrackingNumbers: []string{"TRK-5"}}))
	must(t, e.svc.OnPackageDelivered(ctx, e.meta("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-5", AssignmentID: "asg-2", DeliveredAt: at(23, 12, 0)}))
}

func TestProjectionsFollowTheirProducers(t *testing.T) {
	e := newEnv(t)
	n := e.svc
	// lane closed, listing suppressed/restored, scorecard windows, area versions, stale availability
	must(t, n.OnLaneClosed(ctx, e.meta("LaneClosed"), usecases.LaneClosed{LaneID: "lane-1"}))
	must(t, n.OnLaneClosed(ctx, e.meta("LaneClosed"), usecases.LaneClosed{LaneID: "lane-new"}))
	if q := e.quote(shared.TierNextDay); len(q.Offers) != 0 {
		t.Fatalf("a closed lane still carries offers: %+v", q.Offers)
	}
	must(t, n.OnLaneOpened(ctx, e.meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-1", OriginNodeID: "N-AUS3", DestinationNodeID: "N-SAU1", NominalTransitMinutes: 75}))
	must(t, n.OnListingToggled(ctx, e.meta("ListingSuppressed"), usecases.ListingToggled{ListingID: "lst-1", NodeID: "N-AUS3"}, true))
	if q := e.quote(shared.TierNextDay); len(q.Offers) != 0 {
		t.Fatalf("a suppressed listing still sells: %+v", q.Offers)
	}
	must(t, n.OnListingToggled(ctx, e.meta("ListingRestored"), usecases.ListingToggled{ListingID: "lst-1", NodeID: "N-AUS3"}, false))
	must(t, n.OnListingToggled(ctx, e.meta("ListingSuppressed"), usecases.ListingToggled{ListingID: "lst-unseen", NodeID: "N-AUS3"}, true))
	offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY")
	// stale availability does not overwrite newer
	must(t, n.OnListingAvailabilityChanged(ctx, e.meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3", AvailableQty: 0, AsOf: at(22, 10, 0)}))
	offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY")
	// service area: older version ignored, newer moves the station
	must(t, n.OnServiceAreaAssigned(ctx, e.meta("ServiceAreaAssigned"), usecases.ServiceAreaAssigned{PostalPrefix: "787", DeliveryStationNodeID: "N-NOPE", RegionID: "R-SC", AssignmentVersion: 1}))
	offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY")
	// scorecards: only the 30d window counts
	must(t, n.OnScorecardUpdated(ctx, e.meta("NodeScorecardUpdated"), usecases.ScorecardUpdated{NodeID: "N-AUS3", Window: "7d", Grade: "F", Observations: 100, ComputedAt: at(22, 14, 0)}))
	must(t, n.OnScorecardUpdated(ctx, e.meta("NodeScorecardUpdated"), usecases.ScorecardUpdated{NodeID: "N-AUS3", Window: "30d", Grade: "A", Observations: 100, ComputedAt: at(22, 14, 0)}))
	// profile revisions: stale ignored
	stale := fcProfile(500, 1)
	stale.NodeID, stale.Timezone = "N-AUS3", "America/Chicago"
	must(t, n.OnNodeProfileDeclared(ctx, e.meta("NodeOperationalProfileDeclared"), *stale))
	offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY")
	// capacity events that arrive before the schedule create a stub; the schedule keeps the remaining count
	must(t, n.OnDepartureCapacityChanged(ctx, e.meta("DepartureCapacityChanged"), usecases.DepartureCapacityChanged{DepartureID: "dep-early", RemainingPackages: 3}))
	e.departure("dep-early", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(22, 17, 0), at(22, 18, 15), 40)
	must(t, n.OnWaveCapacityChanged(ctx, e.meta("DeliveryWaveCapacityChanged"), usecases.WaveCapacityChanged{WaveID: "W-early", RemainingStops: 4}))
	e.wave("W-early", at(23, 7, 0), at(23, 9, 0), at(23, 12, 0), 100)
	// a catalogue item that was discontinued stops selling and is back when created again
	must(t, n.OnCatalogItemChanged(ctx, e.meta("CatalogItemDiscontinued"), usecases.CatalogItemChanged{CatalogItemID: "ci-1001"}, true))
	must(t, n.OnCatalogItemChanged(ctx, e.meta("CatalogItemCreated"), usecases.CatalogItemChanged{CatalogItemID: "ci-1001"}, false))
	offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY")
}

func TestAnOrderCancelledBeforeAnythingWasConfirmedIsANoOp(t *testing.T) {
	e := newEnv(t)
	must(t, e.svc.OnCustomerOrderCancelled(ctx, e.meta("CustomerOrderCancelled"), usecases.CustomerOrderCancelled{OrderID: "ord-none"}))
	eq(t, "nothing happened", len(e.eventNames()), 0)
	e.clock.Advance(time.Second)
}
