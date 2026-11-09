package readmodel

import "time"

// NodeTierEligibility is a row of the NodeTierEligibility read model (ADR
// 0022): whether node-performance currently lets a node win a fast tier. A
// (node, tier) with no row is ELIGIBLE.
type NodeTierEligibility struct {
	NodeID string
	// Tier is SAME_DAY or NEXT_DAY, the tiers node-performance governs.
	Tier     string
	Eligible bool
	// ChangedAt is the changedAt of the event that set the row: an event with an
	// earlier changedAt never replaces it.
	ChangedAt time.Time
}

// The fast tiers whose eligibility is projected, as they appear on the wire.
const (
	TierSameDay = "SAME_DAY"
	TierNextDay = "NEXT_DAY"
)

// GovernedTier reports whether eligibility is tracked for the tier.
func GovernedTier(tier string) bool { return tier == TierSameDay || tier == TierNextDay }
