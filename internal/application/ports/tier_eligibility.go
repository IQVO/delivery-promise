package ports

import (
	"context"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
)

// NodeTierEligibility stores the NodeTierEligibility read model (ADR 0022),
// fed by node-performance's NodeTierEligibilityChanged. A (node, tier) with no
// row is ELIGIBLE. Every call joins the unit of work carried by ctx when there
// is one.
type NodeTierEligibility interface {
	// Apply stores the fact unless the row already holds a later changedAt (an
	// equal one is applied) and reports whether it did.
	Apply(ctx context.Context, e readmodel.NodeTierEligibility) (applied bool, err error)
	// Get returns the row of (nodeID, tier), or ErrNotFound (eligible).
	Get(ctx context.Context, nodeID, tier string) (readmodel.NodeTierEligibility, error)
	// Ineligible returns the rows currently ineligible, ordered by node then tier.
	Ineligible(ctx context.Context) ([]readmodel.NodeTierEligibility, error)
}
