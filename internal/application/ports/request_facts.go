package ports

import "context"

// RequestFactMetrics counts what became of the best-effort request facts
// (ADR 0031). The implementation lives in internal/adapters/outbound/telemetry;
// a nil one records nothing.
type RequestFactMetrics interface {
	// FactWritten counts a fact the worker stored as an analytics outbox row.
	FactWritten(ctx context.Context)
	// FactDropped counts a fact that was lost, by reason (queue_full,
	// write_failed, internal_error, shutdown).
	FactDropped(ctx context.Context, reason string)
}
