package usecases_test

import (
	"testing"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// pickupEnv is the worked example plus a registered, ACTIVE pickup point
// served by the example's delivery station (ADR 0027).
func pickupEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	must(t, e.svc.OnNodeRegistered(ctx, e.meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: "N-PNT1", NodeType: "PICKUP_POINT", Operator: "NETWORK",
		ServingStationNodeID: "N-DAU1", RegionID: "R-SC", Timezone: "America/Chicago", PostalCode: "78701",
	}))
	must(t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-PNT1"}, "ACTIVE"))
	return e
}

func TestQuoteToAPickupPointUsesThePointsStation(t *testing.T) {
	e := pickupEnv(t)
	res, err := e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		PickupPointID: "N-PNT1",
		Lines:         []shared.Line{{CatalogItemID: "ci-1001", Quantity: 1}},
	})
	must(t, err)
	if len(res.Offers) == 0 {
		t.Fatalf("a pickup point served by the example's station is quotable; unavailable = %+v", res.Unavailable)
	}
	for _, o := range res.Offers {
		if o.DestinationPostalPrefix != "78701" {
			t.Fatalf("offer prefix = %q, want the point's own 78701", o.DestinationPostalPrefix)
		}
	}
}

func TestQuoteToAnUnknownOrInactivePointIsNotServed(t *testing.T) {
	e := pickupEnv(t)
	for _, id := range []string{"N-NOPE", "N-SAU1"} { // unknown; a sort centre, not a point
		res, err := e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
			PickupPointID: id,
			Lines:         []shared.Line{{CatalogItemID: "ci-1001", Quantity: 1}},
		})
		must(t, err)
		if len(res.Offers) != 0 {
			t.Fatalf("%s: no offer may exist, got %d", id, len(res.Offers))
		}
	}
	must(t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeSuspended"), usecases.NodeStateChanged{NodeID: "N-PNT1"}, "SUSPENDED"))
	res, err := e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		PickupPointID: "N-PNT1",
		Lines:         []shared.Line{{CatalogItemID: "ci-1001", Quantity: 1}},
	})
	must(t, err)
	if len(res.Offers) != 0 {
		t.Fatalf("a suspended point is not quotable, got %d offers", len(res.Offers))
	}
}

func TestQuoteNeedsExactlyOneDestination(t *testing.T) {
	e := pickupEnv(t)
	if _, err := e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{Lines: []shared.Line{{CatalogItemID: "ci-1001"}}}); err == nil {
		t.Fatal("no destination is refused")
	}
	if _, err := e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		DestinationPostalPrefix: "787", PickupPointID: "N-PNT1",
		Lines: []shared.Line{{CatalogItemID: "ci-1001"}},
	}); err == nil {
		t.Fatal("both destinations are refused")
	}
}

func TestEstimateToAPickupPoint(t *testing.T) {
	e := pickupEnv(t)
	res, err := e.svc.EstimateDeliveries(ctx, usecases.EstimateCommand{
		PickupPointID: "N-PNT1",
		Items:         []shared.Line{{CatalogItemID: "ci-1001", Quantity: 1}},
	})
	must(t, err)
	if res.DestinationPostalPrefix != "78701" {
		t.Fatalf("estimate prefix = %q, want 78701", res.DestinationPostalPrefix)
	}
}
