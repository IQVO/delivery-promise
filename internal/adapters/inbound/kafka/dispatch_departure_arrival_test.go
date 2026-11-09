package kafka

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/memory"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// newArrivalService is newService with the DepartureArrival model wired.
func newArrivalService() (*usecases.Service, *memory.Store) {
	store := memory.NewStore()
	svc := usecases.New(usecases.Deps{
		UoW: store.UnitOfWork(), Offers: store.Offers(), Promises: store.Promises(), Books: store.Books(), Policies: store.Policies(),
		Network: store.Network(), Shipments: store.Shipments(), TransitActuals: store.TransitActuals(), DepartureArrivals: store.DepartureArrivals(),
		Outbox: store.Outbox(), Inbox: store.Inbox(), Clock: memory.NewFakeClock(t0), IDs: &memory.SeqIDs{},
	})
	return svc, store
}

// TestLoadArrivedDepartureIdIsOptionalAndTolerant dispatches LoadArrived on its
// full type with and without the optional departureId, and with fields it does
// not know (ADR 0021).
func TestLoadArrivedDepartureIdIsOptionalAndTolerant(t *testing.T) {
	svc, store := newArrivalService()
	c := &Consumer{Name: "t", Handlers: Handlers(svc), Logger: discard()}
	ctx := context.Background()
	noErr(t, c.HandleMessage(ctx, message(t, "arr-new", loadArrived, map[string]any{
		"loadId": "ld-1", "departureId": "dep-1800", "destinationNodeId": "N-B", "arrivedAt": t0.Add(time.Hour),
		"trackingNumbers": []string{"TRK-1"}, "departureKind": "LINEHAUL", "someFutureField": map[string]any{"a": 1},
	})))
	noErr(t, c.HandleMessage(ctx, message(t, "arr-old", loadArrived, map[string]any{
		"loadId": "ld-2", "destinationNodeId": "N-B", "arrivedAt": t0.Add(time.Hour),
	})))
	got, err := store.DepartureArrivals().DepartureArrived(ctx, "dep-1800")
	noErr(t, err)
	if !got {
		t.Fatal("an arrival that carries a departureId must reach the DepartureArrival model")
	}
	if got, _ := store.DepartureArrivals().DepartureArrived(ctx, ""); got {
		t.Fatal("an arrival without a departureId must write no row")
	}
}

// TestLoadArrivedRedeliveryRecordsOneArrival pins the inbox on the new model.
func TestLoadArrivedRedeliveryRecordsOneArrival(t *testing.T) {
	svc, store := newArrivalService()
	c := &Consumer{Name: "t", Handlers: Handlers(svc), Logger: discard()}
	ctx := context.Background()
	msg := message(t, "arr-once", loadArrived, map[string]any{"loadId": "ld-1", "departureId": "dep-1800", "destinationNodeId": "N-B", "arrivedAt": t0})
	noErr(t, c.HandleMessage(ctx, msg))
	noErr(t, c.HandleMessage(ctx, msg))
	if got, _ := store.DepartureArrivals().DepartureArrived(ctx, "dep-1800"); !got {
		t.Fatal("the arrival must be recorded")
	}
}
