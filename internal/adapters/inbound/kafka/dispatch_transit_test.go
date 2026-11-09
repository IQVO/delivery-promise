package kafka

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"
)

const (
	loadDeparted = "com.retailnetwork.logistics.linehaul-transportation.load.LoadDeparted"
	loadArrived  = "com.retailnetwork.logistics.linehaul-transportation.load.LoadArrived"
)

// TestLoadEventsPairIntoMeasuredTransit projects the two linehaul load events
// through the consumer into the measured-transit read model (ADR 0014).
func TestLoadEventsPairIntoMeasuredTransit(t *testing.T) {
	svc, store := newService(nil)
	c := &Consumer{Name: "t", Handlers: Handlers(svc), Logger: discard()}
	ctx := context.Background()
	for i := range 20 {
		departed := t0.Add(time.Duration(i) * time.Hour)
		noErr(t, c.HandleMessage(ctx, message(t, fmt.Sprintf("dep-%d", i), loadDeparted, map[string]any{
			"loadId": "ld-" + strconv.Itoa(i), "originNodeId": "N-A", "destinationNodeId": "N-X", "departedAt": departed,
		})))
		noErr(t, c.HandleMessage(ctx, message(t, fmt.Sprintf("arr-%d", i), loadArrived, map[string]any{
			"loadId": "ld-" + strconv.Itoa(i), "destinationNodeId": "N-B", "arrivedAt": departed.Add(time.Duration(60+i) * time.Minute),
		})))
	}
	ok, minutes := store.TransitActuals().MeasuredTransit(ctx, "N-A", "N-B", 90, 20, 28, t0.Add(24*time.Hour))
	if !ok || minutes != 77 {
		t.Fatalf("P90 = %d ok=%v, want 77", minutes, ok)
	}
}

// TestALoadArrivalWithoutItsDepartureIsANoOp keeps the pairing best-effort
// (ADR 0014): an arrival whose departure was never seen records nothing.
func TestALoadArrivalWithoutItsDepartureIsANoOp(t *testing.T) {
	svc, store := newService(nil)
	c := &Consumer{Name: "t", Handlers: Handlers(svc), Logger: discard()}
	ctx := context.Background()
	noErr(t, c.HandleMessage(ctx, message(t, "arr-orphan", loadArrived, map[string]any{
		"loadId": "ld-orphan", "destinationNodeId": "N-B", "arrivedAt": t0.Add(time.Hour),
	})))
	if ok, _ := store.TransitActuals().MeasuredTransit(ctx, "", "N-B", 90, 1, 28, t0); ok {
		t.Fatal("an orphan arrival must not be measured")
	}
}

// TestALoadEventRedeliveryChangesNothing pins the inbox idempotency of the
// two load events.
func TestALoadEventRedeliveryChangesNothing(t *testing.T) {
	svc, store := newService(nil)
	c := &Consumer{Name: "t", Handlers: Handlers(svc), Logger: discard()}
	ctx := context.Background()
	departed := message(t, "dep-once", loadDeparted, map[string]any{"loadId": "ld-1", "originNodeId": "N-A", "departedAt": t0})
	arrived := message(t, "arr-once", loadArrived, map[string]any{"loadId": "ld-1", "destinationNodeId": "N-B", "arrivedAt": t0.Add(90 * time.Minute)})
	noErr(t, c.HandleMessage(ctx, departed))
	noErr(t, c.HandleMessage(ctx, arrived))
	ok, minutes := store.TransitActuals().MeasuredTransit(ctx, "N-A", "N-B", 100, 1, 28, t0)
	if !ok || minutes != 90 {
		t.Fatalf("minutes = %d ok=%v, want 90", minutes, ok)
	}
	noErr(t, c.HandleMessage(ctx, departed))
	noErr(t, c.HandleMessage(ctx, arrived))
	ok, minutes = store.TransitActuals().MeasuredTransit(ctx, "N-A", "N-B", 100, 1, 28, t0)
	if !ok || minutes != 90 {
		t.Fatalf("after the redelivery minutes = %d ok=%v, want still one 90-minute observation", minutes, ok)
	}
}
