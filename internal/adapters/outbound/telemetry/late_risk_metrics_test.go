package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/telemetry"
)

func TestLateRiskMetricsCountEachOutcome(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	metrics, err := telemetry.NewLateRiskMetrics()
	if err != nil {
		t.Fatalf("NewLateRiskMetrics: %v", err)
	}
	ctx := context.Background()
	metrics.PredictedLateFlagged(ctx)
	metrics.PredictedLateFlagged(ctx)
	metrics.DepartureArrivalIgnored(ctx)

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := map[string]int64{}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || len(sum.DataPoints) != 1 || m.Description == "" || m.Unit == "" {
				t.Fatalf("%s: %T with %d points, description %q, unit %q; want one int64 sum point, a description and a unit", m.Name, m.Data, len(sum.DataPoints), m.Description, m.Unit)
			}
			got[m.Name] = sum.DataPoints[0].Value
		}
	}
	want := map[string]int64{"deliverypromise.predicted_late.flagged": 2, "deliverypromise.departure_arrival.ignored": 1}
	for name, v := range want {
		if got[name] != v {
			t.Errorf("%s = %d, want %d (all: %v)", name, got[name], v, got)
		}
	}
}
