package ports

import (
	"context"
	"time"
)

// DepartureArrivals stores the DepartureArrival read model (ADR 0021): one row
// per scheduled departure whose load was seen arriving, fed by LoadArrived
// events that carry a departureId. Every call joins the unit of work carried
// by ctx when there is one.
type DepartureArrivals interface {
	// RecordDepartureArrived stores the arrival of the departure; the first
	// arrival seen is kept and a repeat changes nothing. It also deletes the
	// rows older than the longest transit window (policy.MaxTransitWindowDays)
	// before arrivedAt.
	RecordDepartureArrived(ctx context.Context, departureID string, arrivedAt time.Time) error
	// DepartureArrived reports whether an arrival row exists for the departure.
	DepartureArrived(ctx context.Context, departureID string) (bool, error)
}

// LateRiskMetrics counts the business outcomes of the late-risk read model and
// sweep (ADR 0021). The implementation lives in
// internal/adapters/outbound/telemetry; a nil Deps.LateRiskMetrics records
// nothing.
type LateRiskMetrics interface {
	// PredictedLateFlagged counts a promise the sweep flagged AT_RISK with the
	// reason PREDICTED_LATE.
	PredictedLateFlagged(ctx context.Context)
	// DepartureArrivalIgnored counts a LoadArrived that carried no departureId
	// and so could not feed the DepartureArrival model.
	DepartureArrivalIgnored(ctx context.Context)
}
