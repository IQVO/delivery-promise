package usecases

import "time"

// The consumed linehaul-transportation messages (tolerant copies: only the fields this service reads).

// DepartureScheduled is linehaul-transportation DepartureScheduled.
type DepartureScheduled struct {
	DepartureID       string    `json:"departureId"`
	LaneID            string    `json:"laneId"`
	Kind              string    `json:"kind"`
	OriginNodeID      string    `json:"originNodeId"`
	DestinationNodeID string    `json:"destinationNodeId"`
	CutoffAt          time.Time `json:"cutoffAt"`
	DepartsAt         time.Time `json:"departsAt"`
	ArrivesAt         time.Time `json:"arrivesAt"`
	CapacityPackages  int       `json:"capacityPackages"`
}

// DepartureCapacityChanged is linehaul-transportation DepartureCapacityChanged.
type DepartureCapacityChanged struct {
	DepartureID       string `json:"departureId"`
	RemainingPackages int    `json:"remainingPackages"`
}

// DepartureCancelled is linehaul-transportation DepartureCancelled.
type DepartureCancelled struct {
	DepartureID string `json:"departureId"`
}

// BookingMoved is linehaul-transportation BookingMoved.
type BookingMoved struct {
	AssignmentID    string `json:"assignmentId"`
	FromDepartureID string `json:"fromDepartureId"`
	ToDepartureID   string `json:"toDepartureId"`
}

// BookingFailed is linehaul-transportation BookingFailed.
type BookingFailed struct {
	AssignmentID string `json:"assignmentId"`
	DepartureID  string `json:"departureId"`
}

// LoadDeparted is linehaul-transportation LoadDeparted (ADR 0014). DepartureID
// is optional: older producers omit it.
type LoadDeparted struct {
	LoadID       string    `json:"loadId"`
	DepartureID  string    `json:"departureId,omitempty"`
	OriginNodeID string    `json:"originNodeId"`
	DepartedAt   time.Time `json:"departedAt"`
}

// LoadArrived is linehaul-transportation LoadArrived (ADR 0014). DepartureID
// is optional (older producers omit it): when present, the arrival is recorded
// per departure for the late-risk sweep (ADR 0021).
type LoadArrived struct {
	LoadID            string    `json:"loadId"`
	DepartureID       string    `json:"departureId,omitempty"`
	DestinationNodeID string    `json:"destinationNodeId"`
	ArrivedAt         time.Time `json:"arrivedAt"`
}
