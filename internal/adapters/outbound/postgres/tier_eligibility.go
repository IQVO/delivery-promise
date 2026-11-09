package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
)

// NodeTierEligibility is the Postgres ports.NodeTierEligibility over
// rm_node_tier_eligibility (ADR 0022).
type NodeTierEligibility struct{ db }

var _ ports.NodeTierEligibility = (*NodeTierEligibility)(nil)

// NewNodeTierEligibility constructs NodeTierEligibility over pool.
func NewNodeTierEligibility(pool *pgxpool.Pool) *NodeTierEligibility {
	return &NodeTierEligibility{db{pool}}
}

// Apply upserts the row unless it already holds a later changed_at; an equal
// one is applied.
func (n *NodeTierEligibility) Apply(ctx context.Context, e readmodel.NodeTierEligibility) (bool, error) {
	tag, err := n.q(ctx).Exec(ctx,
		`INSERT INTO rm_node_tier_eligibility (node_id, tier, eligible, changed_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (node_id, tier) DO UPDATE SET eligible = EXCLUDED.eligible, changed_at = EXCLUDED.changed_at
		WHERE rm_node_tier_eligibility.changed_at <= EXCLUDED.changed_at`,
		e.NodeID, e.Tier, e.Eligible, e.ChangedAt.UTC())
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// Get returns the row of (nodeID, tier) or ports.ErrNotFound.
func (n *NodeTierEligibility) Get(ctx context.Context, nodeID, tier string) (readmodel.NodeTierEligibility, error) {
	var e readmodel.NodeTierEligibility
	err := n.q(ctx).QueryRow(ctx,
		`SELECT node_id, tier, eligible, changed_at FROM rm_node_tier_eligibility WHERE node_id = $1 AND tier = $2`, nodeID, tier).
		Scan(&e.NodeID, &e.Tier, &e.Eligible, &e.ChangedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return readmodel.NodeTierEligibility{}, ports.ErrNotFound
	}
	e.ChangedAt = e.ChangedAt.UTC()
	return e, err
}

// Ineligible returns the ineligible rows ordered by node then tier.
func (n *NodeTierEligibility) Ineligible(ctx context.Context) ([]readmodel.NodeTierEligibility, error) {
	rows, err := n.q(ctx).Query(ctx,
		`SELECT node_id, tier, eligible, changed_at FROM rm_node_tier_eligibility WHERE NOT eligible ORDER BY node_id, tier`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []readmodel.NodeTierEligibility
	for rows.Next() {
		var e readmodel.NodeTierEligibility
		if err := rows.Scan(&e.NodeID, &e.Tier, &e.Eligible, &e.ChangedAt); err != nil {
			return nil, err
		}
		e.ChangedAt = e.ChangedAt.UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
