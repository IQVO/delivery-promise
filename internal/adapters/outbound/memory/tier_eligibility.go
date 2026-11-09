package memory

import (
	"context"
	"slices"
	"strings"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
)

// tierKey keys one (node, tier) row of the NodeTierEligibility double.
type tierKey struct{ node, tier string }

// NodeTierEligibility is the in-memory ports.NodeTierEligibility (ADR 0022). It
// keeps the persistence contract of the Postgres adapter: a row is replaced
// only by a changedAt not before the stored one.
type NodeTierEligibility struct{ s *Store }

var _ ports.NodeTierEligibility = (*NodeTierEligibility)(nil)

// Apply stores the fact unless the row holds a later changedAt.
func (n *NodeTierEligibility) Apply(ctx context.Context, e readmodel.NodeTierEligibility) (bool, error) {
	defer n.s.enter(ctx)()
	k := tierKey{e.NodeID, e.Tier}
	if cur, ok := n.s.st.tierEligibility[k]; ok && cur.ChangedAt.After(e.ChangedAt) {
		return false, nil
	}
	e.ChangedAt = e.ChangedAt.UTC()
	n.s.st.tierEligibility[k] = e
	return true, nil
}

// Get returns the row of (nodeID, tier) or ports.ErrNotFound.
func (n *NodeTierEligibility) Get(ctx context.Context, nodeID, tier string) (readmodel.NodeTierEligibility, error) {
	defer n.s.enter(ctx)()
	e, ok := n.s.st.tierEligibility[tierKey{nodeID, tier}]
	if !ok {
		return readmodel.NodeTierEligibility{}, ports.ErrNotFound
	}
	return e, nil
}

// Ineligible returns the ineligible rows ordered by node then tier.
func (n *NodeTierEligibility) Ineligible(ctx context.Context) ([]readmodel.NodeTierEligibility, error) {
	defer n.s.enter(ctx)()
	var out []readmodel.NodeTierEligibility
	for _, e := range n.s.st.tierEligibility {
		if !e.Eligible {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b readmodel.NodeTierEligibility) int {
		if c := strings.Compare(a.NodeID, b.NodeID); c != 0 {
			return c
		}
		return strings.Compare(a.Tier, b.Tier)
	})
	return out, nil
}
