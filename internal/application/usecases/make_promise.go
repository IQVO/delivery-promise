package usecases

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// MakePromiseCommand is the input of MakePromise.
type MakePromiseCommand struct {
	OfferID string
	Lines   []shared.Line
}

// MakePromiseResult is what MakePromise returns: Created is false when the
// request was a replay of an earlier one.
type MakePromiseResult struct {
	Promise views.Promise
	Created bool
}

// MakePromise consumes a QUOTED offer, creates the PROVISIONAL promise and
// holds one order of capacity on the witness PromiseBook (opened lazily), in ONE
// transaction: the documented exception of gap G22. A retry for an offer that
// already backs a promise with the same lines returns that promise.
func (s *Service) MakePromise(ctx context.Context, cmd MakePromiseCommand) (MakePromiseResult, error) {
	if !shared.ValidID(cmd.OfferID) {
		err := shared.Invalid("offerId %q is not a valid id", cmd.OfferID)
		s.recordMakePromise(ctx, MakePromiseResult{}, err)
		return MakePromiseResult{}, err
	}
	if err := shared.ValidateLines(cmd.Lines); err != nil {
		s.recordMakePromise(ctx, MakePromiseResult{}, err)
		return MakePromiseResult{}, err
	}
	var res MakePromiseResult
	err := s.tx(ctx, func(ctx context.Context) error {
		var err error
		res, err = s.makePromise(ctx, cmd)
		return err
	})
	s.recordMakePromise(ctx, res, err)
	return res, err
}

func (s *Service) makePromise(ctx context.Context, cmd MakePromiseCommand) (MakePromiseResult, error) {
	now := s.Clock.Now().UTC()
	o, err := s.Offers.Get(ctx, offer.ID(cmd.OfferID))
	if err != nil {
		return MakePromiseResult{}, err
	}
	if o.State() == offer.StateConsumed {
		existing, gerr := s.Promises.GetByOffer(ctx, cmd.OfferID)
		if err := missingOrFailed(gerr); err != nil {
			return MakePromiseResult{}, err
		}
		if gerr == nil && shared.SameLines(existing.Lines(), cmd.Lines) {
			return MakePromiseResult{Promise: views.FromPromise(existing)}, nil
		}
	}
	if !shared.SameLines(o.Lines(), cmd.Lines) {
		return MakePromiseResult{}, shared.Invalid("the lines must be those of offer %s", cmd.OfferID)
	}
	pol, err := s.currentPolicy(ctx)
	if err != nil {
		return MakePromiseResult{}, err
	}
	if err := o.Consume(now); err != nil {
		return MakePromiseResult{}, err
	}
	if err := s.revalidate(ctx, o.Witness(), pol); err != nil {
		return MakePromiseResult{}, err
	}
	avail, err := s.nodeAvailability(ctx, o.Witness().NodeID, o.Lines())
	if err != nil {
		return MakePromiseResult{}, err
	}
	b, bevents, err := s.holdBook(ctx, o, pol, now)
	if err != nil {
		return MakePromiseResult{}, err
	}
	p, pevents, err := promise.Make(promise.Making{
		ID: s.IDs.NewPromiseID(), OfferID: cmd.OfferID, DestinationPrefix: o.DestinationPrefix(), Lines: o.Lines(),
		Terms:   promise.Terms{Tier: o.Tier(), ShipBy: o.PickupCutoffAt(), Window: o.Window(), Basis: o.Basis(), PolicyVersion: o.PolicyVersion()},
		Witness: o.Witness(), BookID: string(b.ID()), HoldTTL: pol.HoldTTL(), Available: avail,
	}, now)
	if err != nil {
		return MakePromiseResult{}, err
	}
	for _, save := range []func() error{
		func() error { return s.Offers.Save(ctx, o) },
		func() error { return s.Books.Save(ctx, b) },
		func() error { return s.Promises.Save(ctx, p) },
		func() error { return s.Outbox.Append(ctx, append(pevents, bevents...)...) },
	} {
		if err := save(); err != nil {
			return MakePromiseResult{}, err
		}
	}
	return MakePromiseResult{Promise: views.FromPromise(p), Created: true}, nil
}

// refusal is the error of a witness path that is no longer feasible: checkout
// treats it like a full book and re-quotes (ADR 0002, A27).
func refusal(format string, args ...any) error {
	return fmt.Errorf("%w: %s", book.ErrPromiseCapacityExhausted, fmt.Sprintf(format, args...))
}

// revalidate re-checks the offer's witness path against the current read
// models: the node is ACTIVE, and the pickup, every leg and the wave still
// exist, are not cancelled and have room. A NOMINAL path has only the node.
func (s *Service) revalidate(ctx context.Context, w shared.Witness, pol *policy.Policy) error {
	n, err := s.Network.GetNode(ctx, w.NodeID)
	if err := missingOrFailed(err); err != nil {
		return err
	}
	if err != nil || n.State != readmodel.NodeActive {
		return refusal("node %s is not available", w.NodeID)
	}
	if w.PickupDepartureID == "" {
		return nil
	}
	for _, id := range append([]string{w.PickupDepartureID}, w.Legs...) {
		if err := s.revalidateDeparture(ctx, id, pol); err != nil {
			return err
		}
	}
	return s.revalidateWave(ctx, w.DeliveryWaveID)
}

func (s *Service) revalidateDeparture(ctx context.Context, id string, pol *policy.Policy) error {
	d, err := s.Network.GetDeparture(ctx, id)
	if err := missingOrFailed(err); err != nil {
		return err
	}
	if err != nil || !d.Scheduled || d.Cancelled || d.Remaining < pol.PackagesPerOrder() {
		return refusal("departure %s is no longer available", id)
	}
	return nil
}

func (s *Service) revalidateWave(ctx context.Context, id string) error {
	wave, err := s.Network.GetWave(ctx, id)
	if err := missingOrFailed(err); err != nil {
		return err
	}
	if err != nil || !wave.Scheduled || wave.Cancelled || wave.Remaining < 1 {
		return refusal("wave %s is no longer available", id)
	}
	return nil
}

// missingOrFailed lets a read model miss (the row is gone, so the witness is
// refused by the caller) pass as nil and returns any other read failure, which
// must not be mistaken for a full book.
func missingOrFailed(err error) error {
	if err != nil && !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	return nil
}

// holdBook loads (or lazily opens) the witness book and takes the hold.
func (s *Service) holdBook(ctx context.Context, o *offer.Offer, pol *policy.Policy, now time.Time) (*book.Book, []shared.Event, error) {
	w := o.Witness()
	id := search.BookIDFor(w, o.PickupCutoffAt())
	b, err := s.Books.Get(ctx, id)
	if errors.Is(err, ports.ErrNotFound) {
		b, err = s.openBook(ctx, w, o.PickupCutoffAt(), pol)
	}
	if err != nil {
		return nil, nil, err
	}
	evs, err := b.Hold(now)
	return b, evs, err
}

func (s *Service) openBook(ctx context.Context, w shared.Witness, cutoff time.Time, pol *policy.Policy) (*book.Book, error) {
	row, err := s.Network.GetNode(ctx, w.NodeID)
	if err != nil {
		return nil, err
	}
	pickup := w.PickupDepartureID
	if pickup == "" {
		pickup = search.NominalPickupID(cutoff)
	}
	op, ok := search.OpeningFor(ToSearchNode(row), pickup, cutoff, pol)
	if !ok {
		return nil, refusal("node %s has no operational profile", w.NodeID)
	}
	b, err := book.Open(op)
	if err != nil {
		return nil, err
	}
	// Persist the opening (version 1) before the hold (version 2): Save accepts
	// only the next version, and the surrounding transaction rolls the opening
	// back if the hold is refused.
	if err := s.Books.Save(ctx, b); err != nil {
		return nil, err
	}
	return b, nil
}
