package book

import "github.com/claudioed/delivery-promise/internal/domain/shared"

// PromiseBookExhausted is raised by Hold when provisional + confirmed reaches
// share (integration event, consumed by order-sourcing).
type PromiseBookExhausted struct {
	shared.Header
	BookID            ID
	NodeID            string
	PickupDepartureID string
	Share             int
}

// EventName returns "PromiseBookExhausted".
func (PromiseBookExhausted) EventName() string { return "PromiseBookExhausted" }

// Entity returns "promisebook".
func (PromiseBookExhausted) Entity() string { return "promisebook" }

// SubjectID returns the bookId.
func (e PromiseBookExhausted) SubjectID() string { return string(e.BookID) }

var _ shared.Event = PromiseBookExhausted{}
