package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/telemetry"
)

// counterPoints reads the data points of the named int64 counter as
// "attribute value" -> value, checking its unit and description.
func counterPoints(t *testing.T, reader sdkmetric.Reader, name, unit, attr string) map[string]int64 {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := map[string]int64{}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			if m.Name != name {
				continue
			}
			if m.Unit != unit || m.Description == "" {
				t.Errorf("%s: unit = %q, description empty = %v", name, m.Unit, m.Description == "")
			}
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("%s is a %T, want an int64 Sum", name, m.Data)
			}
			for _, point := range sum.DataPoints {
				v, _ := point.Attributes.Value(attribute.Key(attr))
				got[v.AsString()] = point.Value
			}
		}
	}
	return got
}

func TestTierMetricsCountExcludedCandidatesByTierAndIgnoredEventsByReason(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	metrics, err := telemetry.NewPromiseMetrics()
	if err != nil {
		t.Fatalf("NewPromiseMetrics: %v", err)
	}
	ctx := context.Background()
	metrics.TierCandidatesExcluded(ctx, "NEXT_DAY", 1)
	metrics.TierCandidatesExcluded(ctx, "NEXT_DAY", 2)
	metrics.TierCandidatesExcluded(ctx, "SAME_DAY", 1)
	metrics.TierEligibilityIgnored(ctx, "unknown-tier")
	metrics.TierEligibilityIgnored(ctx, "unknown-tier")
	metrics.TierEligibilityIgnored(ctx, "missing-field")

	excluded := counterPoints(t, reader, "deliverypromise.tier.candidates.excluded", "{candidate}", "tier")
	if len(excluded) != 2 || excluded["NEXT_DAY"] != 3 || excluded["SAME_DAY"] != 1 {
		t.Errorf("excluded = %v, want NEXT_DAY 3 and SAME_DAY 1", excluded)
	}
	ignored := counterPoints(t, reader, "deliverypromise.tier.eligibility.events.ignored", "{event}", "reason")
	if len(ignored) != 2 || ignored["unknown-tier"] != 2 || ignored["missing-field"] != 1 {
		t.Errorf("ignored = %v, want unknown-tier 2 and missing-field 1", ignored)
	}
}
