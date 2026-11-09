package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"
)

// The fast-tier eligibility counters (ADR 0022). Prometheus names:
// deliverypromise_tier_candidates_excluded_total and
// deliverypromise_tier_eligibility_events_ignored_total.
const (
	tierCandidatesExcludedName = "deliverypromise.tier.candidates.excluded"
	tierEventsIgnoredName      = "deliverypromise.tier.eligibility.events.ignored"
)

// tierCounters are the instruments of the fast-tier eligibility gate.
type tierCounters struct {
	excluded metric.Int64Counter
	ignored  metric.Int64Counter
}

func newTierCounters() (tierCounters, error) {
	meter := otel.Meter(meterName)
	excluded, err := meter.Int64Counter(tierCandidatesExcludedName,
		metric.WithDescription("Candidate fulfillment nodes removed from a SAME_DAY or NEXT_DAY search because node-performance revoked their "+
			"fast-tier eligibility (ADR 0022), once per node and tier per request. A rising count with a falling share of fast offers "+
			"means the gate is steering sourcing away from failing nodes; zero means every node is eligible."),
		metric.WithUnit("{candidate}"))
	if err != nil {
		return tierCounters{}, err
	}
	ignored, err := meter.Int64Counter(tierEventsIgnoredName,
		metric.WithDescription("NodeTierEligibilityChanged events the consumer could not apply and dropped, by reason (unknown-tier or missing-field). "+
			"Anything above zero means node-performance published a shape this service does not read."),
		metric.WithUnit("{event}"))
	if err != nil {
		return tierCounters{}, err
	}
	return tierCounters{excluded: excluded, ignored: ignored}, nil
}

// TierCandidatesExcluded counts n candidate nodes removed from a fast tier.
func (m *PromiseMetrics) TierCandidatesExcluded(ctx context.Context, tier string, n int) {
	m.tiers.excluded.Add(ctx, int64(n), metric.WithAttributes(tierKey.String(tier)))
}

// TierEligibilityIgnored counts a NodeTierEligibilityChanged that was dropped.
func (m *PromiseMetrics) TierEligibilityIgnored(ctx context.Context, reason string) {
	m.tiers.ignored.Add(ctx, 1, metric.WithAttributes(reasonKey.String(reason)))
}
