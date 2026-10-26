package telemetry

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/claudioed/delivery-promise/internal/application/ports"
)

// meterName scopes this service's own instruments, keeping them distinct from
// the ones otelchi and the runtime collector register.
const meterName = "github.com/claudioed/delivery-promise"

// promisesMadeName is the Tier 2 business metric of the context (the fleet's
// standard metrics convention): every MakePromise attempt, by outcome.
const promisesMadeName = "promising.promises.made"

const (
	outcomeKey = attribute.Key("outcome")
	reasonKey  = attribute.Key("reason")

	outcomeMade    = "made"
	outcomeRefused = "refused"
)

// PromiseMetrics implements ports.PromiseMetrics against the global
// MeterProvider. Until Setup installs a real provider the global one is a
// no-op, so recording is cheap and safe in tests and local runs.
type PromiseMetrics struct {
	counter metric.Int64Counter
}

var _ ports.PromiseMetrics = (*PromiseMetrics)(nil)

// NewPromiseMetrics registers the counter. It only fails if the instrument
// name is invalid, which is a programming error, not a runtime condition. Call
// it after Setup so the instrument binds to the real MeterProvider.
func NewPromiseMetrics() (*PromiseMetrics, error) {
	counter, err := otel.Meter(meterName).Int64Counter(
		promisesMadeName,
		metric.WithDescription("Attempts to make a delivery promise by outcome (made or refused; reason says why a refusal happened, "+
			"as a problem slug such as offer-not-quotable or promise-capacity-exhausted). "+
			"A falling made share with a rising offer-not-quotable or promise-capacity-exhausted means shoppers are offered dates "+
			"the network can no longer keep (expired offers, full nodes); a rising internal-error means the service itself is failing."),
		metric.WithUnit("{promise}"),
	)
	if err != nil {
		return nil, err
	}
	return &PromiseMetrics{counter: counter}, nil
}

// PromiseMade counts a promise that was made.
func (m *PromiseMetrics) PromiseMade(ctx context.Context) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeMade)))
}

// PromiseRefused counts a MakePromise that made no promise, with the reason why.
func (m *PromiseMetrics) PromiseRefused(ctx context.Context, reason string) {
	m.counter.Add(ctx, 1, metric.WithAttributes(outcomeKey.String(outcomeRefused), reasonKey.String(reason)))
}
