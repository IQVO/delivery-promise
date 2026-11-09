package report

import (
	"math"
	"sort"
	"time"
)

// ReliabilityRow is a ReliabilityDay plus the KPIs of ADR 0018: Judged = kept +
// broken and KeptRate = kept / judged.
type ReliabilityRow struct {
	ReliabilityDay
	Judged   int
	KeptRate float64
}

// PromiseReliability derives each row's KPIs. Order is preserved; a nil input
// yields an empty, non-nil slice.
func PromiseReliability(days []ReliabilityDay) []ReliabilityRow {
	out := make([]ReliabilityRow, 0, len(days))
	for _, d := range days {
		judged := d.Kept + d.Broken
		out = append(out, ReliabilityRow{ReliabilityDay: d, Judged: judged, KeptRate: Rate(d.Kept, judged)})
	}
	return out
}

// PrecisionRow is a PrecisionDay plus the KPIs of ADR 0018: the mean window
// width, and the exact-day and revised shares of the promises made.
type PrecisionRow struct {
	PrecisionDay
	WindowMean    float64
	ExactDayShare float64
	RevisedShare  float64
}

// PromisePrecision derives each row's KPIs. Order is preserved; a nil input
// yields an empty, non-nil slice.
func PromisePrecision(days []PrecisionDay) []PrecisionRow {
	out := make([]PrecisionRow, 0, len(days))
	for _, d := range days {
		out = append(out, PrecisionRow{
			PrecisionDay:  d,
			WindowMean:    Mean(d.WindowMinutesSum, d.PromisesMade),
			ExactDayShare: Rate(d.ExactDay, d.PromisesMade),
			RevisedShare:  Rate(d.Revised, d.PromisesMade),
		})
	}
	return out
}

// ConversionRow is a ConversionDay plus its conversion = promisesMade /
// offersQuoted, nil when no offer was quoted that day and tier.
type ConversionRow struct {
	ConversionDay
	Conversion *float64
}

// QuoteConversion derives each row's conversion. Order is preserved; a nil
// input yields an empty, non-nil slice.
func QuoteConversion(days []ConversionDay) []ConversionRow {
	out := make([]ConversionRow, 0, len(days))
	for _, d := range days {
		out = append(out, ConversionRow{ConversionDay: d, Conversion: RateOrNil(d.PromisesMade, d.OffersQuoted)})
	}
	return out
}

// Rate is num/den, or 0 when den is not positive (nothing to rate).
func Rate(num, den int) float64 {
	if den <= 0 {
		return 0
	}
	return float64(num) / float64(den)
}

// RateOrNil is num/den, or nil when den is not positive (the ratio is
// undefined, not zero).
func RateOrNil(num, den int) *float64 {
	if den <= 0 {
		return nil
	}
	r := Rate(num, den)
	return &r
}

// Mean is sum/n, or 0 when n is not positive.
func Mean(sum float64, n int) float64 {
	if n <= 0 {
		return 0
	}
	return sum / float64(n)
}

// Percentile is the continuous percentile (linear interpolation between
// closest ranks, p in [0,1]) of values, the same definition as Postgres'
// percentile_cont. values need not be sorted; it is not modified. An empty
// input yields 0.
func Percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	rank := p * float64(len(sorted)-1)
	lo := math.Floor(rank)
	hi := math.Ceil(rank)
	return sorted[int(lo)] + (sorted[int(hi)]-sorted[int(lo)])*(rank-lo)
}

// PercentileOrNil is Percentile, or nil for an empty input (percentile_cont
// over no row is NULL).
func PercentileOrNil(values []float64, p float64) *float64 {
	if len(values) == 0 {
		return nil
	}
	v := Percentile(values, p)
	return &v
}

// Freshness is the projection's freshness: AsOf is the CloudEvents time of the
// newest event applied and LagSeconds is now - AsOf. Both are nil while the
// projection has applied nothing.
type Freshness struct {
	AsOf       *time.Time
	LagSeconds *float64
}

// ComputeFreshness derives the lag from the newest applied event time. A clock
// that is behind the event (skew) reads as zero lag, never negative.
func ComputeFreshness(asOf *time.Time, now time.Time) Freshness {
	if asOf == nil {
		return Freshness{}
	}
	// math.Max, not `if lag < 0`: both yield 0 at the boundary, which would
	// leave an equivalent `<` -> `<=` mutant.
	lag := math.Max(0, now.Sub(*asOf).Seconds())
	utc := asOf.UTC()
	return Freshness{AsOf: &utc, LagSeconds: &lag}
}
