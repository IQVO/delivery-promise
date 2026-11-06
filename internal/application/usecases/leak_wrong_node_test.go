package usecases_test

import (
	"testing"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// Leak C: the PromiseBook followed the node the promise was QUOTED on (the
// witness), not the node order-sourcing LOCKED. When sourcing locks another
// node, the quoted node's book kept capacity it would never ship and the node
// that really fulfils was never charged; a later cancel released the quoted
// node's book. The charge and the release must hit the node that fulfils.

func (e *env) seedSecondFulfillmentCenter() {
	e.t.Helper()
	e.stock("N-DFW7", "ci-1001", "lst-2", 50)
	e.node("N-DFW7", "FULFILLMENT_CENTER", "SORTABLE", "R-SC", fcProfile(95, 2500))
	e.departure("dep-DFW7-1800", "lane-1", "PICKUP", "N-DFW7", "N-SAU1", at(22, 18, 0), at(22, 19, 15), 40)
}

func (e *env) lockAt(promiseID, node, pickup string) {
	e.t.Helper()
	must(e.t, e.svc.OnFulfillmentLocked(ctx, e.meta("FulfillmentLocked"), usecases.FulfillmentLocked{
		AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: promiseID, NodeID: node, PickupDepartureID: pickup,
	}))
}

func TestWhenSourcingLocksAnotherNodeTheChargeMovesToIt(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1) // quoted and confirmed on N-AUS3
	e.seedSecondFulfillmentCenter()
	eq(t, "charged on the quoted node first", e.bookOf("N-AUS3:dep-AUS3-1800").Confirmed(), 1)

	e.lockAt(id, "N-DFW7", "dep-DFW7-1800")

	eq(t, "the quoted node is no longer charged", e.bookOf("N-AUS3:dep-AUS3-1800").Confirmed(), 0)
	eq(t, "the locked node is charged", e.bookOf("N-DFW7:dep-DFW7-1800").Confirmed(), 1)
	got, err := e.svc.GetPromise(ctx, id)
	must(t, err)
	eq(t, "the promise follows the node that fulfils", got.WitnessPath.NodeID, "N-DFW7")
	eq(t, "and its pickup", got.WitnessPath.PickupDepartureID, "dep-DFW7-1800")

	// the release hits the same, correct node
	_, err = e.svc.CancelPromise(ctx, id)
	must(t, err)
	eq(t, "released from the locked node", e.bookOf("N-DFW7:dep-DFW7-1800").Confirmed(), 0)
	eq(t, "the quoted node is untouched", e.bookOf("N-AUS3:dep-AUS3-1800").Confirmed(), 0)
}

func TestALockOnTheQuotedNodeChangesNothing(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	e.lockAt(id, "N-AUS3", "dep-AUS3-1800")
	eq(t, "still charged once", e.bookOf("N-AUS3:dep-AUS3-1800").Confirmed(), 1)
	e.lockAt(id, "N-AUS3", "dep-AUS3-1800") // a replay
	eq(t, "a replay charges nothing twice", e.bookOf("N-AUS3:dep-AUS3-1800").Confirmed(), 1)
}

func TestALockOnADepartureTheNodeDoesNotRunLeavesTheChargeAndDoesNotFail(t *testing.T) {
	e := newEnv(t)
	id := e.confirmedNextDay("ord-1", 1)
	e.seedSecondFulfillmentCenter()
	e.lockAt(id, "N-DFW7", "dep-DFW7-0000") // not a departure of the read model: nothing can be charged
	eq(t, "unchanged", e.bookOf("N-AUS3:dep-AUS3-1800").Confirmed(), 1)
}
