package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// rehomeOnLock moves the capacity charge of a confirmed promise to the node
// order-sourcing locked for it when that is not the node it was quoted on
// (ADR 0016): the witness book is released and the locked node's book charged,
// in the transaction of the FulfillmentLocked projection. It does nothing when
// the lock is on the quoted node, when the event does not say which departure
// (an older producer), when the promise is not active, or when the locked
// node's book cannot take the charge (unknown departure, full or closed): the
// lock is a fact that already happened, so it is logged and never retried.
func (s *Service) rehomeOnLock(ctx context.Context, e FulfillmentLocked) error {
	if e.PromiseID == "" || e.NodeID == "" || e.PickupDepartureID == "" {
		return nil
	}
	p, err := s.Promises.Get(ctx, promise.ID(e.PromiseID))
	if notFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	w := p.Witness()
	if !p.State().Active() || w.NodeID == e.NodeID && w.PickupDepartureID == e.PickupDepartureID {
		return nil
	}
	w.NodeID, w.PickupDepartureID = e.NodeID, e.PickupDepartureID
	if ok, err := s.departureKnown(ctx, w.PickupDepartureID); err != nil || !ok {
		if err == nil {
			s.Logger.WarnContext(ctx, "locked departure is unknown: the charge stays on the quoted node", "promiseId", e.PromiseID, "nodeId", e.NodeID, "departureId", e.PickupDepartureID)
		}
		return err
	}
	return s.moveChargeTo(ctx, p, w)
}

func (s *Service) departureKnown(ctx context.Context, id string) (bool, error) {
	_, err := s.Network.GetDeparture(ctx, id)
	if notFound(err) {
		return false, nil
	}
	return err == nil, err
}

// moveChargeTo holds the confirmed charge on the book of w, releases the
// promise's current book and re-points the promise at w.
func (s *Service) moveChargeTo(ctx context.Context, p *promise.Promise, w shared.Witness) error {
	old, err := s.Books.Get(ctx, book.ID(p.BookID()))
	if err != nil {
		return err
	}
	pol, err := s.currentPolicy(ctx)
	if err != nil {
		return err
	}
	newID := search.BookIDFor(w, old.CutoffAt())
	events, err := s.moveCapacity(ctx, p, search.Candidate{BookID: string(newID), Witness: w, ShipBy: old.CutoffAt()}, pol)
	if errors.Is(err, book.ErrPromiseCapacityExhausted) || errors.Is(err, book.ErrBookClosed) {
		s.Logger.WarnContext(ctx, "locked node cannot take the charge: it stays on the quoted node", "promiseId", string(p.ID()), "nodeId", w.NodeID, "error", err)
		return nil
	}
	if err != nil {
		return err
	}
	if err := p.Rehome(w, string(newID)); err != nil {
		return err
	}
	if err := s.Promises.Save(ctx, p); err != nil {
		return err
	}
	return s.Outbox.Append(ctx, events...)
}
