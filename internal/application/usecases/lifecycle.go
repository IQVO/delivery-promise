package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// ConfirmPromise confirms a PROVISIONAL promise for an order group: the hold
// moves from provisional to confirmed on its book and PromiseConfirmed is
// raised, in one transaction. It is idempotent (ADR 0002, A16): a promise
// already confirmed for the same order and group is returned unchanged.
func (s *Service) ConfirmPromise(ctx context.Context, id, orderID string, groupNo int) (views.Promise, error) {
	var out views.Promise
	err := s.tx(ctx, func(ctx context.Context) error {
		p, err := s.Promises.Get(ctx, promise.ID(id))
		if err != nil {
			return err
		}
		if err := s.confirm(ctx, p, orderID, groupNo); err != nil {
			return err
		}
		out = views.FromPromise(p)
		return nil
	})
	return out, err
}

func (s *Service) confirm(ctx context.Context, p *promise.Promise, orderID string, groupNo int) error {
	events, err := p.Confirm(orderID, groupNo, s.Clock.Now())
	if err != nil || len(events) == 0 {
		return err
	}
	b, err := s.Books.Get(ctx, book.ID(p.BookID()))
	if err != nil {
		return err
	}
	if err := b.ConfirmHold(); err != nil {
		return err
	}
	return s.saveAll(ctx, p, b, events)
}

// saveAll saves a promise and its book and appends the events.
func (s *Service) saveAll(ctx context.Context, p *promise.Promise, b *book.Book, events []shared.Event) error {
	if err := s.Promises.Save(ctx, p); err != nil {
		return err
	}
	if b != nil {
		if err := s.Books.Save(ctx, b); err != nil {
			return err
		}
	}
	return s.Outbox.Append(ctx, events...)
}

// CancelPromises cancels the CONFIRMED and AT_RISK promises of an order (the
// named groups, every group when none is named) and releases their confirmed
// capacity (CancelPromise, gap G21). Promises already closed are skipped.
func (s *Service) CancelPromises(ctx context.Context, orderID string, groupNos []int, reason string) error {
	ps, err := s.Promises.ListByOrder(ctx, orderID)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if len(groupNos) > 0 && !containsInt(groupNos, p.GroupNo()) {
			continue
		}
		if err := s.tx(ctx, func(ctx context.Context) error { return s.cancelOne(ctx, p.ID(), reason) }); err != nil {
			return err
		}
	}
	return nil
}

func containsInt(xs []int, x int) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func (s *Service) cancelOne(ctx context.Context, id promise.ID, reason string) error {
	p, err := s.Promises.Get(ctx, id)
	if err != nil {
		return err
	}
	events, err := p.Cancel(reason, s.Clock.Now())
	if errors.Is(err, promise.ErrPromiseClosed) || errors.Is(err, shared.ErrIllegalTransition) {
		return nil
	}
	if err != nil {
		return err
	}
	b, err := s.Books.Get(ctx, book.ID(p.BookID()))
	if err != nil {
		return err
	}
	if err := b.ReleaseConfirmed(); err != nil {
		return err
	}
	return s.saveAll(ctx, p, b, events)
}

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

// PublishPromisePolicy stores the next policy version (gap G19).
func (s *Service) PublishPromisePolicy(ctx context.Context, in policy.Input) (views.Policy, error) {
	var out views.Policy
	err := s.tx(ctx, func(ctx context.Context) error {
		cur, err := s.currentPolicy(ctx)
		if err != nil {
			return err
		}
		p, events, err := policy.Publish(cur.Version(), in, s.Clock.Now())
		if err != nil {
			return err
		}
		if err := s.Policies.Save(ctx, p); err != nil {
			return err
		}
		out = views.FromPolicy(p)
		return s.Outbox.Append(ctx, events...)
	})
	return out, err
}

// CurrentPolicy returns the current promise policy (the default v0 when none
// was published).
func (s *Service) CurrentPolicy(ctx context.Context) (views.Policy, error) {
	p, err := s.currentPolicy(ctx)
	if err != nil {
		return views.Policy{}, err
	}
	return views.FromPolicy(p), nil
}

// GetOffer returns an offer in any state.
func (s *Service) GetOffer(ctx context.Context, id string) (views.Offer, error) {
	o, err := s.Offers.Get(ctx, offer.ID(id))
	if err != nil {
		return views.Offer{}, err
	}
	return views.FromOffer(o), nil
}

// GetPromise returns a promise in any state.
func (s *Service) GetPromise(ctx context.Context, id string) (views.Promise, error) {
	p, err := s.Promises.Get(ctx, promise.ID(id))
	if err != nil {
		return views.Promise{}, err
	}
	return views.FromPromise(p), nil
}

// ListPromisesByOrder returns the promises of an order, one per group.
func (s *Service) ListPromisesByOrder(ctx context.Context, orderID string) (views.PromiseList, error) {
	if !shared.ValidID(orderID) {
		return views.PromiseList{}, shared.Invalid("orderId %q is not a valid id", orderID)
	}
	ps, err := s.Promises.ListByOrder(ctx, orderID)
	if err != nil {
		return views.PromiseList{}, err
	}
	out := views.PromiseList{Items: make([]views.Promise, 0, len(ps))}
	for _, p := range ps {
		out.Items = append(out.Items, views.FromPromise(p))
	}
	return out, nil
}

// ListPromiseBooks returns the books of a node whose cutoff falls on the date
// (YYYY-MM-DD, the node's local date).
func (s *Service) ListPromiseBooks(ctx context.Context, nodeID, date string) (views.BookList, error) {
	if !shared.ValidID(nodeID) {
		return views.BookList{}, shared.Invalid("nodeId %q is not a valid id", nodeID)
	}
	day, err := parseDate(date)
	if err != nil {
		return views.BookList{}, err
	}
	loc := time.UTC
	if row, err := s.Network.GetNode(ctx, nodeID); err == nil {
		if l, lerr := time.LoadLocation(row.Timezone); lerr == nil {
			loc = l
		}
	} else if !errors.Is(err, ports.ErrNotFound) {
		return views.BookList{}, err
	}
	from := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, loc)
	bs, err := s.Books.List(ctx, repository.BookFilter{NodeIDs: []string{nodeID}, CutoffFrom: from, CutoffUntil: from.AddDate(0, 0, 1)})
	if err != nil {
		return views.BookList{}, err
	}
	out := views.BookList{Items: make([]views.Book, 0, len(bs))}
	for _, b := range bs {
		out.Items = append(out.Items, views.FromBook(b))
	}
	return out, nil
}

func parseDate(s string) (time.Time, error) {
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, shared.Invalid("date %q is not YYYY-MM-DD", s)
	}
	return d, nil
}
