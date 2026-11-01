package promise

import (
	"fmt"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

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
