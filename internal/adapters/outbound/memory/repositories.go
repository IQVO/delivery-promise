package memory

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
)

// versionOK applies the optimistic-concurrency rule of the ports package: a
// create (version 1) expects no stored row, an update expects the previous one.
func versionOK(stored int64, exists bool, version int64) bool {
	if version == 1 {
		return !exists
	}
	return exists && stored == version-1
}

// OfferRepository is the in-memory ports.OfferRepository.
type OfferRepository struct{ s *Store }

// Get returns the offer or ports.ErrNotFound.
func (r *OfferRepository) Get(ctx context.Context, id offer.ID) (*offer.Offer, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.st.offers[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return offer.Restore(snap)
}

// ListExpired returns QUOTED offers with expiresAt <= now, oldest first.
func (r *OfferRepository) ListExpired(ctx context.Context, now time.Time, limit int) ([]offer.ID, error) {
	defer r.s.enter(ctx)()
	var rows []offer.Snapshot
	for _, o := range r.s.st.offers {
		if o.State == offer.StateQuoted && !o.ExpiresAt.After(now) {
			rows = append(rows, o)
		}
	}
	slices.SortFunc(rows, func(a, b offer.Snapshot) int {
		return cmp.Or(a.ExpiresAt.Compare(b.ExpiresAt), cmp.Compare(a.ID, b.ID))
	})
	var ids []offer.ID
	for _, o := range rows[:min(limit, len(rows))] {
		ids = append(ids, o.ID)
	}
	return ids, nil
}

// Save stores the offer under the version rule.
func (r *OfferRepository) Save(ctx context.Context, o *offer.Offer) error {
	defer r.s.enter(ctx)()
	snap := o.Snapshot()
	cur, ok := r.s.st.offers[snap.ID]
	if !versionOK(cur.Version, ok, snap.Version) {
		return ports.ErrConcurrentUpdate
	}
	r.s.st.offers[snap.ID] = snap
	return nil
}

// PromiseRepository is the in-memory ports.PromiseRepository.
type PromiseRepository struct{ s *Store }

func (r *PromiseRepository) restoreAll(rows []promise.Snapshot) ([]*promise.Promise, error) {
	out := make([]*promise.Promise, 0, len(rows))
	for _, row := range rows {
		p, err := promise.Restore(row)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *PromiseRepository) where(pred func(promise.Snapshot) bool) []promise.Snapshot {
	var rows []promise.Snapshot
	for _, p := range r.s.st.promises {
		if pred(p) {
			rows = append(rows, p)
		}
	}
	return rows
}

// Get returns the promise or ports.ErrNotFound.
func (r *PromiseRepository) Get(ctx context.Context, id promise.ID) (*promise.Promise, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.st.promises[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return promise.Restore(snap)
}

// GetByOffer returns the promise made from the offer or ports.ErrNotFound.
func (r *PromiseRepository) GetByOffer(ctx context.Context, offerID string) (*promise.Promise, error) {
	defer r.s.enter(ctx)()
	rows := r.where(func(p promise.Snapshot) bool { return p.OfferID == offerID })
	if len(rows) == 0 {
		return nil, ports.ErrNotFound
	}
	return promise.Restore(rows[0])
}

// FindByOrderGroup returns the promise of an order group or ports.ErrNotFound.
func (r *PromiseRepository) FindByOrderGroup(ctx context.Context, orderID string, groupNo int) (*promise.Promise, error) {
	defer r.s.enter(ctx)()
	rows := r.where(func(p promise.Snapshot) bool { return p.OrderID == orderID && p.GroupNo == groupNo })
	if len(rows) == 0 {
		return nil, ports.ErrNotFound
	}
	return promise.Restore(rows[0])
}

// ListByOrder returns the order's promises by groupNo.
func (r *PromiseRepository) ListByOrder(ctx context.Context, orderID string) ([]*promise.Promise, error) {
	defer r.s.enter(ctx)()
	rows := r.where(func(p promise.Snapshot) bool { return p.OrderID == orderID })
	slices.SortFunc(rows, func(a, b promise.Snapshot) int { return a.GroupNo - b.GroupNo })
	return r.restoreAll(rows)
}

func ids(rows []promise.Snapshot, limit int) []promise.ID {
	var out []promise.ID
	for _, p := range rows[:min(limit, len(rows))] {
		out = append(out, p.ID)
	}
	return out
}

// ListHoldsExpired returns PROVISIONAL promises with holdExpiresAt <= now.
func (r *PromiseRepository) ListHoldsExpired(ctx context.Context, now time.Time, limit int) ([]promise.ID, error) {
	defer r.s.enter(ctx)()
	rows := r.where(func(p promise.Snapshot) bool {
		return p.State == promise.StateProvisional && !p.HoldExpiresAt.After(now)
	})
	slices.SortFunc(rows, func(a, b promise.Snapshot) int {
		return cmp.Or(a.HoldExpiresAt.Compare(b.HoldExpiresAt), cmp.Compare(a.ID, b.ID))
	})
	return ids(rows, limit), nil
}

// ListAtRisk returns AT_RISK promises, oldest first.
func (r *PromiseRepository) ListAtRisk(ctx context.Context, limit int) ([]promise.ID, error) {
	defer r.s.enter(ctx)()
	rows := r.where(func(p promise.Snapshot) bool { return p.State == promise.StateAtRisk })
	slices.SortFunc(rows, func(a, b promise.Snapshot) int { return cmp.Or(a.MadeAt.Compare(b.MadeAt), cmp.Compare(a.ID, b.ID)) })
	return ids(rows, limit), nil
}

// ListActiveByPath returns CONFIRMED and AT_RISK promises whose witness uses
// the departure or the wave.
func (r *PromiseRepository) ListActiveByPath(ctx context.Context, f repository.PromiseRiskFilter) ([]promise.ID, error) {
	defer r.s.enter(ctx)()
	rows := r.where(func(p promise.Snapshot) bool {
		if !p.State.Active() {
			return false
		}
		return f.DepartureID != "" && (p.Witness.PickupDepartureID == f.DepartureID || slices.Contains(p.Witness.Legs, f.DepartureID)) ||
			f.WaveID != "" && p.Witness.DeliveryWaveID == f.WaveID
	})
	slices.SortFunc(rows, func(a, b promise.Snapshot) int { return cmp.Compare(a.ID, b.ID) })
	return ids(rows, len(rows)), nil
}

// HeldQuantities sums the lines of PROVISIONAL promises per (node, item).
func (r *PromiseRepository) HeldQuantities(ctx context.Context, nodeIDs []string) (map[repository.HoldKey]int, error) {
	defer r.s.enter(ctx)()
	out := map[repository.HoldKey]int{}
	for _, p := range r.s.st.promises {
		if p.State != promise.StateProvisional || !slices.Contains(nodeIDs, p.Witness.NodeID) {
			continue
		}
		for _, l := range p.Lines {
			out[repository.HoldKey{NodeID: p.Witness.NodeID, ItemID: l.CatalogItemID}] += l.Quantity
		}
	}
	return out, nil
}

// Save stores the promise under the version rule and the unique offer index.
func (r *PromiseRepository) Save(ctx context.Context, p *promise.Promise) error {
	defer r.s.enter(ctx)()
	snap := p.Snapshot()
	cur, ok := r.s.st.promises[snap.ID]
	if !versionOK(cur.Version, ok, snap.Version) {
		return ports.ErrConcurrentUpdate
	}
	for id, other := range r.s.st.promises {
		if other.OfferID == snap.OfferID && id != snap.ID {
			return ports.ErrConcurrentUpdate
		}
	}
	r.s.st.promises[snap.ID] = snap
	return nil
}

// BookRepository is the in-memory ports.BookRepository.
type BookRepository struct{ s *Store }

// Get returns the book or ports.ErrNotFound.
func (r *BookRepository) Get(ctx context.Context, id book.ID) (*book.Book, error) {
	defer r.s.enter(ctx)()
	snap, ok := r.s.st.books[id]
	if !ok {
		return nil, ports.ErrNotFound
	}
	return book.Restore(snap)
}

// List returns the matching books by cutoffAt then id.
func (r *BookRepository) List(ctx context.Context, f repository.BookFilter) ([]*book.Book, error) {
	defer r.s.enter(ctx)()
	var rows []book.Snapshot
	for _, b := range r.s.st.books {
		if len(f.NodeIDs) > 0 && !slices.Contains(f.NodeIDs, b.NodeID) {
			continue
		}
		if !f.CutoffFrom.IsZero() && b.CutoffAt.Before(f.CutoffFrom) || !f.CutoffUntil.IsZero() && !b.CutoffAt.Before(f.CutoffUntil) {
			continue
		}
		rows = append(rows, b)
	}
	slices.SortFunc(rows, func(a, b book.Snapshot) int { return cmp.Or(a.CutoffAt.Compare(b.CutoffAt), cmp.Compare(a.ID, b.ID)) })
	out := make([]*book.Book, 0, len(rows))
	for _, row := range rows {
		b, err := book.Restore(row)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}

// ListOpenDue returns OPEN books with cutoffAt <= now, oldest first.
func (r *BookRepository) ListOpenDue(ctx context.Context, now time.Time, limit int) ([]book.ID, error) {
	defer r.s.enter(ctx)()
	var rows []book.Snapshot
	for _, b := range r.s.st.books {
		if b.State == book.StateOpen && !b.CutoffAt.After(now) {
			rows = append(rows, b)
		}
	}
	slices.SortFunc(rows, func(a, b book.Snapshot) int { return cmp.Or(a.CutoffAt.Compare(b.CutoffAt), cmp.Compare(a.ID, b.ID)) })
	var out []book.ID
	for _, b := range rows[:min(limit, len(rows))] {
		out = append(out, b.ID)
	}
	return out, nil
}

// Save stores the book under the version rule.
func (r *BookRepository) Save(ctx context.Context, b *book.Book) error {
	defer r.s.enter(ctx)()
	snap := b.Snapshot()
	cur, ok := r.s.st.books[snap.ID]
	if !versionOK(cur.Version, ok, snap.Version) {
		return ports.ErrConcurrentUpdate
	}
	r.s.st.books[snap.ID] = snap
	return nil
}

// PolicyRepository is the in-memory ports.PolicyRepository.
type PolicyRepository struct{ s *Store }

// Current returns the highest version or ports.ErrNotFound.
func (r *PolicyRepository) Current(ctx context.Context) (*policy.Policy, error) {
	defer r.s.enter(ctx)()
	best := 0
	for v := range r.s.st.policies {
		best = max(best, v)
	}
	if best == 0 {
		return nil, ports.ErrNotFound
	}
	return policy.Restore(r.s.st.policies[best])
}

// Save stores a new version; an existing version is ports.ErrConcurrentUpdate.
func (r *PolicyRepository) Save(ctx context.Context, p *policy.Policy) error {
	defer r.s.enter(ctx)()
	snap := p.Snapshot()
	if _, exists := r.s.st.policies[snap.Version]; exists {
		return ports.ErrConcurrentUpdate
	}
	r.s.st.policies[snap.Version] = snap
	return nil
}
