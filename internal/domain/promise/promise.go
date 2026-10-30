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

// Restore rebuilds a persisted promise, validating every invariant.
func Restore(s Snapshot) (*Promise, error) {
	if err := checkSnapshot(s); err != nil {
		return nil, shared.Corrupt("promise %q: %v", s.ID, err)
	}
	p := &Promise{
		id: s.ID, offerID: s.OfferID, orderID: s.OrderID, groupNo: s.GroupNo, state: s.State, destination: s.DestinationPrefix,
		lines: slices.Clone(s.Lines), terms: normTerms(s.Terms), witness: s.Witness.Clone(), bookID: s.BookID,
		holdExpiresAt: shared.UTC(s.HoldExpiresAt), revisions: cloneRevisions(s.Revisions), atRiskReason: s.AtRiskReason,
		cancelReason: s.CancelReason, releaseReason: s.ReleaseReason, unkeepable: s.Unkeepable, madeAt: shared.UTC(s.MadeAt), version: s.Version,
	}
	if s.Judgement != nil {
		j := *s.Judgement
		j.DeliveredAt, j.JudgedAt = shared.UTC(j.DeliveredAt), shared.UTC(j.JudgedAt)
		p.judgement = &j
	}
	return p, nil
}

func cloneRevisions(in []Revision) []Revision {
	out := slices.Clone(in)
	for i := range out {
		out[i].PreviousTerms = normTerms(out[i].PreviousTerms)
		out[i].RevisedAt = shared.UTC(out[i].RevisedAt)
	}
	return out
}

func checkSnapshot(s Snapshot) error {
	if !shared.ValidID(string(s.ID)) || !shared.ValidID(s.OfferID) {
		return errors.New("promiseId and offerId must be valid ids")
	}
	if s.Version < 1 || s.MadeAt.IsZero() || s.HoldExpiresAt.IsZero() || s.BookID == "" {
		return errors.New("version, madeAt, holdExpiresAt and bookId are required")
	}
	if !s.State.Valid() {
		return fmt.Errorf("state %q is not a promise state", s.State)
	}
	if !shared.ValidPostalPrefix(s.DestinationPrefix) {
		return errors.New("destination postal prefix is not valid")
	}
	if err := checkValueObjects(s); err != nil {
		return err
	}
	return checkStateFields(s)
}

func checkValueObjects(s Snapshot) error {
	if err := shared.ValidateLines(s.Lines); err != nil {
		return err
	}
	if err := validateTerms(s.Terms); err != nil {
		return err
	}
	if err := s.Witness.Validate(s.Terms.Basis); err != nil {
		return err
	}
	for i, r := range s.Revisions {
		if r.No != i+1 || r.RevisedAt.IsZero() || validateTerms(r.PreviousTerms) != nil {
			return fmt.Errorf("revision %d is not numbered %d or carries invalid terms", r.No, i+1)
		}
	}
	return nil
}

func checkStateFields(s Snapshot) error {
	ordered := s.OrderID != "" && s.GroupNo >= 1
	switch s.State {
	case StateProvisional:
		return checkProvisional(s)
	case StateConfirmed, StateAtRisk:
		return checkActive(s, ordered)
	case StateKept, StateBroken:
		return checkJudged(s, ordered)
	case StateReleased:
		if s.ReleaseReason == "" || s.OrderID != "" {
			return errors.New("a RELEASED promise has a release reason and no order")
		}
	case StateCancelled:
		if s.CancelReason == "" || !ordered {
			return errors.New("a CANCELLED promise has an order and a cancel reason")
		}
	}
	return nil
}

func checkProvisional(s Snapshot) error {
	if s.OrderID != "" || s.GroupNo != 0 || s.Judgement != nil || len(s.Revisions) > 0 {
		return errors.New("a PROVISIONAL promise has no order, judgement or revision")
	}
	return nil
}

func checkActive(s Snapshot, ordered bool) error {
	if !ordered || s.Judgement != nil {
		return errors.New("a CONFIRMED or AT_RISK promise has an order and no judgement")
	}
	if (s.State == StateAtRisk) != (s.AtRiskReason != "") {
		return errors.New("atRiskReason is set exactly while AT_RISK")
	}
	return nil
}

func checkJudged(s Snapshot, ordered bool) error {
	j := s.Judgement
	if !ordered || j == nil || j.JudgedAt.IsZero() || string(j.Outcome) != string(s.State) {
		return errors.New("a KEPT or BROKEN promise has an order and the matching judgement")
	}
	if s.State == StateKept && (j.Cause != "" || j.LateByMinutes != 0 || j.DeliveredAt.IsZero()) {
		return errors.New("a KEPT judgement has a delivery and no cause")
	}
	if s.State == StateBroken && (!j.Cause.valid() || j.LateByMinutes < 0 || (j.Cause == CauseLate) != !j.DeliveredAt.IsZero()) {
		return errors.New("a BROKEN judgement has a cause; LATE has a delivery, LOST and RETURNED have none")
	}
	return nil
}

func (c Cause) valid() bool { return c == CauseLate || c == CauseLost || c == CauseReturned }

func (p *Promise) closed() error {
	if p.state.Terminal() {
		return fmt.Errorf("%w: promise %s is %s", ErrPromiseClosed, p.id, p.state)
	}
	return nil
}

func (p *Promise) illegal(command string) error {
	return fmt.Errorf("%w: %s on promise %s in state %s", shared.ErrIllegalTransition, command, p.id, p.state)
}

func (p *Promise) header(now time.Time) shared.Header {
	return shared.Header{Version: p.version, At: shared.UTC(now)}
}

// Confirm moves a PROVISIONAL promise, before holdExpiresAt, to CONFIRMED for
// an order group and raises PromiseConfirmed. It is idempotent: a promise
// already confirmed (or at risk) for the same order and group is a no-op that
// returns no events; for another order or group it is an illegal transition.
func (p *Promise) Confirm(orderID string, groupNo int, now time.Time) ([]shared.Event, error) {
	if !shared.ValidID(orderID) || groupNo < 1 || groupNo > shared.MaxLines {
		return nil, shared.Invalid("orderId and groupNo are required")
	}
	if err := p.closed(); err != nil {
		return nil, err
	}
	if p.state.Active() {
		if p.orderID == orderID && p.groupNo == groupNo {
			return nil, nil
		}
		return nil, p.illegal("Confirm for another order")
	}
	if !now.Before(p.holdExpiresAt) {
		return nil, fmt.Errorf("%w: promise %s held until %s", ErrPromiseHoldExpired, p.id, p.holdExpiresAt.Format(time.RFC3339))
	}
	p.state, p.orderID, p.groupNo = StateConfirmed, orderID, groupNo
	p.version++
	return []shared.Event{PromiseConfirmed{Header: p.header(now), PromiseID: p.id, OrderID: orderID, GroupNo: groupNo}}, nil
}

// FlagAtRisk moves a CONFIRMED promise to AT_RISK and raises PromiseAtRisk.
func (p *Promise) FlagAtRisk(reason string, now time.Time) ([]shared.Event, error) {
	if !shared.ValidTextN(reason, shared.MaxReason) {
		return nil, shared.Invalid("a reason is required")
	}
	if err := p.closed(); err != nil {
		return nil, err
	}
	if p.state != StateConfirmed {
		return nil, p.illegal("FlagPromiseAtRisk")
	}
	p.state, p.atRiskReason = StateAtRisk, reason
	p.version++
	return []shared.Event{PromiseAtRisk{Header: p.header(now), PromiseID: p.id, OrderID: p.orderID, Reason: reason, DetectedAt: shared.UTC(now)}}, nil
}

// Revising is the input of Revise.
type Revising struct {
	Terms   Terms
	Witness shared.Witness
	BookID  string
	Reason  string
}

// Revise moves a CONFIRMED or AT_RISK promise to the new terms and witness and
// back to CONFIRMED, appends a Revision and raises PromiseRevised. The new
// deliverByLatest is never earlier than the current one, and the tier never
// faster.
func (p *Promise) Revise(in Revising, now time.Time) ([]shared.Event, error) {
	if !shared.ValidTextN(in.Reason, shared.MaxReason) || in.BookID == "" {
		return nil, shared.Invalid("a reason and a bookId are required")
	}
	if err := p.closed(); err != nil {
		return nil, err
	}
	if !p.state.Active() {
		return nil, p.illegal("RevisePromise")
	}
	if err := validateTerms(in.Terms); err != nil {
		return nil, err
	}
	if err := in.Witness.Validate(in.Terms.Basis); err != nil {
		return nil, err
	}
	if in.Terms.Tier.Rank() < p.terms.Tier.Rank() {
		return nil, fmt.Errorf("%w: tier %s is faster than %s", ErrInvalidPromiseTerms, in.Terms.Tier, p.terms.Tier)
	}
	if in.Terms.Window.Latest.Before(p.terms.Window.Latest) {
		return nil, fmt.Errorf("%w: %s is before %s", ErrRevisionMovesEarlier, in.Terms.Window.Latest.UTC().Format(time.RFC3339), p.terms.Window.Latest.Format(time.RFC3339))
	}
	previous := p.terms.Window.Latest
	p.revisions = append(p.revisions, Revision{No: len(p.revisions) + 1, PreviousTerms: p.terms, Reason: in.Reason, RevisedAt: shared.UTC(now)})
	p.terms, p.witness, p.bookID = normTerms(in.Terms), in.Witness.Clone(), in.BookID
	p.state, p.atRiskReason, p.unkeepable = StateConfirmed, "", false
	p.version++
	return []shared.Event{PromiseRevised{
		Header: p.header(now), PromiseID: p.id, OrderID: p.orderID, RevisionNo: len(p.revisions), PreviousDeliverByLatest: previous,
		Terms: p.terms, NodeID: p.witness.NodeID, Reason: in.Reason,
	}}, nil
}

// MarkUnkeepable records that re-promising found no path for an AT_RISK
// promise and raises PromiseUnkeepable, once: a repeat returns no events.
func (p *Promise) MarkUnkeepable(now time.Time) ([]shared.Event, error) {
	if err := p.closed(); err != nil {
		return nil, err
	}
	if p.state != StateAtRisk {
		return nil, p.illegal("MarkUnkeepable")
	}
	if p.unkeepable {
		return nil, nil
	}
	p.unkeepable = true
	p.version++
	return []shared.Event{PromiseUnkeepable{Header: p.header(now), PromiseID: p.id, OrderID: p.orderID, GroupNo: p.groupNo, Reason: p.atRiskReason}}, nil
}

// Verdict is the input of Judge: either a delivery (DeliveredAt set, Cause
// empty) or a package loss or return (Cause LOST or RETURNED, At = the instant
// it was declared).
type Verdict struct {
	DeliveredAt time.Time
	Cause       Cause
	At          time.Time
}

// Judge closes a CONFIRMED or AT_RISK promise as KEPT or BROKEN. A delivery on
// or before deliverByLatest is KEPT; later is BROKEN (LATE) with lateByMinutes
// rounded up; LOST and RETURNED are BROKEN with lateByMinutes the rounded-up
// minutes from deliverByLatest to At (0 if not later).
func (p *Promise) Judge(v Verdict, now time.Time) ([]shared.Event, error) {
	if err := p.closed(); err != nil {
		return nil, err
	}
	if !p.state.Active() {
		return nil, p.illegal("JudgePromise")
	}
	j, err := p.verdict(v, now)
	if err != nil {
		return nil, err
	}
	p.judgement = &j
	p.version++
	if j.Outcome == OutcomeKept {
		p.state = StateKept
		return []shared.Event{PromiseKept{Header: p.header(now), PromiseID: p.id, OrderID: p.orderID, NodeID: p.witness.NodeID, DeliveredAt: j.DeliveredAt, DeliverByLatest: p.terms.Window.Latest}}, nil
	}
	p.state = StateBroken
	return []shared.Event{PromiseBroken{
		Header: p.header(now), PromiseID: p.id, OrderID: p.orderID, NodeID: p.witness.NodeID, DeliveredAt: j.DeliveredAt,
		DeliverByLatest: p.terms.Window.Latest, LateByMinutes: j.LateByMinutes, Cause: j.Cause,
	}}, nil
}

func (p *Promise) verdict(v Verdict, now time.Time) (Judgement, error) {
	latest := p.terms.Window.Latest
	judged := shared.UTC(now)
	switch v.Cause {
	case "":
		if v.DeliveredAt.IsZero() {
			return Judgement{}, shared.Invalid("a delivery needs deliveredAt")
		}
		d := shared.UTC(v.DeliveredAt)
		if !d.After(latest) {
			return Judgement{Outcome: OutcomeKept, DeliveredAt: d, JudgedAt: judged}, nil
		}
		return Judgement{Outcome: OutcomeBroken, DeliveredAt: d, LateByMinutes: ceilMinutes(d.Sub(latest)), Cause: CauseLate, JudgedAt: judged}, nil
	case CauseLost, CauseReturned:
		if v.At.IsZero() {
			return Judgement{}, shared.Invalid("a loss or return needs the instant it was declared")
		}
		return Judgement{Outcome: OutcomeBroken, LateByMinutes: ceilMinutes(max(0, v.At.Sub(latest))), Cause: v.Cause, JudgedAt: judged}, nil
	default:
		return Judgement{}, shared.Invalid("cause %q is not LOST or RETURNED", v.Cause)
	}
}

func ceilMinutes(d time.Duration) int {
	m := d / time.Minute
	if d%time.Minute > 0 {
		m++
	}
	return int(m)
}

// Cancel moves a CONFIRMED or AT_RISK promise to CANCELLED and raises
// PromiseCancelled.
func (p *Promise) Cancel(reason string, now time.Time) ([]shared.Event, error) {
	if !shared.ValidTextN(reason, shared.MaxReason) {
		return nil, shared.Invalid("a reason is required")
	}
	if err := p.closed(); err != nil {
		return nil, err
	}
	if !p.state.Active() {
		return nil, p.illegal("CancelPromise")
	}
	p.state, p.cancelReason = StateCancelled, reason
	p.version++
	return []shared.Event{PromiseCancelled{Header: p.header(now), PromiseID: p.id, OrderID: p.orderID, Reason: reason}}, nil
}

// ReleaseExpiredHold moves a PROVISIONAL promise whose hold is past
// holdExpiresAt to RELEASED and raises PromiseReleased.
func (p *Promise) ReleaseExpiredHold(now time.Time) ([]shared.Event, error) {
	if err := p.closed(); err != nil {
		return nil, err
	}
	if p.state != StateProvisional || now.Before(p.holdExpiresAt) {
		return nil, p.illegal("ReleaseExpiredHolds")
	}
	p.state, p.releaseReason = StateReleased, ReleaseHoldExpired
	p.version++
	return []shared.Event{PromiseReleased{Header: p.header(now), PromiseID: p.id, Reason: ReleaseHoldExpired}}, nil
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

// Snapshot returns the persisted form.
func (p *Promise) Snapshot() Snapshot {
	return Snapshot{
		ID: p.id, OfferID: p.offerID, OrderID: p.orderID, GroupNo: p.groupNo, State: p.state, DestinationPrefix: p.destination,
		Lines: slices.Clone(p.lines), Terms: p.terms, Witness: p.witness.Clone(), BookID: p.bookID, HoldExpiresAt: p.holdExpiresAt,
		Revisions: cloneRevisions(p.revisions), Judgement: p.Judgement(), AtRiskReason: p.atRiskReason, CancelReason: p.cancelReason,
		ReleaseReason: p.releaseReason, Unkeepable: p.unkeepable, MadeAt: p.madeAt, Version: p.version,
	}
}
