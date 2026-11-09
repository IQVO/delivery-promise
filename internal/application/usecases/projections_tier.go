package usecases

import (
	"context"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// The reasons an unusable NodeTierEligibilityChanged is counted under.
const (
	IgnoredUnknownTier   = "unknown-tier"
	IgnoredMissingField  = "missing-field"
	tierEligibilityEvent = "NodeTierEligibilityChanged"
)

// NodeTierEligibilityChanged is node-performance NodeTierEligibilityChanged
// (tolerant local copy, ADR 0022): only the fields the read model needs.
// Eligible is a pointer so a missing field is not read as `false`.
type NodeTierEligibilityChanged struct {
	NodeID    string    `json:"nodeId"`
	Tier      string    `json:"tier"`
	Eligible  *bool     `json:"eligible"`
	ChangedAt time.Time `json:"changedAt"`
}

// ignoredReason says why the event cannot be applied, or "" when it can.
func (e NodeTierEligibilityChanged) ignoredReason() string {
	switch {
	case e.NodeID == "" || e.Tier == "" || e.Eligible == nil || e.ChangedAt.IsZero():
		return IgnoredMissingField
	case !readmodel.GovernedTier(e.Tier):
		return IgnoredUnknownTier
	}
	return ""
}

// OnNodeTierEligibilityChanged projects NodeTierEligibilityChanged into the
// NodeTierEligibility read model (ADR 0022). An event with an unknown tier or a
// missing field is logged, counted and dropped (never dead-lettered); one whose
// changedAt is older than the stored row changes nothing.
func (s *Service) OnNodeTierEligibilityChanged(ctx context.Context, m Meta, e NodeTierEligibilityChanged) error {
	if reason := e.ignoredReason(); reason != "" {
		s.Metrics.TierEligibilityIgnored(ctx, reason)
		s.Logger.WarnContext(ctx, "ignoring a "+tierEligibilityEvent+" that cannot be applied",
			"reason", reason, "event_id", m.ID, "nodeId", e.NodeID, "tier", e.Tier)
		return nil
	}
	return s.project(ctx, m, func(ctx context.Context) error {
		applied, err := s.TierEligibility.Apply(ctx, readmodel.NodeTierEligibility{
			NodeID: e.NodeID, Tier: e.Tier, Eligible: *e.Eligible, ChangedAt: e.ChangedAt.UTC(),
		})
		if err != nil {
			return err
		}
		if !applied {
			s.Logger.DebugContext(ctx, "stale "+tierEligibilityEvent+" dropped", "nodeId", e.NodeID, "tier", e.Tier, "changedAt", e.ChangedAt)
		}
		return nil
	})
}

// tierKey keys one (node, tier) pair of the eligibility lookup.
type tierKey struct{ node, tier string }

// tierIneligibleOf loads the ineligible (node, tier) pairs once and adapts them
// to the search's TierIneligible closure (ADR 0022). It returns nil, so
// nothing is filtered, when the service has no read model, the request asks for
// no fast tier or no pair is ineligible. Each excluded (node, tier) is counted
// once per request.
func (s *Service) tierIneligibleOf(ctx context.Context, tiers []shared.Tier) (func(nodeID string, tier shared.Tier) bool, error) {
	if s.TierEligibility == nil || !asksFastTier(tiers) {
		return nil, nil
	}
	rows, err := s.TierEligibility.Ineligible(ctx)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	ineligible := make(map[tierKey]bool, len(rows))
	for _, r := range rows {
		ineligible[tierKey{r.NodeID, r.Tier}] = true
	}
	counted := map[tierKey]bool{}
	return func(nodeID string, tier shared.Tier) bool {
		k := tierKey{nodeID, string(tier)}
		if !ineligible[k] {
			return false
		}
		if !counted[k] {
			counted[k] = true
			s.Metrics.TierCandidatesExcluded(ctx, string(tier), 1)
		}
		return true
	}, nil
}

// asksFastTier is true when the request names a fast tier; no tier named means
// every tier.
func asksFastTier(tiers []shared.Tier) bool {
	if len(tiers) == 0 {
		return true
	}
	for _, t := range tiers {
		if search.FastTier(t) {
			return true
		}
	}
	return false
}
