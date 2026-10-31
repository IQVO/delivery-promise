package usecases

import (
	"context"
	"errors"

	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// ReleaseExpiredHolds releases the PROVISIONAL promises whose hold lapsed and
// gives their capacity back (ReleaseExpiredHolds). It returns how many it
// released; a promise confirmed in the meantime is skipped.
func (s *Service) ReleaseExpiredHolds(ctx context.Context, limit int) (int, error) {
	ids, err := s.Promises.ListHoldsExpired(ctx, s.Clock.Now(), limit)
	if err != nil {
		return 0, err
	}
	released := 0
	for _, id := range ids {
		var done bool
		err := s.tx(ctx, func(ctx context.Context) error {
			var err error
			done, err = s.releaseOne(ctx, id)
			return err
		})
		if err != nil {
			return released, err
		}
		if done {
			released++
		}
	}
	return released, nil
}

func (s *Service) releaseOne(ctx context.Context, id promise.ID) (bool, error) {
	p, err := s.Promises.Get(ctx, id)
	if err != nil {
		return false, err
	}
	events, err := p.ReleaseExpiredHold(s.Clock.Now())
	if errors.Is(err, promise.ErrPromiseClosed) || errors.Is(err, shared.ErrIllegalTransition) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	b, err := s.Books.Get(ctx, book.ID(p.BookID()))
	if err != nil {
		return false, err
	}
	if err := b.ReleaseProvisional(); err != nil {
		return false, err
	}
	return true, s.saveAll(ctx, p, b, events)
}

// ExpireOffers moves the QUOTED offers past their expiry to EXPIRED
// (ExpireOffers). It returns how many it expired; an offer consumed in the
// meantime is skipped.
func (s *Service) ExpireOffers(ctx context.Context, limit int) (int, error) {
	ids, err := s.Offers.ListExpired(ctx, s.Clock.Now(), limit)
	if err != nil {
		return 0, err
	}
	expired := 0
	for _, id := range ids {
		err := s.tx(ctx, func(ctx context.Context) error {
			o, err := s.Offers.Get(ctx, id)
			if err != nil {
				return err
			}
			if err := o.Expire(s.Clock.Now()); err != nil {
				return nil //nolint:nilerr // consumed or already expired: nothing to do
			}
			expired++
			return s.Offers.Save(ctx, o)
		})
		if err != nil {
			return expired, err
		}
	}
	return expired, nil
}

// CloseBooks closes the OPEN books whose cutoff has been reached (CloseBook,
// driven by the clock, gap G9). It returns how many it closed.
func (s *Service) CloseBooks(ctx context.Context, limit int) (int, error) {
	ids, err := s.Books.ListOpenDue(ctx, s.Clock.Now(), limit)
	if err != nil {
		return 0, err
	}
	closed := 0
	for _, id := range ids {
		err := s.tx(ctx, func(ctx context.Context) error {
			b, err := s.Books.Get(ctx, id)
			if err != nil {
				return err
			}
			if err := b.Close(s.Clock.Now()); err != nil {
				return nil //nolint:nilerr // already closed: nothing to do
			}
			closed++
			return s.Books.Save(ctx, b)
		})
		if err != nil {
			return closed, err
		}
	}
	return closed, nil
}
