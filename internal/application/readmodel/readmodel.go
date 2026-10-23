// Package readmodel holds the rows of the event-fed local read models (ADR
// 0003): NodeProfileView, RegionAndServiceAreaView, LaneView, AvailabilityView,
// DepartureView, DeliveryWaveView, ScorecardView, and the assignment and package
// maps that JudgeOnDelivery needs. They are plain data: the ports package
// declares how they are stored and the usecases package how they are projected.
// Departures, waves and lanes are stored as the search package's own types.
package readmodel

import (
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/search"
)

// Node states as projected from node-topology.
const (
	NodeProposed  = "PROPOSED"
	NodeActive    = "ACTIVE"
	NodeSuspended = "SUSPENDED"
	NodeRetired   = "RETIRED"
)

// Hours is one weekday's opening hours, "HH:mm" local.
type Hours struct {
	Start string
	End   string
}

// Cutoff is one pickup cutoff slot of a profile.
type Cutoff struct {
	LocalTime string
	Days      []string
	MaxOrders int
}

// Profile is a node's declared operational profile.
type Profile struct {
	Revision         int
	Timezone         string
	OperatingHours   map[string]Hours
	Holidays         []string
	HandlingMinutes  int
	Cutoffs          []Cutoff
	CapValue         int
	CapPerHour       bool
	SortDwellMinutes int
}

// Node is a row of NodeProfileView. Profile is nil until one is declared.
type Node struct {
	ID       string
	Type     string
	Subtype  string
	Operator string
	RegionID string
	Timezone string
	State    string
	Profile  *Profile
}

// Region is a row of the region part of RegionAndServiceAreaView.
type Region struct {
	ID   string
	Code string
	Name string
}

// Area is a row of the service-area part of RegionAndServiceAreaView.
type Area struct {
	PostalPrefix string
	StationID    string
	RegionID     string
	Version      int
}

// Item is a catalogue item of AvailabilityView.
type Item struct {
	ID           string
	Discontinued bool
}

// Listing is a row of AvailabilityView: one item at one node.
type Listing struct {
	ID           string
	ItemID       string
	NodeID       string
	AvailableQty int
	AsOf         time.Time
	Suppressed   bool
}

// Scorecard is a row of ScorecardView (the 30d window only).
type Scorecard struct {
	NodeID       string
	Grade        string
	Observations int
	ComputedAt   time.Time
}

// Assignment maps an order-sourcing assignment to the promise it serves. A row
// is created by FulfillmentLocked (Locked) or earlier by FulfillmentUnsourceable.
// Re-promising never picks an excluded node or departure, and once Locked it
// only picks NodeID (the node never changes after lock).
type Assignment struct {
	ID                 string
	PromiseID          string
	OrderID            string
	GroupNo            int
	NodeID             string
	Locked             bool
	LockedAt           time.Time
	ExcludedNodes      []string
	ExcludedDepartures []string
}

// Outcome kinds of a package.
const (
	OutcomeDelivered = "DELIVERED"
	OutcomeLost      = "LOST"
	OutcomeReturned  = "RETURNED"
)

// PackageOutcome is the first final fact recorded for a tracking number.
type PackageOutcome struct {
	TrackingNumber string
	AssignmentID   string // may be empty for a loss or return that arrived first
	Kind           string
	At             time.Time
}

// Query asks the store for the slice of the network a search needs.
type Query struct {
	StationID string
	ItemIDs   []string
	From      time.Time
	Until     time.Time
}

// Network is the slice of the read models a search runs on.
type Network struct {
	Nodes      []Node
	Listings   []Listing
	Items      []Item
	Scorecards []Scorecard
	Lanes      []search.Lane
	Departures []search.Departure
	Waves      []search.Wave
}
