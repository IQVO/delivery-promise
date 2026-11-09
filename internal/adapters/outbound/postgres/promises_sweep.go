package postgres

import (
	"context"

	"github.com/claudioed/delivery-promise/internal/domain/promise"
)

// ListAtRiskPage returns AT_RISK promises, oldest first, after the first offset.
func (r *PromiseRepository) ListAtRiskPage(ctx context.Context, offset, limit int) ([]promise.ID, error) {
	return listIDs[promise.ID](ctx, r.q(ctx), `SELECT id FROM promises WHERE state = 'AT_RISK' ORDER BY made_at, id OFFSET $1 LIMIT $2`, max(offset, 0), limit)
}

// ListConfirmed returns the CONFIRMED promises with an id above after, by id.
func (r *PromiseRepository) ListConfirmed(ctx context.Context, after promise.ID, limit int) ([]promise.ID, error) {
	return listIDs[promise.ID](ctx, r.q(ctx), `SELECT id FROM promises WHERE state = 'CONFIRMED' AND id > $1 ORDER BY id LIMIT $2`, string(after), limit)
}
