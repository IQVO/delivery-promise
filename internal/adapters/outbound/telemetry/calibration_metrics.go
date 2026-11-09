package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const (
	// calibrationBufferName is the histogram of the buffer a quote folded into
	// its offer, by tier (ADR 0020).
	calibrationBufferName = "promising.calibration.buffer"
	// calibrationBelowMinimumName counts the segments a search loaded whose
	// observations are fewer than the policy's minimum (ADR 0020).
	calibrationBelowMinimumName = "promising.calibration.segments_below_minimum"
)

const (
	tierKey      = attribute.Key("tier")
	basisKey     = attribute.Key("basis")
	nodeGradeKey = attribute.Key("node_grade")
)

// calibrationMetrics holds the two instruments of the calibration buffer.
type calibrationMetrics struct {
	buffer       metric.Int64Histogram
	belowMinimum metric.Int64Counter
}

func newCalibrationMetrics() (calibrationMetrics, error) {
	meter := otel.Meter(meterName)
	buffer, err := meter.Int64Histogram(
		calibrationBufferName,
		metric.WithDescription("Minutes of calibration buffer folded into a quoted delivery offer, by tier. A tier whose distribution moves "+
			"right is quoting later windows because its judged promises ran late; 0 means the segment's own history met the on-time target."),
		metric.WithUnit("min"),
		metric.WithExplicitBucketBoundaries(0, 15, 30, 60, 120, 240, 480, 720, 1440, 2880),
	)
	if err != nil {
		return calibrationMetrics{}, err
	}
	below, err := meter.Int64Counter(
		calibrationBelowMinimumName,
		metric.WithDescription("Calibration segments (tier, basis, node grade) a search loaded that have judged promises but fewer than "+
			"minCalibrationObservations: they quote as before calibration and carry no confidence."),
		metric.WithUnit("{segment}"),
	)
	if err != nil {
		return calibrationMetrics{}, err
	}
	return calibrationMetrics{buffer: buffer, belowMinimum: below}, nil
}

// CalibrationBufferApplied records the buffer of one quoted offer.
func (m calibrationMetrics) CalibrationBufferApplied(ctx context.Context, tier string, minutes int) {
	m.buffer.Record(ctx, int64(minutes), metric.WithAttributes(tierKey.String(tier)))
}

// CalibrationSegmentBelowMinimum counts one thin segment a search loaded.
func (m calibrationMetrics) CalibrationSegmentBelowMinimum(ctx context.Context, tier, basis, nodeGrade string) {
	m.belowMinimum.Add(ctx, 1, metric.WithAttributes(tierKey.String(tier), basisKey.String(basis), nodeGradeKey.String(nodeGrade)))
}
