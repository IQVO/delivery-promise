package telemetry_test

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/telemetry"
)

// calibrationPoints is what the two calibration instruments hold: per tier the
// count and sum of the buffer histogram, and the thin-segment counter by
// "tier/basis/node_grade".
type calibrationPoints struct {
	bufferCount map[string]uint64
	bufferSum   map[string]int64
	thin        map[string]int64
}

func collectCalibration(t *testing.T, reader sdkmetric.Reader) calibrationPoints {
	t.Helper()
	var collected metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &collected); err != nil {
		t.Fatal(err)
	}
	got := calibrationPoints{bufferCount: map[string]uint64{}, bufferSum: map[string]int64{}, thin: map[string]int64{}}
	for _, scope := range collected.ScopeMetrics {
		for _, md := range scope.Metrics {
			switch md.Name {
			case "promising.calibration.buffer":
				addBufferPoints(t, md, got)
			case "promising.calibration.segments_below_minimum":
				addThinPoints(t, md, got)
			}
		}
	}
	return got
}

func addBufferPoints(t *testing.T, md metricdata.Metrics, got calibrationPoints) {
	t.Helper()
	h, ok := md.Data.(metricdata.Histogram[int64])
	if !ok || md.Unit != "min" || md.Description == "" {
		t.Fatalf("%s is a %T with unit %q, want an int64 histogram in minutes with a description", md.Name, md.Data, md.Unit)
	}
	for _, p := range h.DataPoints {
		tier, _ := p.Attributes.Value("tier")
		got.bufferCount[tier.AsString()], got.bufferSum[tier.AsString()] = p.Count, p.Sum
	}
}

func addThinPoints(t *testing.T, md metricdata.Metrics, got calibrationPoints) {
	t.Helper()
	s, ok := md.Data.(metricdata.Sum[int64])
	if !ok || md.Description == "" {
		t.Fatalf("%s is a %T, want an int64 sum with a description", md.Name, md.Data)
	}
	for _, p := range s.DataPoints {
		tier, _ := p.Attributes.Value("tier")
		basis, _ := p.Attributes.Value("basis")
		grade, _ := p.Attributes.Value("node_grade")
		got.thin[tier.AsString()+"/"+basis.AsString()+"/"+grade.AsString()] = p.Value
	}
}

func TestCalibrationMetricsRecordTheBufferPerTierAndThinSegments(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)))
	t.Cleanup(func() { otel.SetMeterProvider(previous) })

	m, err := telemetry.NewPromiseMetrics()
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	m.CalibrationBufferApplied(ctx, "NEXT_DAY", 0)
	m.CalibrationBufferApplied(ctx, "NEXT_DAY", 400)
	m.CalibrationBufferApplied(ctx, "STANDARD", 60)
	m.CalibrationSegmentBelowMinimum(ctx, "NEXT_DAY", "SCHEDULED", "PROVISIONAL")
	m.CalibrationSegmentBelowMinimum(ctx, "NEXT_DAY", "SCHEDULED", "PROVISIONAL")

	got := collectCalibration(t, reader)
	if got.bufferCount["NEXT_DAY"] != 2 || got.bufferSum["NEXT_DAY"] != 400 || got.bufferCount["STANDARD"] != 1 || got.bufferSum["STANDARD"] != 60 {
		t.Errorf("buffer histogram: counts %v sums %v", got.bufferCount, got.bufferSum)
	}
	if got.thin["NEXT_DAY/SCHEDULED/PROVISIONAL"] != 2 || len(got.thin) != 1 {
		t.Errorf("segments below minimum = %v, want 2 for NEXT_DAY/SCHEDULED/PROVISIONAL", got.thin)
	}
}
