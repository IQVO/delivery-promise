// Package report is the self-contained read-model region of the analytics read
// side (ADR 0018): the facts the projector folds in, the shapes of the promise
// reports, the KPI rules (the PromiseBroken cause vocabulary, the percentile
// ranks, the window width and the local-day rule, the rates and shares), the
// date-range rules, the freshness lag, and the writer/reader ports the
// analytical store implements. It imports no other internal package (arch
// test): the vocabulary below is hand-mirrored from apis/asyncapi.yaml, never
// imported from the domain, so the OLTP model can never leak into the
// projection.
package report

import (
	"context"
	"errors"
	"time"
)

// Kind is which of the six projected events a fact came from. The names are
// the CloudEvents event names (the last segment of `type`).
type Kind string

// The six projected events: two from retailnetwork.delivery-promise.analytics
// (DeliveryOfferQuoted, PromiseMade), four from retailnetwork.delivery-promise.events.
const (
	KindDeliveryOfferQuoted Kind = "DeliveryOfferQuoted"
	KindPromiseMade         Kind = "PromiseMade"
	KindPromiseRevised      Kind = "PromiseRevised"
	KindPromiseUnkeepable   Kind = "PromiseUnkeepable"
	KindPromiseKept         Kind = "PromiseKept"
	KindPromiseBroken       Kind = "PromiseBroken"
)

// Verdicts are the judgements promise_judgements records.
const (
	VerdictKept   = "KEPT"
	VerdictBroken = "BROKEN"
)

// Event is one decoded analytics fact ready to project. It is the union of the
// six payloads; only the fields of its Kind are set.
type Event struct {
	Kind    Kind
	EventID string // CloudEvents id: the idempotency key
	// At is the CloudEvents time. Every projected event is raised by its use
	// case at the moment of the fact (the quote, the promise, the judgement),
	// so it is both the business instant the reports bucket days on and the
	// basis of the freshness lag.
	At time.Time

	// PromiseID is the subject of every promise event; OfferID is the subject
	// of DeliveryOfferQuoted and a payload field of PromiseMade.
	PromiseID string
	OfferID   string

	// DeliveryOfferQuoted and PromiseMade.
	Tier string

	// PromiseMade: the terms as made. TimeZone is optional (ADR 0011).
	Basis             string
	DeliverByEarliest time.Time
	DeliverByLatest   time.Time
	TimeZone          string

	// PromiseRevised.
	RevisionNo int

	// PromiseBroken.
	Cause         string
	LateByMinutes int
}

// Projection is the WRITER port: Apply records the event id and projects the
// fact in ONE transaction. applied is false when the id was already recorded
// (a replay): nothing changed. An error wrapping ErrRejected is deterministic
// (the store can never accept this event: retry is pointless); any other error
// is transient and the same event may be retried.
type Projection interface {
	Apply(ctx context.Context, e Event) (applied bool, err error)
}

// ErrRejected marks an event the analytical store deterministically refuses
// (a data-exception or integrity-violation class error).
var ErrRejected = errors.New("report: event rejected by the analytical store")

// Reader is the READER port. Every method answers for the half-open instant
// range [r.From, r.To) of the counted event's instant, bucketed by UTC day.
// Results are ordered by day (then tier, then basis) and never nil.
type Reader interface {
	PromiseReliability(ctx context.Context, r Range) ([]ReliabilityDay, error)
	PromisePrecision(ctx context.Context, r Range) ([]PrecisionDay, error)
	QuoteConversion(ctx context.Context, r Range) ([]ConversionDay, error)
	// LastEventAt is the CloudEvents time of the newest event the projection
	// applied (nil while it applied none): the basis of the freshness lag.
	LastEventAt(ctx context.Context) (*time.Time, error)
}

// ReliabilityDay is what the store counts for one UTC day of judgement, one
// tier and one basis (both as made; UNKNOWN while the PromiseMade is not
// projected): the kept and broken promises, the broken ones per cause, and the
// lateness percentiles at P50 and P90 over the broken LATE ones (nil when
// there is none).
type ReliabilityDay struct {
	Day            time.Time
	Tier           string
	Basis          string
	Kept           int
	Broken         int
	BrokenLate     int
	BrokenLost     int
	BrokenReturned int
	LateByP50      *float64
	LateByP90      *float64
}

// PrecisionDay is what the store aggregates for one UTC day of PromiseMade and
// one tier: the promises made, the sum and percentiles of their window widths
// in minutes, how many windows are one local day, how many were read in UTC
// for want of a zone, how many promises were revised at least once and how
// many became unkeepable.
type PrecisionDay struct {
	Day              time.Time
	Tier             string
	PromisesMade     int
	WindowMinutesSum float64
	WindowP50        float64
	WindowP90        float64
	ExactDay         int
	ZoneUnknown      int
	Revised          int
	Unkeepable       int
}

// ConversionDay is what the store counts for one UTC day and one tier: the
// offers quoted and the promises made.
type ConversionDay struct {
	Day          time.Time
	Tier         string
	OffersQuoted int
	PromisesMade int
}
