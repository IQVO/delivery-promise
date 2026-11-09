package postgres

import (
	"context"

	"github.com/claudioed/delivery-promise/internal/application/repository"
)

// The hold queries: how many units open promises still net out of an offer
// (ADR 0015). They live apart from the promise CRUD to keep each file readable.

// HeldQuantities sums the lines of PROVISIONAL promises per (node, item,
// ownerRef); a line persisted before the owner was recorded is operator-owned
// (ADR 0017).
func (r *PromiseRepository) HeldQuantities(ctx context.Context, nodeIDs []string) (map[repository.HoldKey]int, error) {
	out := map[repository.HoldKey]int{}
	if len(nodeIDs) == 0 {
		return out, nil
	}
	rows, err := r.q(ctx).Query(ctx, `
		SELECT p.node_id, l->>'CatalogItemID', COALESCE(l->>'OwnerRef', ''), SUM((l->>'Quantity')::int)
		FROM promises p, jsonb_array_elements(p.snapshot->'Lines') AS l
		WHERE p.state = 'PROVISIONAL' AND p.node_id = ANY ($1)
		GROUP BY p.node_id, l->>'CatalogItemID', COALESCE(l->>'OwnerRef', '')`, nodeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var k repository.HoldKey
		var qty int
		if err := rows.Scan(&k.NodeID, &k.ItemID, &k.OwnerRef, &qty); err != nil {
			return nil, err
		}
		out[k] = qty
	}
	return out, rows.Err()
}

// ConfirmedHolds lists the lines of the CONFIRMED and AT_RISK promises per
// (node, item, ownerRef) with the instant of their confirmation; a promise confirmed
// before that instant was recorded falls back to its hold deadline, the latest
// it can have been confirmed.
func (r *PromiseRepository) ConfirmedHolds(ctx context.Context, nodeIDs []string) ([]repository.ConfirmedHold, error) {
	if len(nodeIDs) == 0 {
		return nil, nil
	}
	rows, err := r.q(ctx).Query(ctx, `
		SELECT p.node_id, l->>'CatalogItemID', COALESCE(l->>'OwnerRef', ''), (l->>'Quantity')::int,
		       COALESCE(NULLIF(p.snapshot->>'ConfirmedAt', '0001-01-01T00:00:00Z')::timestamptz, p.hold_expires_at)
		FROM promises p, jsonb_array_elements(p.snapshot->'Lines') AS l
		WHERE p.state IN ('CONFIRMED', 'AT_RISK') AND p.node_id = ANY ($1)
		ORDER BY p.id`, nodeIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []repository.ConfirmedHold
	for rows.Next() {
		var h repository.ConfirmedHold
		if err := rows.Scan(&h.Key.NodeID, &h.Key.ItemID, &h.Key.OwnerRef, &h.Qty, &h.ConfirmedAt); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
