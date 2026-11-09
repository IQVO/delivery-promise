package usecases_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// lateRiskCounter is the LateRiskMetrics of a test.
type lateRiskCounter struct{ flagged, ignored int }

func (c *lateRiskCounter) PredictedLateFlagged(context.Context)    { c.flagged++ }
func (c *lateRiskCounter) DepartureArrivalIgnored(context.Context) { c.ignored++ }

// The worked example's path: the pickup dep-AUS3-1800 is expected at the
// sortation center at 19:15, the linehaul dep-SAU1-DAU1-2200 at the delivery
// station at 23:30, the wave W-DAU1-1023-A takes inductions until 06:00 and
// delivers 09:00-21:00 on the 23rd.

func newLateEnv(t *testing.T) (*env, *lateRiskCounter, string) {
	t.Helper()
	e := newEnv(t)
	counter := &lateRiskCounter{}
	e.svc.LateRisk = counter
	return e, counter, e.confirmedNextDay("ord-1", 1)
}

func (e *env) arrived(loadID, departureID, dest string, at time.Time) {
	e.t.Helper()
	must(e.t, e.svc.OnLoadArrived(ctx, e.meta("LoadArrived"), usecases.LoadArrived{LoadID: loadID, DepartureID: departureID, DestinationNodeID: dest, ArrivedAt: at}))
}

func (e *env) flagLate(limit int) int {
	e.t.Helper()
	n, err := e.svc.FlagPredictedLate(ctx, limit)
	must(e.t, err)
	return n
}

func (e *env) reason(id string) string {
	e.t.Helper()
	p, err := e.svc.GetPromise(ctx, id)
	must(e.t, err)
	return p.AtRiskReason
}

func TestFlagPredictedLateFlagsOnceThenLeavesItAlone(t *testing.T) {
	e, counter, id := newLateEnv(t)
	e.clock.Set(at(22, 19, 40)) // 25 minutes past the pickup's 19:15
	eq(t, "flagged", e.flagLate(100), 1)
	eq(t, "state", e.state(id), "AT_RISK")
	eq(t, "reason", e.reason(id), "PREDICTED_LATE")
	eq(t, "PromiseAtRisk raised", count(e.eventNames(), "PromiseAtRisk"), 1)
	eq(t, "metric", counter.flagged, 1)
	// a second pass: the promise is AT_RISK now, nothing flagged twice
	eq(t, "second pass", e.flagLate(100), 0)
	eq(t, "still one PromiseAtRisk", count(e.eventNames(), "PromiseAtRisk"), 1)
	eq(t, "metric unchanged", counter.flagged, 1)
}

func TestFlagPredictedLateNeedsNoArrivalRowToBeAbsent(t *testing.T) {
	e, _, id := newLateEnv(t)
	e.clock.Set(at(22, 19, 40))
	e.arrived("ld-1", "dep-AUS3-1800", "N-SAU1", at(22, 19, 20))
	eq(t, "the pickup arrived: nothing to flag", e.flagLate(100), 0)
	eq(t, "state", e.state(id), "CONFIRMED")
	// a row for a different departure does not excuse this one
	e.arrived("ld-2", "dep-other", "N-SAU1", at(22, 19, 20))
	eq(t, "still nothing", e.flagLate(100), 0)
}

func TestFlagPredictedLateWaitsOutTheGrace(t *testing.T) {
	e, _, id := newLateEnv(t)
	e.clock.Set(at(22, 19, 30)) // exactly the 15-minute grace past 19:15
	eq(t, "inside the grace", e.flagLate(100), 0)
	eq(t, "state", e.state(id), "CONFIRMED")
	e.clock.Set(at(22, 19, 31))
	eq(t, "one minute past the grace", e.flagLate(100), 1)
}

func TestFlagPredictedLateChecksTheLaterLegs(t *testing.T) {
	e, _, id := newLateEnv(t)
	e.arrived("ld-1", "dep-AUS3-1800", "N-SAU1", at(22, 19, 10))
	e.clock.Set(at(22, 23, 40)) // the linehaul is expected 23:30
	eq(t, "inside the grace", e.flagLate(100), 0)
	e.clock.Set(at(22, 23, 46))
	eq(t, "the linehaul is late", e.flagLate(100), 1)
	eq(t, "state", e.state(id), "AT_RISK")
}

// observeTransit feeds twenty paired load events whose transit is minutes, so
// the (origin, dest) pair has the observations the policy asks for (ADR 0014).
func (e *env) observeTransit(origin, dest string, minutes int) {
	e.t.Helper()
	for i := range 20 {
		departed := at(20, 8, 0).Add(time.Duration(i) * time.Hour)
		load := fmt.Sprintf("ld-obs-%d", i)
		must(e.t, e.svc.OnLoadDeparted(ctx, e.meta("LoadDeparted"), usecases.LoadDeparted{LoadID: load, OriginNodeID: origin, DepartedAt: departed}))
		e.arrived(load, "", dest, departed.Add(time.Duration(minutes)*time.Minute))
	}
}

func TestFlagPredictedLateAppliesTheMeasuredAdjustment(t *testing.T) {
	e, counter, id := newLateEnv(t)
	e.observeTransit("N-AUS3", "N-SAU1", 95) // 60 scheduled: the pickup is expected 35 minutes later, at 19:50
	eq(t, "arrivals without a departureId are counted", counter.ignored, 20)
	e.clock.Set(at(22, 20, 0))
	eq(t, "10 minutes past the measured 19:50, inside the grace", e.flagLate(100), 0)
	e.clock.Set(at(22, 20, 6))
	eq(t, "past the measured expectation and the grace", e.flagLate(100), 1)
	eq(t, "state", e.state(id), "AT_RISK")
}

func TestFlagPredictedLateWithoutObservationsUsesTheTimetable(t *testing.T) {
	e, _, _ := newLateEnv(t)
	for i := range 19 { // one short of minTransitObservations
		departed := at(20, 8, 0).Add(time.Duration(i) * time.Hour)
		load := fmt.Sprintf("ld-obs-%d", i)
		must(t, e.svc.OnLoadDeparted(ctx, e.meta("LoadDeparted"), usecases.LoadDeparted{LoadID: load, OriginNodeID: "N-AUS3", DepartedAt: departed}))
		e.arrived(load, "", "N-SAU1", departed.Add(95*time.Minute))
	}
	e.clock.Set(at(22, 19, 40))
	eq(t, "below the minimum the adjustment is 0", e.flagLate(100), 1)
}

func TestFlagPredictedLatePagesThroughEveryConfirmedPromise(t *testing.T) {
	e, counter, _ := newLateEnv(t)
	e.confirmedNextDay("ord-2", 1)
	e.confirmedNextDay("ord-3", 1)
	e.clock.Set(at(22, 19, 40))
	eq(t, "three promises, a page of one", e.flagLate(1), 3)
	eq(t, "metric", counter.flagged, 3)
	eq(t, "events", count(e.eventNames(), "PromiseAtRisk"), 3)
}

func TestFlagPredictedLateIgnoresPromisesThatAreNotConfirmed(t *testing.T) {
	e, _, _ := newLateEnv(t)
	provisional := e.make(offerOf(t, e.quote("NEXT_DAY"), "NEXT_DAY").OfferID).Promise
	e.clock.Set(at(22, 19, 40))
	eq(t, "only the confirmed one", e.flagLate(100), 1)
	eq(t, "the hold is left to its expiry", e.state(provisional.PromiseID), "PROVISIONAL")
}

func TestFlagPredictedLateWithoutTheModelIsANoOp(t *testing.T) {
	e, _, id := newLateEnv(t)
	e.svc.DepartureArrivals = nil
	e.clock.Set(at(22, 19, 40))
	eq(t, "no model, no flag", e.flagLate(100), 0)
	eq(t, "state", e.state(id), "CONFIRMED")
}
