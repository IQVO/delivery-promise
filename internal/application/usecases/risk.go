package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Risk reasons carried by PromiseAtRisk.
const (
	ReasonNodeUnsourceable  = "NODE_UNSOURCEABLE"
	ReasonPackageException  = "PACKAGE_EXCEPTION"
	ReasonInductionDeferred = "INDUCTION_DEFERRED"
	ReasonDepartureCanceled = "DEPARTURE_CANCELLED"
	ReasonWaveCanceled      = "WAVE_CANCELLED"
	ReasonBookingFailed     = "BOOKING_FAILED"
	ReasonBookingMoved      = "BOOKING_MOVED"
)

// flagAtRisk moves the CONFIRMED promises among ids to AT_RISK inside the
// caller's unit of work and returns the ids that are AT_RISK afterwards (they
// still need a revision). Promises that are closed or not yet confirmed are
// skipped.
func (s *Service) flagAtRisk(ctx context.Context, ids []promise.ID, reason string) ([]promise.ID, error) {
	var pending []promise.ID
	for _, id := range ids {
		p, err := s.Promises.Get(ctx, id)
		if errors.Is(err, ports.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if p.State() == promise.StateConfirmed {
			events, err := p.FlagAtRisk(reason, s.Clock.Now())
			if err != nil {
				return nil, err
			}
			if err := s.saveAll(ctx, p, nil, events); err != nil {
				return nil, err
			}
		}
		if p.State() == promise.StateAtRisk {
			pending = append(pending, id)
		}
	}
	return pending, nil
}

// ReviseAll re-promises the AT_RISK promises among ids after their risk was
// committed. A failure of one is logged and left to the RepriceAtRisk sweeper.
func (s *Service) ReviseAll(ctx context.Context, ids []promise.ID) {
	for _, id := range ids {
		if err := s.ReviseAtRisk(ctx, id); err != nil {
			s.Logger.WarnContext(ctx, "re-promise failed; the sweeper retries", "promiseId", id, "error", err)
		}
	}
}

// RepriceAtRisk is the sweeper: it retries the re-promise of AT_RISK
// promises, including those PromiseUnkeepable was raised for (a new departure
// may have made them feasible). It returns how many it examined.
func (s *Service) RepriceAtRisk(ctx context.Context, limit int) (int, error) {
	ids, err := s.Promises.ListAtRisk(ctx, limit)
	if err != nil {
		return 0, err
	}
	s.ReviseAll(ctx, ids)
	return len(ids), nil
}

func toSet(xs []string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

// ReviseAtRisk is RevisePromise: it searches again for the promise's group
// from now, for its tier and then the slower ones down to STANDARD, skipping the
// excluded nodes and departures of its assignment and, once the assignment is
// locked, every node but the locked one. The first candidate that does not move
// deliverByLatest earlier and whose book accepts the hold becomes the new
// witness; when none exists PromiseUnkeepable is raised, once.
func (s *Service) ReviseAtRisk(ctx context.Context, id promise.ID) error {
	p, err := s.Promises.Get(ctx, id)
	if err != nil {
		return err
	}
	if p.State() != promise.StateAtRisk {
		return nil
	}
	now := s.Clock.Now().UTC()
	pol, err := s.currentPolicy(ctx)
	if err != nil {
		return err
	}
	opt := searchOptions{singleGroup: true}
	if a, err := s.Shipments.FindAssignmentByPromise(ctx, string(id)); err == nil {
		opt.excludeNodes, opt.excludeDepartures = toSet(a.ExcludedNodes), toSet(a.ExcludedDepartures)
		if a.Locked {
			opt.onlyNode = a.NodeID
		}
	} else if !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	tiers := p.Terms().Tier.SlowerOrEqual()
	in, err := s.searchInput(ctx, now, pol, p.DestinationPrefix(), p.Lines(), tiers, opt)
	if err != nil {
		return err
	}
	res := search.Run(in)
	for _, c := range candidatesInOrder(res, tiers) {
		if c.Window.Latest.Before(p.Terms().Window.Latest) {
			continue
		}
		moved, err := s.tryRevise(ctx, id, c, pol)
		if err != nil {
			return err
		}
		if moved {
			return nil
		}
	}
	return s.tx(ctx, func(ctx context.Context) error {
		cur, err := s.Promises.Get(ctx, id)
		if err != nil {
			return err
		}
		events, err := cur.MarkUnkeepable(s.Clock.Now())
		if err != nil || len(events) == 0 {
			return ignoreStale(err)
		}
		return s.saveAll(ctx, cur, nil, events)
	})
}

// ignoreStale drops the errors that only say the promise is no longer AT_RISK.
func ignoreStale(err error) error {
	if errors.Is(err, promise.ErrPromiseClosed) || errors.Is(err, shared.ErrIllegalTransition) {
		return nil
	}
	return err
}

func candidatesInOrder(res search.Result, tiers []shared.Tier) []search.Candidate {
	if len(res.Groups) == 0 {
		return nil
	}
	var out []search.Candidate
	for _, t := range tiers {
		out = append(out, res.Groups[0].Candidates[t]...)
	}
	return out
}

// tryRevise applies one candidate in a transaction: it holds and confirms the
// new book, releases the old one and revises the promise. A full or closed new
// book returns false so the next candidate is tried.
func (s *Service) tryRevise(ctx context.Context, id promise.ID, c search.Candidate, pol *policy.Policy) (bool, error) {
	moved := false
	err := s.tx(ctx, func(ctx context.Context) error {
		moved = false
		p, err := s.Promises.Get(ctx, id)
		if err != nil {
			return err
		}
		if p.State() != promise.StateAtRisk {
			return nil
		}
		events, err := s.moveCapacity(ctx, p, c, pol)
		if err != nil {
			return err
		}
		revEvents, err := p.Revise(promise.Revising{
			Terms:   promise.Terms{Tier: c.Tier, ShipBy: c.ShipBy, Window: c.Window, Basis: c.Basis, PolicyVersion: pol.VersionName()},
			Witness: c.Witness, BookID: c.BookID, Reason: p.AtRiskReason(),
		}, s.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.Promises.Save(ctx, p); err != nil {
			return err
		}
		moved = true
		return s.Outbox.Append(ctx, append(revEvents, events...)...)
	})
	if errors.Is(err, book.ErrPromiseCapacityExhausted) || errors.Is(err, book.ErrBookClosed) || errors.Is(err, promise.ErrRevisionMovesEarlier) {
		return false, nil
	}
	return moved, err
}

// moveCapacity holds and confirms the candidate's book and releases the
// promise's current one, unless they are the same book.
func (s *Service) moveCapacity(ctx context.Context, p *promise.Promise, c search.Candidate, pol *policy.Policy) ([]shared.Event, error) {
	if c.BookID == p.BookID() {
		return nil, nil
	}
	nb, err := s.Books.Get(ctx, book.ID(c.BookID))
	if errors.Is(err, ports.ErrNotFound) {
		nb, err = s.openBook(ctx, c.Witness, c.ShipBy, pol)
	}
	if err != nil {
		return nil, err
	}
	events, err := nb.Hold(s.Clock.Now())
	if err != nil {
		return nil, err
	}
	// Save accepts only the next version, so each command is saved on its own.
	if err := s.Books.Save(ctx, nb); err != nil {
		return nil, err
	}
	if err := nb.ConfirmHold(); err != nil {
		return nil, err
	}
	if err := s.Books.Save(ctx, nb); err != nil {
		return nil, err
	}
	old, err := s.Books.Get(ctx, book.ID(p.BookID()))
	if err != nil {
		return nil, err
	}
	if err := old.ReleaseConfirmed(); err != nil {
		return nil, err
	}
	return events, s.Books.Save(ctx, old)
}
