package promise

import (
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

const entity = "promise"

// PromiseMade is raised by Make (analytics only).
type PromiseMade struct {
	shared.Header
	PromiseID     ID
	OfferID       string
	Terms         Terms
	Witness       shared.Witness
	HoldExpiresAt time.Time
	Lines         []shared.Line
}

// EventName returns "PromiseMade".
func (PromiseMade) EventName() string { return "PromiseMade" }

// Entity returns "promise".
func (PromiseMade) Entity() string { return entity }

// SubjectID returns the promiseId.
func (e PromiseMade) SubjectID() string { return string(e.PromiseID) }

// PromiseConfirmed is raised by Confirm (analytics only).
type PromiseConfirmed struct {
	shared.Header
	PromiseID ID
	OrderID   string
	GroupNo   int
}

// EventName returns "PromiseConfirmed".
func (PromiseConfirmed) EventName() string { return "PromiseConfirmed" }

// Entity returns "promise".
func (PromiseConfirmed) Entity() string { return entity }

// SubjectID returns the promiseId.
func (e PromiseConfirmed) SubjectID() string { return string(e.PromiseID) }

// PromiseAtRisk is raised by FlagAtRisk (integration event).
type PromiseAtRisk struct {
	shared.Header
	PromiseID  ID
	OrderID    string
	Reason     string
	DetectedAt time.Time
}

// EventName returns "PromiseAtRisk".
func (PromiseAtRisk) EventName() string { return "PromiseAtRisk" }

// Entity returns "promise".
func (PromiseAtRisk) Entity() string { return entity }

// SubjectID returns the promiseId.
func (e PromiseAtRisk) SubjectID() string { return string(e.PromiseID) }

// PromiseRevised is raised by Revise (integration event).
type PromiseRevised struct {
	shared.Header
	PromiseID               ID
	OrderID                 string
	RevisionNo              int
	PreviousDeliverByLatest time.Time
	Terms                   Terms
	NodeID                  string
	Reason                  string
}

// EventName returns "PromiseRevised".
func (PromiseRevised) EventName() string { return "PromiseRevised" }

// Entity returns "promise".
func (PromiseRevised) Entity() string { return entity }

// SubjectID returns the promiseId.
func (e PromiseRevised) SubjectID() string { return string(e.PromiseID) }

// PromiseKept is raised by Judge (integration event).
type PromiseKept struct {
	shared.Header
	PromiseID       ID
	OrderID         string
	NodeID          string
	DeliveredAt     time.Time
	DeliverByLatest time.Time
}

// EventName returns "PromiseKept".
func (PromiseKept) EventName() string { return "PromiseKept" }

// Entity returns "promise".
func (PromiseKept) Entity() string { return entity }

// SubjectID returns the promiseId.
func (e PromiseKept) SubjectID() string { return string(e.PromiseID) }

// PromiseBroken is raised by Judge (integration event). DeliveredAt is zero
// for the causes LOST and RETURNED.
type PromiseBroken struct {
	shared.Header
	PromiseID       ID
	OrderID         string
	NodeID          string
	DeliveredAt     time.Time
	DeliverByLatest time.Time
	LateByMinutes   int
	Cause           Cause
}

// EventName returns "PromiseBroken".
func (PromiseBroken) EventName() string { return "PromiseBroken" }

// Entity returns "promise".
func (PromiseBroken) Entity() string { return entity }

// SubjectID returns the promiseId.
func (e PromiseBroken) SubjectID() string { return string(e.PromiseID) }

// PromiseReleased is raised by ReleaseExpiredHold (analytics only).
type PromiseReleased struct {
	shared.Header
	PromiseID ID
	Reason    string
}

// EventName returns "PromiseReleased".
func (PromiseReleased) EventName() string { return "PromiseReleased" }

// Entity returns "promise".
func (PromiseReleased) Entity() string { return entity }

// SubjectID returns the promiseId.
func (e PromiseReleased) SubjectID() string { return string(e.PromiseID) }

// PromiseCancelled is raised by Cancel (analytics only).
type PromiseCancelled struct {
	shared.Header
	PromiseID ID
	OrderID   string
	Reason    string
}

// EventName returns "PromiseCancelled".
func (PromiseCancelled) EventName() string { return "PromiseCancelled" }

// Entity returns "promise".
func (PromiseCancelled) Entity() string { return entity }

// SubjectID returns the promiseId.
func (e PromiseCancelled) SubjectID() string { return string(e.PromiseID) }

// PromiseUnkeepable is raised by MarkUnkeepable (integration event, E9).
type PromiseUnkeepable struct {
	shared.Header
	PromiseID ID
	OrderID   string
	GroupNo   int
	Reason    string
}

// EventName returns "PromiseUnkeepable".
func (PromiseUnkeepable) EventName() string { return "PromiseUnkeepable" }

// Entity returns "promise".
func (PromiseUnkeepable) Entity() string { return entity }

// SubjectID returns the promiseId.
func (e PromiseUnkeepable) SubjectID() string { return string(e.PromiseID) }

var (
	_ shared.Event = PromiseMade{}
	_ shared.Event = PromiseConfirmed{}
	_ shared.Event = PromiseAtRisk{}
	_ shared.Event = PromiseRevised{}
	_ shared.Event = PromiseKept{}
	_ shared.Event = PromiseBroken{}
	_ shared.Event = PromiseReleased{}
	_ shared.Event = PromiseCancelled{}
	_ shared.Event = PromiseUnkeepable{}
)
