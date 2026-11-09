package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/delivery-promise/internal/application/ports"
)

// The late-risk counters (ADR 0021). The Prometheus exporter appends _total,
// so they are queried as deliverypromise_predicted_late_flagged_total and
// deliverypromise_departure_arrival_ignored_total.
const (
	predictedLateFlaggedName    = "deliverypromise.predicted_late.flagged"
	departureArrivalIgnoredName = "deliverypromise.departure_arrival.ignored"
	predictedLateFlaggedHelp    = "Promises the late-risk sweep flagged AT_RISK with the reason PREDICTED_LATE: a departure of the witness path " +
		"was past its expected arrival plus the grace with no arrival seen. A rise means linehaul loads are running behind the timetable; " +
		"a rise with no matching rise in PromiseRevised means the slack of the paths is absorbing it."
	departureArrivalIgnoredHelp = "LoadArrived events that carried no departureId and so could not feed the DepartureArrival model. " +
		"A sustained non-zero rate means the producer still publishes the old shape and no promise can be flagged for those loads."
)

// LateRiskMetrics implements ports.LateRiskMetrics against the global
// MeterProvider (a no-op until Setup installs a real one).
type LateRiskMetrics struct {
	flagged metric.Int64Counter
	ignored metric.Int64Counter
}

var _ ports.LateRiskMetrics = (*LateRiskMetrics)(nil)

// NewLateRiskMetrics registers the two counters. Call it after Setup so the
// instruments bind to the real MeterProvider.
func NewLateRiskMetrics() (*LateRiskMetrics, error) {
	meter := otel.Meter(meterName)
	flagged, err := meter.Int64Counter(predictedLateFlaggedName,
		metric.WithDescription(predictedLateFlaggedHelp), metric.WithUnit("{promise}"))
	if err != nil {
		return nil, err
	}
	ignored, err := meter.Int64Counter(departureArrivalIgnoredName,
		metric.WithDescription(departureArrivalIgnoredHelp), metric.WithUnit("{event}"))
	if err != nil {
		return nil, err
	}
	return &LateRiskMetrics{flagged: flagged, ignored: ignored}, nil
}

// PredictedLateFlagged counts a promise flagged PREDICTED_LATE.
func (m *LateRiskMetrics) PredictedLateFlagged(ctx context.Context) { m.flagged.Add(ctx, 1) }

// DepartureArrivalIgnored counts a LoadArrived without a departureId.
func (m *LateRiskMetrics) DepartureArrivalIgnored(ctx context.Context) { m.ignored.Add(ctx, 1) }
