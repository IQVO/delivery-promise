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

func TestRequestFactMetricsCountWrittenAndDroppedByReason(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	metrics, err := telemetry.NewRequestFactMetrics()
	if err != nil {
		t.Fatalf("NewRequestFactMetrics: %v", err)
	}
	ctx := context.Background()
	metrics.FactWritten(ctx)
	metrics.FactDropped(ctx, "queue_full")
	metrics.FactDropped(ctx, "queue_full")
	metrics.FactDropped(ctx, "write_failed")

	var collected metricdata.ResourceMetrics
	if err := reader.Collect(ctx, &collected); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	got := map[string]int64{}
	for _, scope := range collected.ScopeMetrics {
		for _, m := range scope.Metrics {
			sum, ok := m.Data.(metricdata.Sum[int64])
			if !ok || m.Description == "" || m.Unit == "" {
				t.Fatalf("%s: %T, description %q, unit %q; want an int64 sum with a description and a unit", m.Name, m.Data, m.Description, m.Unit)
			}
			for _, dp := range sum.DataPoints {
				reason, _ := dp.Attributes.Value(attribute.Key("reason"))
				got[m.Name+"/"+reason.AsString()] = dp.Value
			}
		}
	}
	want := map[string]int64{
		"deliverypromise.request_facts.written/":             1,
		"deliverypromise.request_facts.dropped/queue_full":   2,
		"deliverypromise.request_facts.dropped/write_failed": 1,
	}
	if len(got) != len(want) {
		t.Fatalf("points = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %d, want %d (all: %v)", k, got[k], v, got)
		}
	}
}
