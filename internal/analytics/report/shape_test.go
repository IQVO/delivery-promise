package report_test

import (
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

func ptr(v float64) *float64 { return &v }

func TestPromiseReliabilityDerivesJudgedAndKeptRate(t *testing.T) {
	day := utcDay(2026, 11, 3)
	in := []report.ReliabilityDay{
		{Day: day, Tier: "NEXT_DAY", Basis: "SCHEDULED", Kept: 3, Broken: 1, BrokenLate: 1, LateByP50: ptr(30), LateByP90: ptr(30)},
		{Day: day, Tier: "UNKNOWN", Basis: "UNKNOWN", Broken: 2, BrokenLost: 1, BrokenReturned: 1},
	}
	want := []report.ReliabilityRow{
		{ReliabilityDay: in[0], Judged: 4, KeptRate: 0.75},
		{ReliabilityDay: in[1], Judged: 2, KeptRate: 0},
	}
	if got := report.PromiseReliability(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v\nwant %+v", got, want)
	}
	if empty := report.PromiseReliability(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("nil input must yield an empty, non-nil slice, got %#v", empty)
	}
}

func TestPromisePrecisionDerivesMeanAndShares(t *testing.T) {
	day := utcDay(2026, 11, 3)
	in := []report.PrecisionDay{{Day: day, Tier: "NEXT_DAY", PromisesMade: 4, WindowMinutesSum: 3000, WindowP50: 720, WindowP90: 1100,
		ExactDay: 3, ZoneUnknown: 1, Revised: 1, Unkeepable: 1}}
	want := []report.PrecisionRow{{PrecisionDay: in[0], WindowMean: 750, ExactDayShare: 0.75, RevisedShare: 0.25}}
	if got := report.PromisePrecision(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows = %+v\nwant %+v", got, want)
	}
	if empty := report.PromisePrecision(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("nil input must yield an empty, non-nil slice, got %#v", empty)
	}
}

func TestQuoteConversionIsNilWithoutOffers(t *testing.T) {
	day := utcDay(2026, 11, 3)
	got := report.QuoteConversion([]report.ConversionDay{
		{Day: day, Tier: "NEXT_DAY", OffersQuoted: 4, PromisesMade: 1},
		{Day: day, Tier: "SAME_DAY", OffersQuoted: 0, PromisesMade: 2},
	})
	if len(got) != 2 || got[0].Conversion == nil || *got[0].Conversion != 0.25 || got[0].Tier != "NEXT_DAY" || got[0].OffersQuoted != 4 {
		t.Fatalf("row 0 = %+v", got[0])
	}
	if got[1].Conversion != nil || got[1].PromisesMade != 2 {
		t.Fatalf("a day without offers must have a nil conversion, got %+v", got[1])
	}
	if empty := report.QuoteConversion(nil); empty == nil || len(empty) != 0 {
		t.Fatalf("nil input must yield an empty, non-nil slice, got %#v", empty)
	}
}

func TestRate(t *testing.T) {
	cases := []struct {
		num, den int
		want     float64
	}{{1, 4, 0.25}, {3, 3, 1}, {0, 5, 0}, {2, 0, 0}, {2, -1, 0}, {1, 1, 1}}
	for _, c := range cases {
		if got := report.Rate(c.num, c.den); got != c.want {
			t.Errorf("Rate(%d, %d) = %v, want %v", c.num, c.den, got, c.want)
		}
	}
}

func TestRateOrNil(t *testing.T) {
	for _, den := range []int{0, -1} {
		if r := report.RateOrNil(1, den); r != nil {
			t.Errorf("RateOrNil(1, %d) = %v, want nil", den, *r)
		}
	}
	if r := report.RateOrNil(1, 1); r == nil || *r != 1 {
		t.Fatalf("RateOrNil(1, 1) = %v, want 1", r)
	}
	if r := report.RateOrNil(3, 2); r == nil || *r != 1.5 {
		t.Fatalf("RateOrNil(3, 2) = %v, want 1.5", r)
	}
}

func TestMean(t *testing.T) {
	cases := []struct {
		sum  float64
		n    int
		want float64
	}{{10, 4, 2.5}, {7, 1, 7}, {5, 0, 0}, {5, -2, 0}, {0, 3, 0}}
	for _, c := range cases {
		if got := report.Mean(c.sum, c.n); got != c.want {
			t.Errorf("Mean(%v, %d) = %v, want %v", c.sum, c.n, got, c.want)
		}
	}
}

func TestPercentileMatchesPercentileCont(t *testing.T) {
	values := []float64{7, 1, 3, 5}
	cases := []struct{ p, want float64 }{{0.5, 4}, {0.9, 6.4}, {0, 1}, {1, 7}, {0.25, 2.5}}
	for _, c := range cases {
		if got := report.Percentile(values, c.p); math.Abs(got-c.want) > 1e-9 {
			t.Errorf("Percentile(%v) = %v, want %v", c.p, got, c.want)
		}
		if got := report.PercentileOrNil(values, c.p); got == nil || math.Abs(*got-c.want) > 1e-9 {
			t.Errorf("PercentileOrNil(%v) = %v, want %v", c.p, got, c.want)
		}
	}
	if !reflect.DeepEqual(values, []float64{7, 1, 3, 5}) {
		t.Fatalf("Percentile modified its input: %v", values)
	}
	if got := report.Percentile(nil, 0.5); got != 0 {
		t.Fatalf("empty = %v, want 0", got)
	}
	if got := report.PercentileOrNil(nil, 0.5); got != nil {
		t.Fatalf("empty = %v, want nil", *got)
	}
	if got := report.Percentile([]float64{3}, 0.9); got != 3 {
		t.Fatalf("single = %v, want 3", got)
	}
}

func TestComputeFreshness(t *testing.T) {
	if f := report.ComputeFreshness(nil, now); f.AsOf != nil || f.LagSeconds != nil {
		t.Fatalf("nothing applied must give nil/nil, got %+v", f)
	}
	asOf := time.Date(2026, 11, 8, 18, 29, 0, 0, time.UTC) // now is 18:30 UTC
	f := report.ComputeFreshness(&asOf, now)
	if f.AsOf == nil || !f.AsOf.Equal(asOf) || f.AsOf.Location() != time.UTC || f.LagSeconds == nil || *f.LagSeconds != 60 {
		t.Fatalf("freshness = %+v", f)
	}
	future := now.Add(time.Minute)
	if f := report.ComputeFreshness(&future, now); *f.LagSeconds != 0 {
		t.Fatalf("a clock behind the event must read zero lag, got %v", *f.LagSeconds)
	}
}
