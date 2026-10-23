// Package book is the PromiseBook aggregate: the promise-side capacity ledger
// of one node pickup cutoff (bookId = nodeId + ':' + pickupDepartureId), the
// orders the network has promised onto it against the share the node declared.
//
// States OPEN, CLOSED; transition OPEN -> CLOSED (Close, driven by the clock at
// the cutoff). Invariants: provisional + confirmed <= share
// (ErrPromiseCapacityExhausted); no hold once the book is CLOSED or after
// cutoffAt minus handling time (ErrBookClosed); a release or confirm never
// makes a counter negative (ErrCapacityUnderflow). A book is opened lazily by
// the first hold (gap G9); confirming and releasing stay legal on a CLOSED
// book so an in-flight confirm never fails for the clock (ADR 0002, A13).
package book

import (
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// The PromiseBook invariants of the model, by the model's names.
var (
	// ErrPromiseCapacityExhausted: provisional + confirmed would exceed share (409).
	ErrPromiseCapacityExhausted = errors.New("promise capacity exhausted")
	// ErrBookClosed: the book is CLOSED or past its hold deadline (409).
	ErrBookClosed = errors.New("book closed")
	// ErrCapacityUnderflow: a counter would go negative (internal guard).
	ErrCapacityUnderflow = errors.New("capacity underflow")
)

// ID is the bookId: nodeId + ':' + pickupDepartureId.
type ID string

// MakeID builds the bookId of a node pickup.
func MakeID(nodeID, pickupDepartureID string) ID { return ID(nodeID + ":" + pickupDepartureID) }

// State is OPEN or CLOSED.
type State string

// The two book states.
const (
	StateOpen   State = "OPEN"
	StateClosed State = "CLOSED"
)

// MaxShare is the Postgres integer ceiling for a share (plain literal).
const MaxShare = 2147483647

// Book is the aggregate root.
type Book struct {
	id           ID
	nodeID       string
	pickupID     string
	cutoffAt     time.Time
	holdDeadline time.Time
	share        int
	provisional  int
	confirmed    int
	state        State
	closedAt     time.Time
	version      int64
}

// Snapshot is the persisted form of a book. Restore validates it.
type Snapshot struct {
	ID                ID
	NodeID            string
	PickupDepartureID string
	CutoffAt          time.Time
	HoldDeadlineAt    time.Time
	Share             int
	Provisional       int
	Confirmed         int
	State             State
	ClosedAt          time.Time
	Version           int64
}

// Opening is the input of Open.
type Opening struct {
	NodeID            string
	PickupDepartureID string
	CutoffAt          time.Time
	// HoldDeadlineAt is cutoffAt minus the handling time (business time):
	// from this instant no hold is accepted.
	HoldDeadlineAt time.Time
	Share          int
}

// Open creates an OPEN book at version 1 with nothing held.
func Open(in Opening) (*Book, error) {
	if err := checkOpening(in); err != nil {
		return nil, err
	}
	return &Book{
		id: MakeID(in.NodeID, in.PickupDepartureID), nodeID: in.NodeID, pickupID: in.PickupDepartureID,
		cutoffAt: shared.UTC(in.CutoffAt), holdDeadline: shared.UTC(in.HoldDeadlineAt), share: in.Share,
		state: StateOpen, version: 1,
	}, nil
}

func checkOpening(in Opening) error {
	switch {
	case !shared.ValidID(in.NodeID) || !shared.ValidID(in.PickupDepartureID):
		return shared.Invalid("nodeId and pickupDepartureId must be valid ids")
	case len(in.NodeID)+1+len(in.PickupDepartureID) > 2*shared.MaxID+1:
		return shared.Invalid("bookId is too long")
	case in.CutoffAt.IsZero() || in.HoldDeadlineAt.IsZero() || in.HoldDeadlineAt.After(in.CutoffAt):
		return shared.Invalid("holdDeadlineAt must not be after cutoffAt")
	case in.Share < 1 || in.Share > MaxShare:
		return shared.Invalid("share must be between 1 and %d", MaxShare)
	}
	return nil
}

// Restore rebuilds a persisted book, validating every invariant.
func Restore(s Snapshot) (*Book, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("book %q: %v", s.ID, err)
	}
	return &Book{
		id: s.ID, nodeID: s.NodeID, pickupID: s.PickupDepartureID, cutoffAt: shared.UTC(s.CutoffAt),
		holdDeadline: shared.UTC(s.HoldDeadlineAt), share: s.Share, provisional: s.Provisional, confirmed: s.Confirmed,
		state: s.State, closedAt: shared.UTC(s.ClosedAt), version: s.Version,
	}, nil
}

func checkSnapshot(s Snapshot) error {
	if err := checkOpening(Opening{NodeID: s.NodeID, PickupDepartureID: s.PickupDepartureID, CutoffAt: s.CutoffAt, HoldDeadlineAt: s.HoldDeadlineAt, Share: s.Share}); err != nil {
		return err
	}
	switch {
	case s.ID != MakeID(s.NodeID, s.PickupDepartureID):
		return errors.New("bookId is not nodeId:pickupDepartureId")
	case s.Version < 1:
		return errors.New("a version of at least 1 is required")
	case s.Provisional < 0 || s.Confirmed < 0:
		return ErrCapacityUnderflow
	case s.Provisional+s.Confirmed > s.Share:
		return ErrPromiseCapacityExhausted
	case s.State != StateOpen && s.State != StateClosed:
		return fmt.Errorf("state %q is not OPEN or CLOSED", s.State)
	case s.State == StateOpen && !s.ClosedAt.IsZero():
		return errors.New("an OPEN book has no closedAt")
	case s.State == StateClosed && s.ClosedAt.IsZero():
		return errors.New("a CLOSED book needs closedAt")
	}
	return nil
}

// Hold takes one order of capacity as provisional. It refuses a CLOSED book or
// one past its hold deadline (ErrBookClosed) and a full book
// (ErrPromiseCapacityExhausted). When the hold fills the book it raises
// PromiseBookExhausted.
func (b *Book) Hold(now time.Time) ([]shared.Event, error) {
	if b.state != StateOpen || !now.Before(b.holdDeadline) {
		return nil, fmt.Errorf("%w: book %s is %s, holds stop %s", ErrBookClosed, b.id, b.state, b.holdDeadline.Format(time.RFC3339))
	}
	if b.provisional+b.confirmed >= b.share {
		return nil, fmt.Errorf("%w: book %s has no room left (share %d)", ErrPromiseCapacityExhausted, b.id, b.share)
	}
	b.provisional++
	b.version++
	if b.provisional+b.confirmed < b.share {
		return nil, nil
	}
	return []shared.Event{PromiseBookExhausted{
		Header: shared.Header{Version: b.version, At: shared.UTC(now)}, BookID: b.id, NodeID: b.nodeID, PickupDepartureID: b.pickupID, Share: b.share,
	}}, nil
}

// ConfirmHold moves one provisional hold to confirmed.
func (b *Book) ConfirmHold() error {
	if b.provisional < 1 {
		return fmt.Errorf("%w: book %s has no provisional hold to confirm", ErrCapacityUnderflow, b.id)
	}
	b.provisional--
	b.confirmed++
	b.version++
	return nil
}

// ReleaseProvisional gives back one provisional hold.
func (b *Book) ReleaseProvisional() error {
	if b.provisional < 1 {
		return fmt.Errorf("%w: book %s has no provisional hold to release", ErrCapacityUnderflow, b.id)
	}
	b.provisional--
	b.version++
	return nil
}

// ReleaseConfirmed gives back one confirmed hold.
func (b *Book) ReleaseConfirmed() error {
	if b.confirmed < 1 {
		return fmt.Errorf("%w: book %s has no confirmed hold to release", ErrCapacityUnderflow, b.id)
	}
	b.confirmed--
	b.version++
	return nil
}

// Close moves an OPEN book to CLOSED once the cutoff has been reached. A book
// that is already CLOSED, or whose cutoff is still ahead, is an illegal
// transition.
func (b *Book) Close(now time.Time) error {
	if b.state != StateOpen {
		return fmt.Errorf("%w: book %s is %s", shared.ErrIllegalTransition, b.id, b.state)
	}
	if now.Before(b.cutoffAt) {
		return fmt.Errorf("%w: book %s cutoff %s is ahead", shared.ErrIllegalTransition, b.id, b.cutoffAt.Format(time.RFC3339))
	}
	b.state = StateClosed
	b.closedAt = shared.UTC(now)
	b.version++
	return nil
}

// ID returns the bookId.
func (b *Book) ID() ID { return b.id }

// NodeID returns the node.
func (b *Book) NodeID() string { return b.nodeID }

// PickupDepartureID returns the pickup departure.
func (b *Book) PickupDepartureID() string { return b.pickupID }

// CutoffAt returns the pickup cutoff.
func (b *Book) CutoffAt() time.Time { return b.cutoffAt }

// HoldDeadlineAt returns the instant from which no hold is accepted.
func (b *Book) HoldDeadlineAt() time.Time { return b.holdDeadline }

// Share returns the orders the book may hold.
func (b *Book) Share() int { return b.share }

// Provisional returns the provisional holds.
func (b *Book) Provisional() int { return b.provisional }

// Confirmed returns the confirmed holds.
func (b *Book) Confirmed() int { return b.confirmed }

// Room returns the orders that can still be held.
func (b *Book) Room() int { return b.share - b.provisional - b.confirmed }

// State returns OPEN or CLOSED.
func (b *Book) State() State { return b.state }

// ClosedAt returns when the book was closed (zero while OPEN).
func (b *Book) ClosedAt() time.Time { return b.closedAt }

// Version returns the aggregate version.
func (b *Book) Version() int64 { return b.version }

// Snapshot returns the persisted form.
func (b *Book) Snapshot() Snapshot {
	return Snapshot{
		ID: b.id, NodeID: b.nodeID, PickupDepartureID: b.pickupID, CutoffAt: b.cutoffAt, HoldDeadlineAt: b.holdDeadline,
		Share: b.share, Provisional: b.provisional, Confirmed: b.confirmed, State: b.state, ClosedAt: b.closedAt, Version: b.version,
	}
}
