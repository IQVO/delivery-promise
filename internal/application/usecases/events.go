package usecases

import (
	"encoding/json"
	"time"
)

// Meta is what a consumer knows about the CloudEvents envelope of a message.
type Meta struct {
	ID   string
	Type string
	Time time.Time
}

// The inbound event payloads: the tolerant local copy of the fields this
// service relies on (apis/asyncapi.yaml, receive operations). The Kafka adapter
// decodes a CloudEvents `data` into them; unknown fields are ignored.

// HoursDTO is one weekday's opening hours.
type HoursDTO struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// CutoffDTO is a pickup cutoff slot.
type CutoffDTO struct {
	LocalTime string   `json:"localTime"`
	Days      []string `json:"days"`
	MaxOrders int      `json:"maxOrders"`
}

// ThroughputCapDTO is the node's throughput cap.
type ThroughputCapDTO struct {
	Value    int    `json:"value"`
	TimeUnit string `json:"timeUnit"`
}

// NodeRegistered is node-topology NodeRegistered.
type NodeRegistered struct {
	NodeID   string `json:"nodeId"`
	NodeType string `json:"nodeType"`
	Subtype  string `json:"subtype"`
	Operator string `json:"operator"`
	RegionID string `json:"regionId"`
	Timezone string `json:"timezone"`
}

// NodeProfileDeclared is node-topology NodeOperationalProfileDeclared.
type NodeProfileDeclared struct {
	NodeID              string              `json:"nodeId"`
	ProfileRevision     int                 `json:"profileRevision"`
	Timezone            string              `json:"timezone"`
	OperatingHoursByDay map[string]HoursDTO `json:"operatingHoursByDay"`
	Holidays            []string            `json:"holidays"`
	HandlingTimeMinutes int                 `json:"handlingTimeMinutes"`
	Cutoffs             []CutoffDTO         `json:"cutoffs"`
	ThroughputCap       ThroughputCapDTO    `json:"throughputCap"`
	SortDwellMinutes    int                 `json:"sortDwellMinutes"`
}

// NodeStateChanged is NodeActivated, NodeSuspended or NodeRetired.
type NodeStateChanged struct {
	NodeID string `json:"nodeId"`
}

// LaneOpened is node-topology LaneOpened.
type LaneOpened struct {
	LaneID                string `json:"laneId"`
	OriginNodeID          string `json:"originNodeId"`
	DestinationNodeID     string `json:"destinationNodeId"`
	NominalTransitMinutes int    `json:"nominalTransitMinutes"`
}

// LaneClosed is node-topology LaneClosed.
type LaneClosed struct {
	LaneID string `json:"laneId"`
}

// ServiceAreaAssigned is node-topology ServiceAreaAssigned.
type ServiceAreaAssigned struct {
	PostalPrefix          string `json:"postalPrefix"`
	DeliveryStationNodeID string `json:"deliveryStationNodeId"`
	RegionID              string `json:"regionId"`
	AssignmentVersion     int    `json:"assignmentVersion"`
	// DeliveryMode and Carriers are the ADR 0026 carrier-handoff declaration;
	// both are absent on an old producer, which means NETWORK_ROUTE with no
	// options - the promise then keeps using waves.
	DeliveryMode string             `json:"deliveryMode"`
	Carriers     []carrierOptionDTO `json:"carriers"`
}

// carrierOptionDTO is one declared carrier option of a CARRIER_HANDOFF area.
type carrierOptionDTO struct {
	CarrierID           string `json:"carrierId"`
	HandoffNodeID       string `json:"handoffNodeId"`
	CostPerPackageCents int64  `json:"costPerPackageCents"`
	TransitMinutes      int    `json:"transitMinutes"`
}

// RegionDefined is node-topology RegionDefined.
type RegionDefined struct {
	RegionID string `json:"regionId"`
	Code     string `json:"code"`
	Name     string `json:"name"`
}

// CatalogItemChanged is CatalogItemCreated or CatalogItemDiscontinued.
type CatalogItemChanged struct {
	CatalogItemID string `json:"catalogItemId"`
}

// ListingCreated is offer-availability ListingCreated. OwnerRef is the owning
// seller; absent means operator-owned ("", ADR 0017).
type ListingCreated struct {
	ListingID     string `json:"listingId"`
	CatalogItemID string `json:"catalogItemId"`
	NodeID        string `json:"nodeId"`
	OwnerRef      string `json:"ownerRef"`
}

// ListingAvailabilityChanged is offer-availability ListingAvailabilityChanged;
// OwnerRef as on ListingCreated.
type ListingAvailabilityChanged struct {
	ListingID     string    `json:"listingId"`
	CatalogItemID string    `json:"catalogItemId"`
	NodeID        string    `json:"nodeId"`
	OwnerRef      string    `json:"ownerRef"`
	AvailableQty  int       `json:"availableQty"`
	AsOf          time.Time `json:"asOf"`
}

// ListingToggled is ListingSuppressed or ListingRestored.
type ListingToggled struct {
	ListingID string `json:"listingId"`
	NodeID    string `json:"nodeId"`
}

// WaveScheduled is last-mile-delivery DeliveryWaveScheduled.
type WaveScheduled struct {
	WaveID         string    `json:"waveId"`
	StationNodeID  string    `json:"stationNodeId"`
	InductCutoffAt time.Time `json:"inductCutoffAt"`
	WindowStart    time.Time `json:"windowStart"`
	WindowEnd      time.Time `json:"windowEnd"`
	StopCapacity   int       `json:"stopCapacity"`
}

// WaveCapacityChanged is last-mile-delivery DeliveryWaveCapacityChanged.
type WaveCapacityChanged struct {
	WaveID         string `json:"waveId"`
	RemainingStops int    `json:"remainingStops"`
}

// WaveCancelled is last-mile-delivery DeliveryWaveCancelled.
type WaveCancelled struct {
	WaveID string `json:"waveId"`
}

// InductionDeferred is last-mile-delivery PackageInductionDeferred.
type InductionDeferred struct {
	TrackingNumber string `json:"trackingNumber"`
	AssignmentID   string `json:"assignmentId,omitempty"` // optional on the wire: omitted when no label data arrived
}

// ScorecardUpdated is node-performance NodeScorecardUpdated.
type ScorecardUpdated struct {
	NodeID       string    `json:"nodeId"`
	Window       string    `json:"window"`
	Grade        string    `json:"grade"`
	Observations int       `json:"observations"`
	ComputedAt   time.Time `json:"computedAt"`
}

// FulfillmentLocked is order-sourcing FulfillmentLocked.
type FulfillmentLocked struct {
	AssignmentID string `json:"assignmentId"`
	OrderID      string `json:"orderId"`
	GroupNo      int    `json:"groupNo"`
	PromiseID    string `json:"promiseId"`
	NodeID       string `json:"nodeId"`
	// PickupDepartureID is the departure order-sourcing locked the group on:
	// with NodeID the book that must carry the charge. Absent on older producers.
	PickupDepartureID string `json:"pickupDepartureId,omitempty"`
}

// FulfillmentShipped is order-sourcing FulfillmentShipped.
type FulfillmentShipped struct {
	AssignmentID    string   `json:"assignmentId"`
	TrackingNumbers []string `json:"trackingNumbers"`
}

// FulfillmentUnsourceable is order-sourcing FulfillmentUnsourceable.
type FulfillmentUnsourceable struct {
	AssignmentID  string   `json:"assignmentId"`
	OrderID       string   `json:"orderId"`
	GroupNo       int      `json:"groupNo"`
	PromiseID     string   `json:"promiseId"`
	ExcludedNodes NodeRefs `json:"excludedNodes"`
}

// PackageDelivered is shipping-and-tracking PackageDelivered.
type PackageDelivered struct {
	TrackingNumber string    `json:"trackingNumber"`
	AssignmentID   string    `json:"assignmentId"`
	DeliveredAt    time.Time `json:"deliveredAt"`
}

// PackageHandedToCarrier is shipping-and-tracking PackageHandedToCarrier
// (ADR 0026): the physical handoff of a package to the chosen carrier of a
// CARRIER_HANDOFF area - the departure half of a carrier transit observation.
type PackageHandedToCarrier struct {
	TrackingNumber          string    `json:"trackingNumber"`
	AssignmentID            string    `json:"assignmentId"`
	OrderID                 string    `json:"orderId"`
	CarrierID               string    `json:"carrierId"`
	HandoffNodeID           string    `json:"handoffNodeId"`
	DestinationPostalPrefix string    `json:"destinationPostalPrefix"`
	HandedAt                time.Time `json:"handedAt"`
}

// PackageException is shipping-and-tracking PackageExceptionRaised.
type PackageException struct {
	TrackingNumber string `json:"trackingNumber"`
	AssignmentID   string `json:"assignmentId"`
}

// PackageLost is shipping-and-tracking PackageLost.
type PackageLost struct {
	TrackingNumber string    `json:"trackingNumber"`
	DeclaredAt     time.Time `json:"declaredAt"`
}

// PackageReturned is shipping-and-tracking PackageReturned.
type PackageReturned struct {
	TrackingNumber string    `json:"trackingNumber"`
	ReturnedAt     time.Time `json:"returnedAt"`
}

// OrderGroup is one group of CustomerOrderPlaced.
type OrderGroup struct {
	GroupNo   int    `json:"groupNo"`
	PromiseID string `json:"promiseId"`
}

// CustomerOrderPlaced is customer-ordering CustomerOrderPlaced.
type CustomerOrderPlaced struct {
	OrderID string       `json:"orderId"`
	Groups  []OrderGroup `json:"groups"`
}

// CustomerOrderCancelled is customer-ordering CustomerOrderCancelled.
type CustomerOrderCancelled struct {
	OrderID  string `json:"orderId"`
	Reason   string `json:"reason"`
	GroupNos []int  `json:"groupNos"`
}

// NodeRefs is the excludedNodes list of FulfillmentUnsourceable: order-sourcing
// publishes objects ({nodeId, reason}); a bare node id string is tolerated too.
type NodeRefs []string

// UnmarshalJSON accepts an array of objects with a nodeId, or of strings.
func (n *NodeRefs) UnmarshalJSON(b []byte) error {
	var raw []json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := make(NodeRefs, 0, len(raw))
	for _, r := range raw {
		var id string
		if err := json.Unmarshal(r, &id); err != nil {
			var o struct {
				NodeID string `json:"nodeId"`
			}
			if err := json.Unmarshal(r, &o); err != nil {
				return err
			}
			id = o.NodeID
		}
		if id != "" {
			out = append(out, id)
		}
	}
	*n = out
	return nil
}
