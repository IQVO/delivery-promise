// Package promise is the Promise aggregate: the network's commitment for one
// shipment group of one customer order: tier, shipBy at the witness node, the
// deliverBy window, the witness path, the basis and the policy version.
//
// States: PROVISIONAL, CONFIRMED, AT_RISK, KEPT, BROKEN, RELEASED, CANCELLED.
// Transitions (the model's table): PROVISIONAL -> CONFIRMED (Confirm) |
// RELEASED (ReleaseExpiredHold); CONFIRMED -> AT_RISK (FlagAtRisk) | CONFIRMED
// (Revise) | KEPT | BROKEN (Judge) | CANCELLED (Cancel); AT_RISK -> CONFIRMED
// (Revise) | KEPT | BROKEN | CANCELLED. KEPT, BROKEN, RELEASED and CANCELLED
// are terminal (ErrPromiseClosed).
//
// Invariants: deliverByEarliest <= deliverByLatest and shipBy < deliverByEarliest
// (ErrInvalidPromiseTerms); a fast-tier promise is SCHEDULED
// (ErrNominalBasisNotAllowedForTier); a provisional promise is confirmed only
// before holdExpiresAt (ErrPromiseHoldExpired); a revision never moves
// deliverByLatest earlier (ErrRevisionMovesEarlier); a promise covers lines
// shippable from its witness node (ErrGroupSpansNodes).
package promise

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// The Promise invariants of the model, by the model's names.
var (
	// ErrInvalidPromiseTerms: the terms break deliverByEarliest <= deliverByLatest
	// or shipBy < deliverByEarliest (422).
	ErrInvalidPromiseTerms = errors.New("invalid promise terms")
	// ErrNominalBasisNotAllowedForTier: only STANDARD may be NOMINAL (422).
	ErrNominalBasisNotAllowedForTier = errors.New("nominal basis not allowed for tier")
	// ErrPromiseHoldExpired: the hold is past holdExpiresAt (409).
	ErrPromiseHoldExpired = errors.New("promise hold expired")
	// ErrRevisionMovesEarlier: a revision would move deliverByLatest earlier (409).
	ErrRevisionMovesEarlier = errors.New("revision moves earlier")
	// ErrPromiseClosed: the promise is KEPT, BROKEN, RELEASED or CANCELLED (409).
	ErrPromiseClosed = errors.New("promise closed")
	// ErrGroupSpansNodes: a line of the group is not shippable from the witness node (422).
	ErrGroupSpansNodes = errors.New("group spans nodes")
)

// ID identifies a promise (promiseId).
type ID string

// State is the lifecycle state of a promise.
type State string

// The seven promise states.
const (
	StateProvisional State = "PROVISIONAL"
	StateConfirmed   State = "CONFIRMED"
	StateAtRisk      State = "AT_RISK"
	StateKept        State = "KEPT"
	StateBroken      State = "BROKEN"
	StateReleased    State = "RELEASED"
	StateCancelled   State = "CANCELLED"
)

var states = []State{StateProvisional, StateConfirmed, StateAtRisk, StateKept, StateBroken, StateReleased, StateCancelled}

// Valid reports whether s is one of the seven states.
func (s State) Valid() bool { return slices.Contains(states, s) }

// Terminal reports whether s is KEPT, BROKEN, RELEASED or CANCELLED.
func (s State) Terminal() bool {
	return s == StateKept || s == StateBroken || s == StateReleased || s == StateCancelled
}

// Active reports whether s is CONFIRMED or AT_RISK: the states a promise is
// shipped, revised, judged or cancelled from.
func (s State) Active() bool { return s == StateConfirmed || s == StateAtRisk }

// ReleaseHoldExpired is the reason of a released hold.
const ReleaseHoldExpired = "HOLD_EXPIRED"

// Terms are the PromiseTerms value object.
type Terms struct {
	Tier          shared.Tier
	ShipBy        time.Time
	Window        shared.Window
	Basis         shared.Basis
	PolicyVersion string
}

// Revision is the Revision value object: the terms before one re-promise.
type Revision struct {
	No            int
	PreviousTerms Terms
	Reason        string
	RevisedAt     time.Time
}

// Outcome is the judgement of a promise.
type Outcome string

// The two outcomes.
const (
	OutcomeKept   Outcome = "KEPT"
	OutcomeBroken Outcome = "BROKEN"
)

// Cause says why a promise is BROKEN.
type Cause string

// The three causes (gap G6): delivered late, a package lost, a package returned.
const (
	CauseLate     Cause = "LATE"
	CauseLost     Cause = "LOST"
	CauseReturned Cause = "RETURNED"
)

// Judgement is the verdict on a promise.
type Judgement struct {
	Outcome       Outcome
	DeliveredAt   time.Time // zero for LOST and RETURNED
	LateByMinutes int
	Cause         Cause // empty when KEPT
	JudgedAt      time.Time
}

// Promise is the aggregate root.
type Promise struct {
	id            ID
	offerID       string
	orderID       string
	groupNo       int
	state         State
	destination   string
	lines         []shared.Line
	terms         Terms
	witness       shared.Witness
	bookID        string
	holdExpiresAt time.Time
	revisions     []Revision
	judgement     *Judgement
	atRiskReason  string
	cancelReason  string
	releaseReason string
	unkeepable    bool
	madeAt        time.Time
	version       int64
}

// Snapshot is the persisted form of a promise. Restore validates it.
type Snapshot struct {
	ID                ID
	OfferID           string
	OrderID           string
	GroupNo           int
	State             State
	DestinationPrefix string
	Lines             []shared.Line
	Terms             Terms
	Witness           shared.Witness
	BookID            string
	HoldExpiresAt     time.Time
	Revisions         []Revision
	Judgement         *Judgement
	AtRiskReason      string
	CancelReason      string
	ReleaseReason     string
	Unkeepable        bool
	MadeAt            time.Time
	Version           int64
}

// Making is the input of Make.
type Making struct {
	ID                ID
	OfferID           string
	DestinationPrefix string
	Lines             []shared.Line
	Terms             Terms
	Witness           shared.Witness
	// BookID is the book the hold is taken on (nodeId:pickupDepartureId; for a
	// NOMINAL promise the nominal pickup book).
	BookID  string
	HoldTTL time.Duration
	// Available is the units of each item shippable from the witness node, net
	// of provisional holds.
	Available map[string]int
}

func validateTerms(t Terms) error {
	if !t.Tier.Valid() || !t.Basis.Valid() {
		return fmt.Errorf("%w: tier %q basis %q", ErrInvalidPromiseTerms, t.Tier, t.Basis)
	}
	if t.Basis == shared.BasisNominal && !t.Tier.AllowsNominal() {
		return fmt.Errorf("%w: %s", ErrNominalBasisNotAllowedForTier, t.Tier)
	}
	if t.PolicyVersion == "" || len(t.PolicyVersion) > shared.MaxID {
		return fmt.Errorf("%w: policyVersion is required", ErrInvalidPromiseTerms)
	}
	if err := t.Window.Validate(); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidPromiseTerms, err)
	}
	if t.ShipBy.IsZero() || !t.ShipBy.Before(t.Window.Earliest) {
		return fmt.Errorf("%w: shipBy must be before deliverByEarliest", ErrInvalidPromiseTerms)
	}
	return nil
}

func normTerms(t Terms) Terms {
	t.ShipBy = shared.UTC(t.ShipBy)
	t.Window = t.Window.UTC()
	return t
}

// Make creates a PROVISIONAL promise at version 1 and raises PromiseMade.
func Make(in Making, now time.Time) (*Promise, []shared.Event, error) {
	if !shared.ValidID(string(in.ID)) || !shared.ValidID(in.OfferID) {
		return nil, nil, shared.Invalid("promiseId and offerId must be valid ids")
	}
	if !shared.ValidPostalPrefix(in.DestinationPrefix) {
		return nil, nil, shared.Invalid("destinationPostalPrefix %q is not valid", in.DestinationPrefix)
	}
	if in.BookID == "" || len(in.BookID) > 2*shared.MaxID+1 {
		return nil, nil, shared.Invalid("bookId is required")
	}
	if in.HoldTTL <= 0 {
		return nil, nil, shared.Invalid("the hold TTL must be positive")
	}
	if err := shared.ValidateLines(in.Lines); err != nil {
		return nil, nil, err
	}
	if err := validateTerms(in.Terms); err != nil {
		return nil, nil, err
	}
	if err := in.Witness.Validate(in.Terms.Basis); err != nil {
		return nil, nil, err
	}
	for _, l := range in.Lines {
		if in.Available[l.CatalogItemID] < l.Quantity {
			return nil, nil, fmt.Errorf("%w: %s x%d is not shippable from %s", ErrGroupSpansNodes, l.CatalogItemID, l.Quantity, in.Witness.NodeID)
		}
	}
	now = shared.UTC(now)
	p := &Promise{
		id: in.ID, offerID: in.OfferID, state: StateProvisional, destination: in.DestinationPrefix, lines: slices.Clone(in.Lines),
		terms: normTerms(in.Terms), witness: in.Witness.Clone(), bookID: in.BookID, holdExpiresAt: now.Add(in.HoldTTL), madeAt: now, version: 1,
	}
	ev := PromiseMade{
		Header: shared.Header{Version: p.version, At: now}, PromiseID: p.id, OfferID: p.offerID, Terms: p.terms, Witness: p.witness.Clone(),
		HoldExpiresAt: p.holdExpiresAt, Lines: slices.Clone(p.lines),
	}
	return p, []shared.Event{ev}, nil
}

// UsesDeparture reports whether the witness path uses the departure as pickup
// or leg.
func (p *Promise) UsesDeparture(departureID string) bool {
	return p.witness.PickupDepartureID == departureID || slices.Contains(p.witness.Legs, departureID)
}

// UsesWave reports whether the witness path ends on the wave.
func (p *Promise) UsesWave(waveID string) bool { return p.witness.DeliveryWaveID == waveID }

// ID returns the promise id.
func (p *Promise) ID() ID { return p.id }

// OfferID returns the offer the promise was made from.
func (p *Promise) OfferID() string { return p.offerID }

// OrderID returns the order (empty until CONFIRMED).
func (p *Promise) OrderID() string { return p.orderID }

// GroupNo returns the order's group number (0 until CONFIRMED).
func (p *Promise) GroupNo() int { return p.groupNo }

// State returns the lifecycle state.
func (p *Promise) State() State { return p.state }

// DestinationPrefix returns the destination postal prefix.
func (p *Promise) DestinationPrefix() string { return p.destination }

// Lines returns a copy of the group's lines.
func (p *Promise) Lines() []shared.Line { return slices.Clone(p.lines) }

// Terms returns the current terms.
func (p *Promise) Terms() Terms { return p.terms }

// Witness returns a copy of the current witness path.
func (p *Promise) Witness() shared.Witness { return p.witness.Clone() }

// BookID returns the book the capacity is held on.
func (p *Promise) BookID() string { return p.bookID }

// HoldExpiresAt returns when a PROVISIONAL hold lapses.
func (p *Promise) HoldExpiresAt() time.Time { return p.holdExpiresAt }

// Revisions returns a copy of the revisions.
func (p *Promise) Revisions() []Revision { return slices.Clone(p.revisions) }

// Judgement returns the verdict, or nil before judgement.
func (p *Promise) Judgement() *Judgement {
	if p.judgement == nil {
		return nil
	}
	j := *p.judgement
	return &j
}

// AtRiskReason returns the reason while AT_RISK.
func (p *Promise) AtRiskReason() string { return p.atRiskReason }

// CancelReason returns the reason once CANCELLED.
func (p *Promise) CancelReason() string { return p.cancelReason }

// ReleaseReason returns the reason once RELEASED.
func (p *Promise) ReleaseReason() string { return p.releaseReason }

// Unkeepable reports whether PromiseUnkeepable was raised.
func (p *Promise) Unkeepable() bool { return p.unkeepable }

// MadeAt returns when the promise was made.
func (p *Promise) MadeAt() time.Time { return p.madeAt }

// Version returns the aggregate version.
func (p *Promise) Version() int64 { return p.version }
