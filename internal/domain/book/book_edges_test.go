package book

import (
	"strings"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// TestOpeningAcceptsTheLongestIds pins the exact bookId budget: nodeId and
// pickupDepartureId of MaxID each, joined by one colon.
func TestOpeningAcceptsTheLongestIds(t *testing.T) {
	in := Opening{
		NodeID:            strings.Repeat("a", shared.MaxID),
		PickupDepartureID: strings.Repeat("b", shared.MaxID),
		CutoffAt:          time.Date(2026, 10, 22, 18, 0, 0, 0, time.UTC),
		HoldDeadlineAt:    time.Date(2026, 10, 22, 16, 25, 0, 0, time.UTC),
		Share:             1,
	}
	if err := checkOpening(in); err != nil {
		t.Fatalf("the longest ids are still a valid opening: %v", err)
	}
}

// TestRoomSubtractsBothKindsOfHolds pins the arithmetic of the room left.
func TestRoomSubtractsBothKindsOfHolds(t *testing.T) {
	b := Book{share: 100, provisional: 30, confirmed: 20}
	if b.Room() != 50 {
		t.Fatalf("room = %d, want 100-30-20 = 50", b.Room())
	}
}

// TestThroughputOrdersAcceptsTheSmallestInputs pins the smallest cap, window
// and day that still carry an order.
func TestThroughputOrdersAcceptsTheSmallestInputs(t *testing.T) {
	if got := ThroughputOrders(Throughput{Value: 1}, 1, 1); got != 1 {
		t.Fatalf("a cap of one over one minute = %d, want 1", got)
	}
	if got := ThroughputOrders(Throughput{Value: 1, PerHour: true}, 1, 0); got != 1 {
		t.Fatalf("one per hour over one minute = %d, want 1", got)
	}
}

// TestShareOfTheSmallestBaseStillScales: a base of one order with a 200%
// share doubles, because the floor of one only guards an empty base.
func TestShareOfTheSmallestBaseStillScales(t *testing.T) {
	if got := Share(0, 1, 200); got != 2 {
		t.Fatalf("share = %d, want 2", got)
	}
	if got := Share(0, 0, 50); got != 1 {
		t.Fatalf("an empty base still shares one: %d", got)
	}
}
