package promise

import (
	"errors"
	"fmt"
	"slices"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

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

// Snapshot returns the persisted form.
func (p *Promise) Snapshot() Snapshot {
	return Snapshot{
		ID: p.id, OfferID: p.offerID, OrderID: p.orderID, GroupNo: p.groupNo, State: p.state, DestinationPrefix: p.destination,
		Lines: slices.Clone(p.lines), Terms: p.terms, Witness: p.witness.Clone(), BookID: p.bookID, HoldExpiresAt: p.holdExpiresAt,
		Revisions: cloneRevisions(p.revisions), Judgement: p.Judgement(), AtRiskReason: p.atRiskReason, CancelReason: p.cancelReason,
		ReleaseReason: p.releaseReason, Unkeepable: p.unkeepable, MadeAt: p.madeAt, Version: p.version,
	}
}
