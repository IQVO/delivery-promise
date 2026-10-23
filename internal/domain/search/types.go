// Package search is the PromisePolicy search of the promise model: given a
// snapshot of the event-fed read models, it groups a cart into shipment groups
// (ShipmentGrouping), finds for every group and tier the candidate witness
// paths node -> pickup cutoff -> departures -> delivery wave, applies the
// ScorecardHandicap, and reports the constraint that blocked a tier. It is
// pure: no I/O, the clock is the Now field of the input.
package search

import (
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/calendar"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Node types and subtypes the search relies on (the model's node_types).
const (
	TypeFulfillmentCenter = "FULFILLMENT_CENTER"
	SubtypeSubSameDay     = "SUB_SAME_DAY"
	SubtypeMicro          = "MICRO"
)

// Departure kinds (linehaul-transportation).
const (
	KindPickup   = "PICKUP"
	KindLinehaul = "LINEHAUL"
	KindAir      = "AIR"
)

// Constraint says why a (group, tier) has no offer (OfferConstraint).
type Constraint string

// The five constraints.
const (
	DestinationNotServed     Constraint = "DESTINATION_NOT_SERVED"
	ItemQuantityNotAvailable Constraint = "ITEM_QUANTITY_NOT_AVAILABLE"
	DeliverySLAUnavailable   Constraint = "DELIVERY_SLA_UNAVAILABLE"
	NodeCapacityExhausted    Constraint = "NODE_CAPACITY_EXHAUSTED"
	PastOrderDeadline        Constraint = "PAST_ORDER_DEADLINE"
)

// Grade is a node scorecard grade.
type Grade string

// The scorecard grades; no scorecard is the empty grade.
const (
	GradeA           Grade = "A"
	GradeB           Grade = "B"
	GradeC           Grade = "C"
	GradeD           Grade = "D"
	GradeProvisional Grade = "PROVISIONAL"
)

// Rank orders grades best (0) to worst (3); PROVISIONAL and none count as A.
func (g Grade) Rank() int {
	switch g {
	case GradeB:
		return 1
	case GradeC:
		return 2
	case GradeD:
		return 3
	default:
		return 0
	}
}

// Profile is a node's operational profile as the search needs it.
type Profile struct {
	Schedule         calendar.Schedule
	HandlingMinutes  int
	Cutoffs          []calendar.Cutoff
	Cap              book.Throughput
	SortDwellMinutes int
}

// Node is a row of NodeProfileView.
type Node struct {
	ID       string
	Type     string
	Subtype  string
	RegionID string
	Active   bool
	Loc      *time.Location
	Profile  *Profile
}

// Lane is a row of LaneView.
type Lane struct {
	ID             string
	Origin         string
	Dest           string
	NominalMinutes int
	Open           bool
}

// Departure is a row of DepartureView. A row that arrived through a capacity
// event before its schedule event has Scheduled false and is ignored.
type Departure struct {
	ID        string
	LaneID    string
	Kind      string
	Origin    string
	Dest      string
	CutoffAt  time.Time
	DepartsAt time.Time
	ArrivesAt time.Time
	Remaining int
	Cancelled bool
	Scheduled bool
}

// Wave is a row of DeliveryWaveView.
type Wave struct {
	ID             string
	Station        string
	InductCutoffAt time.Time
	WindowStart    time.Time
	WindowEnd      time.Time
	Remaining      int
	Cancelled      bool
	Scheduled      bool
}

// BookView is what the search needs of a PromiseBook row.
type BookView struct {
	Open           bool
	Room           int
	HoldDeadlineAt time.Time
}

// Input is the snapshot a search runs on.
type Input struct {
	Now    time.Time
	Policy *policy.Policy
	// Station is the delivery station of the destination postal prefix and
	// AreaRegionID its region; a nil Station means DESTINATION_NOT_SERVED.
	Station      *Node
	AreaRegionID string
	Lines        []shared.Line
	Tiers        []shared.Tier
	// SingleGroup keeps all lines in one group (a re-promise of one group).
	SingleGroup bool
	Nodes       map[string]Node
	// Available is the units of an item shippable from a node, net of
	// provisional holds; 0 when the listing is unknown, suppressed or discontinued.
	Available  func(nodeID, itemID string) int
	Lanes      []Lane
	Departures []Departure
	Waves      []Wave
	Grades     map[string]Grade
	// Books holds the existing books by bookId; a missing book is opened lazily
	// with the computed share.
	Books             map[string]BookView
	ExcludeNodes      map[string]bool
	ExcludeDepartures map[string]bool
	ExcludeWaves      map[string]bool
	// OnlyNode restricts the candidates to one node (a locked assignment).
	OnlyNode string
}

// Candidate is one feasible witness path with the numbers an offer needs.
type Candidate struct {
	NodeID  string
	Tier    shared.Tier
	ShipBy  time.Time
	Window  shared.Window
	Basis   shared.Basis
	Witness shared.Witness
	// BookID is the PromiseBook the hold goes on.
	BookID string
	// ExpiresAt is min(now + offerTtl, LatestMeetableAt).
	ExpiresAt time.Time
	// LatestMeetableAt is the pickup cutoff minus the effective handling, in
	// business time.
	LatestMeetableAt time.Time
	Grade            Grade
	InRegion         bool
	LegCount         int
	// Direct is true when the pickup goes straight to the delivery station from a
	// SUB_SAME_DAY or MICRO fulfillment centre: the only SAME_DAY path.
	Direct bool
	// Opening is the book to open when it does not exist yet.
	Opening book.Opening
}

// Group is one shipment group and its candidates per tier.
type Group struct {
	No          int
	Lines       []shared.Line
	Candidates  map[shared.Tier][]Candidate
	Unavailable map[shared.Tier]Constraint
}

// Result is the answer of a search.
type Result struct {
	// WholeQuote holds the constraints that apply to the whole quote
	// (DESTINATION_NOT_SERVED, ITEM_QUANTITY_NOT_AVAILABLE, PAST_ORDER_DEADLINE).
	WholeQuote map[shared.Tier]Constraint
	Groups     []Group
}
