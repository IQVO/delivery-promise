package memory

import (
	"cmp"
	"context"
	"slices"

	"github.com/claudioed/delivery-promise/internal/domain/promise"
)

// ListAtRiskPage returns AT_RISK promises, oldest first, after the first offset.
func (r *PromiseRepository) ListAtRiskPage(ctx context.Context, offset, limit int) ([]promise.ID, error) {
	defer r.s.enter(ctx)()
	rows := r.where(func(p promise.Snapshot) bool { return p.State == promise.StateAtRisk })
	slices.SortFunc(rows, func(a, b promise.Snapshot) int { return cmp.Or(a.MadeAt.Compare(b.MadeAt), cmp.Compare(a.ID, b.ID)) })
	return ids(rows[min(max(offset, 0), len(rows)):], limit), nil
}

// ListConfirmed returns the CONFIRMED promises with an id above after, by id.
func (r *PromiseRepository) ListConfirmed(ctx context.Context, after promise.ID, limit int) ([]promise.ID, error) {
	defer r.s.enter(ctx)()
	rows := r.where(func(p promise.Snapshot) bool { return p.State == promise.StateConfirmed && p.ID > after })
	slices.SortFunc(rows, func(a, b promise.Snapshot) int { return cmp.Compare(a.ID, b.ID) })
	return ids(rows, limit), nil
}
