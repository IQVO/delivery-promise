package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/delivery-promise/internal/application/ports"
)

// The request-fact counters (ADR 0031). The Prometheus exporter appends _total,
// so they are queried as deliverypromise_request_facts_written_total and
// deliverypromise_request_facts_dropped_total{reason}.
const (
	requestFactsWrittenName = "deliverypromise.request_facts.written"
	requestFactsDroppedName = "deliverypromise.request_facts.dropped"
	requestFactsWrittenHelp = "Request-level coverage facts (DeliveryRequestAnswered) the background worker stored as analytics outbox rows."
	requestFactsDroppedHelp = "Request-level coverage facts that were LOST by design (best effort, ADR 0031), by reason: queue_full (the bounded " +
		"queue had no room, the newest fact is dropped), write_failed (the worker's own transaction failed or timed out, no retry), " +
		"internal_error (the hand-over itself failed), shutdown (still queued when the final drain ended). No response was affected. " +
		"A request-based coverage share tolerates a bounded loss; a sustained non-zero rate means the share rests on fewer requests than were answered."
)

// RequestFactMetrics implements ports.RequestFactMetrics against the global
// MeterProvider (a no-op until Setup installs a real one).
type RequestFactMetrics struct {
	written metric.Int64Counter
	dropped metric.Int64Counter
}

var _ ports.RequestFactMetrics = (*RequestFactMetrics)(nil)

// NewRequestFactMetrics registers the two counters. Call it after Setup so the
// instruments bind to the real MeterProvider.
func NewRequestFactMetrics() (*RequestFactMetrics, error) {
	meter := otel.Meter(meterName)
	written, err := meter.Int64Counter(requestFactsWrittenName, metric.WithDescription(requestFactsWrittenHelp), metric.WithUnit("{fact}"))
	if err != nil {
		return nil, err
	}
	dropped, err := meter.Int64Counter(requestFactsDroppedName, metric.WithDescription(requestFactsDroppedHelp), metric.WithUnit("{fact}"))
	if err != nil {
		return nil, err
	}
	return &RequestFactMetrics{written: written, dropped: dropped}, nil
}

// FactWritten counts a fact stored as an outbox row.
func (m *RequestFactMetrics) FactWritten(ctx context.Context) { m.written.Add(ctx, 1) }

// FactDropped counts a lost fact, by reason.
func (m *RequestFactMetrics) FactDropped(ctx context.Context, reason string) {
	m.dropped.Add(ctx, 1, metric.WithAttributes(reasonKey.String(reason)))
}
