package kafka

import (
	"context"
	"fmt"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// The integration topics of the producing contexts (their contracts are
// restated in apis/asyncapi.yaml, never imported).
const (
	CustomerOrderingTopic  = "retailnetwork.customer-ordering.events"
	OrderSourcingTopic     = "retailnetwork.order-sourcing.events"
	ShippingTrackingTopic  = "retailnetwork.shipping-and-tracking.events"
	LinehaulTopic          = "retailnetwork.linehaul-transportation.events"
	LastMileTopic          = "retailnetwork.last-mile-delivery.events"
	NodePerformanceTopic   = "retailnetwork.node-performance.events"
	NodeTopologyTopic      = "retailnetwork.node-topology.events"
	OfferAvailabilityTopic = "retailnetwork.offer-availability.events"
)

// Topics lists every topic the consumer reads.
func Topics() []string {
	return []string{
		CustomerOrderingTopic, OrderSourcingTopic, ShippingTrackingTopic, LinehaulTopic,
		LastMileTopic, NodePerformanceTopic, NodeTopologyTopic, OfferAvailabilityTopic,
	}
}

// typ builds the full CloudEvents type of a consumed event.
func typ(tier, context, entity, name string) string {
	return fmt.Sprintf("com.retailnetwork.%s.%s.%s.%s", tier, context, entity, name)
}

// handle adapts a typed use case to a HandlerFunc.
func handle[T any](required func(T) []string, call func(ctx context.Context, m usecases.Meta, v T) error) HandlerFunc {
	return func(ctx context.Context, m usecases.Meta, e ce.Event) error {
		v, err := decodeData(e, required)
		if err != nil {
			return err
		}
		return call(ctx, m, v)
	}
}

func ids(xs ...string) []string { return xs }

// Handlers maps the full CloudEvents type of every consumed event to its use
// case (apis/asyncapi.yaml, receive operations).
func Handlers(s *usecases.Service) map[string]HandlerFunc {
	h := map[string]HandlerFunc{}
	nodeState := func(state string) HandlerFunc {
		return handle(func(v usecases.NodeStateChanged) []string { return ids(v.NodeID) },
			func(ctx context.Context, m usecases.Meta, v usecases.NodeStateChanged) error {
				return s.OnNodeStateChanged(ctx, m, v, state)
			})
	}
	h[typ("logistics", "node-topology", "node", "NodeRegistered")] = handle(func(v usecases.NodeRegistered) []string { return ids(v.NodeID) }, s.OnNodeRegistered)
	h[typ("logistics", "node-topology", "node", "NodeOperationalProfileDeclared")] = handle(func(v usecases.NodeProfileDeclared) []string { return ids(v.NodeID) }, s.OnNodeProfileDeclared)
	h[typ("logistics", "node-topology", "node", "NodeActivated")] = nodeState(readmodel.NodeActive)
	h[typ("logistics", "node-topology", "node", "NodeSuspended")] = nodeState(readmodel.NodeSuspended)
	h[typ("logistics", "node-topology", "node", "NodeRetired")] = nodeState(readmodel.NodeRetired)
	h[typ("logistics", "node-topology", "lane", "LaneOpened")] = handle(func(v usecases.LaneOpened) []string { return ids(v.LaneID) }, s.OnLaneOpened)
	h[typ("logistics", "node-topology", "lane", "LaneClosed")] = handle(func(v usecases.LaneClosed) []string { return ids(v.LaneID) }, s.OnLaneClosed)
	h[typ("logistics", "node-topology", "servicearea", "ServiceAreaAssigned")] = handle(func(v usecases.ServiceAreaAssigned) []string { return ids(v.PostalPrefix) }, s.OnServiceAreaAssigned)
	h[typ("logistics", "node-topology", "region", "RegionDefined")] = handle(func(v usecases.RegionDefined) []string { return ids(v.RegionID) }, s.OnRegionDefined)

	catalog := func(discontinued bool) HandlerFunc {
		return handle(func(v usecases.CatalogItemChanged) []string { return ids(v.CatalogItemID) },
			func(ctx context.Context, m usecases.Meta, v usecases.CatalogItemChanged) error {
				return s.OnCatalogItemChanged(ctx, m, v, discontinued)
			})
	}
	toggled := func(suppressed bool) HandlerFunc {
		return handle(func(v usecases.ListingToggled) []string { return ids(v.ListingID) },
			func(ctx context.Context, m usecases.Meta, v usecases.ListingToggled) error {
				return s.OnListingToggled(ctx, m, v, suppressed)
			})
	}
	h[typ("commerce", "offer-availability", "catalogitem", "CatalogItemCreated")] = catalog(false)
	h[typ("commerce", "offer-availability", "catalogitem", "CatalogItemDiscontinued")] = catalog(true)
	h[typ("commerce", "offer-availability", "listing", "ListingCreated")] = handle(func(v usecases.ListingCreated) []string { return ids(v.ListingID, v.CatalogItemID, v.NodeID) }, s.OnListingCreated)
	h[typ("commerce", "offer-availability", "listing", "ListingAvailabilityChanged")] = handle(func(v usecases.ListingAvailabilityChanged) []string {
		return ids(v.ListingID, v.CatalogItemID, v.NodeID)
	}, s.OnListingAvailabilityChanged)
	h[typ("commerce", "offer-availability", "listing", "ListingSuppressed")] = toggled(true)
	h[typ("commerce", "offer-availability", "listing", "ListingRestored")] = toggled(false)

	h[typ("partner", "node-performance", "scorecard", "NodeScorecardUpdated")] = handle(func(v usecases.ScorecardUpdated) []string { return ids(v.NodeID) }, s.OnScorecardUpdated)

	h[typ("logistics", "linehaul-transportation", "departure", "DepartureScheduled")] = handle(func(v usecases.DepartureScheduled) []string { return ids(v.DepartureID) }, s.OnDepartureScheduled)
	h[typ("logistics", "linehaul-transportation", "departure", "DepartureCapacityChanged")] = handle(func(v usecases.DepartureCapacityChanged) []string { return ids(v.DepartureID) }, s.OnDepartureCapacityChanged)
	h[typ("logistics", "linehaul-transportation", "departure", "DepartureCancelled")] = handle(func(v usecases.DepartureCancelled) []string { return ids(v.DepartureID) }, s.OnDepartureCancelled)
	h[typ("logistics", "linehaul-transportation", "departure", "BookingMoved")] = handle(func(v usecases.BookingMoved) []string { return ids(v.AssignmentID) }, s.OnBookingMoved)
	h[typ("logistics", "linehaul-transportation", "departure", "BookingFailed")] = handle(func(v usecases.BookingFailed) []string { return ids(v.AssignmentID) }, s.OnBookingFailed)

	h[typ("logistics", "last-mile-delivery", "wave", "DeliveryWaveScheduled")] = handle(func(v usecases.WaveScheduled) []string { return ids(v.WaveID, v.StationNodeID) }, s.OnWaveScheduled)
	h[typ("logistics", "last-mile-delivery", "wave", "DeliveryWaveCapacityChanged")] = handle(func(v usecases.WaveCapacityChanged) []string { return ids(v.WaveID) }, s.OnWaveCapacityChanged)
	h[typ("logistics", "last-mile-delivery", "wave", "DeliveryWaveCancelled")] = handle(func(v usecases.WaveCancelled) []string { return ids(v.WaveID) }, s.OnWaveCancelled)
	h[typ("logistics", "last-mile-delivery", "packageattempthistory", "PackageInductionDeferred")] = handle(func(v usecases.InductionDeferred) []string { return ids(v.AssignmentID) }, s.OnInductionDeferred)

	h[typ("commerce", "order-sourcing", "assignment", "FulfillmentLocked")] = handle(func(v usecases.FulfillmentLocked) []string { return ids(v.AssignmentID, v.PromiseID) }, s.OnFulfillmentLocked)
	h[typ("commerce", "order-sourcing", "assignment", "FulfillmentShipped")] = handle(func(v usecases.FulfillmentShipped) []string { return ids(v.AssignmentID) }, s.OnFulfillmentShipped)
	h[typ("commerce", "order-sourcing", "assignment", "FulfillmentUnsourceable")] = handle(func(v usecases.FulfillmentUnsourceable) []string { return ids(v.AssignmentID, v.PromiseID) }, s.OnFulfillmentUnsourceable)

	h[typ("logistics", "shipping-and-tracking", "journey", "PackageDelivered")] = handle(func(v usecases.PackageDelivered) []string { return ids(v.TrackingNumber) }, s.OnPackageDelivered)
	h[typ("logistics", "shipping-and-tracking", "journey", "PackageExceptionRaised")] = handle(func(v usecases.PackageException) []string { return ids(v.AssignmentID) }, s.OnPackageException)
	h[typ("logistics", "shipping-and-tracking", "journey", "PackageLost")] = handle(func(v usecases.PackageLost) []string { return ids(v.TrackingNumber) }, s.OnPackageLost)
	h[typ("logistics", "shipping-and-tracking", "journey", "PackageReturned")] = handle(func(v usecases.PackageReturned) []string { return ids(v.TrackingNumber) }, s.OnPackageReturned)

	h[typ("commerce", "customer-ordering", "customerorder", "CustomerOrderPlaced")] = handle(func(v usecases.CustomerOrderPlaced) []string { return ids(v.OrderID) }, s.OnCustomerOrderPlaced)
	h[typ("commerce", "customer-ordering", "customerorder", "CustomerOrderCancelled")] = handle(func(v usecases.CustomerOrderCancelled) []string { return ids(v.OrderID) }, s.OnCustomerOrderCancelled)
	return h
}
