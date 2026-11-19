package analyticsstore_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// answered applies a DeliveryRequestAnswered the way the decoder builds it.
func (sd *seeder) answered(id, region, kind string, at time.Time, answered bool, shortest *int) {
	sd.t.Helper()
	sd.apply(report.Event{Kind: report.KindDeliveryRequestAnswered, At: at, RequestID: id, Region: region, RequestKind: kind,
		Items: 3, Answered: answered, ShortestDays: shortest})
}

func intp(n int) *int { return &n }

// requestRows renders the buckets as "day/region/answered/shortestDays" (-1: none) and the count.
func requestRows(buckets []report.RequestBucket) []row {
	out := []row{}
	for _, b := range buckets {
		days := -1
		if b.ShortestDays != nil {
			days = *b.ShortestDays
		}
		out = append(out, row{dayKey(b.Day, b.Region, fmt.Sprint(b.Answered), fmt.Sprint(days)), []float64{float64(b.Count)}})
	}
	return out
}

func contractRequests(t *testing.T, s store) {
	sd := &seeder{t: t, s: s}
	at := nov3.Add(15 * time.Hour)
	sd.answered("R1", "78", "ESTIMATE", at, true, intp(2))
	sd.answered("R2", "78", "QUOTE", at.Add(time.Minute), true, intp(2))
	sd.answered("R3", "78", "ESTIMATE", at, false, nil) // answered with no promise
	sd.answered("R4", "10", "QUOTE", at, true, intp(0))
	sd.answered("R5", "9", "QUOTE", nov4.Add(time.Hour), true, intp(5))
	sd.answered("R6", "7", "QUOTE", at, true, nil) // answered, but no promised days known
	// Outside the range: exactly at To (exclusive) and one second before From.
	sd.answered("R7", "78", "QUOTE", fullRange.To, true, intp(1))
	sd.answered("R8", "78", "QUOTE", nov3.Add(-time.Second), true, intp(1))
	// The same request id under a new CloudEvents id keeps the FIRST fact.
	sd.answered("R1", "78", "ESTIMATE", at, true, intp(9))
	got, err := s.RequestCoverage(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	// per day/region/answered/shortest days; unanswered before answered, no promised days first.
	sameRows(t, "request coverage", requestRows(got), []row{
		{"2026-11-03/10/true/0", []float64{1}},
		{"2026-11-03/7/true/-1", []float64{1}},
		{"2026-11-03/78/false/-1", []float64{1}},
		{"2026-11-03/78/true/2", []float64{2}},
		{"2026-11-04/9/true/5", []float64{1}},
	})
	// The offers-based coverage is untouched by request facts.
	offers, err := s.PromiseCoverage(ctx, fullRange)
	if err != nil || len(offers) != 0 {
		t.Fatalf("offers coverage = %v, %v; request facts are not offers", offers, err)
	}
	// The pure rules over both: the request share of the day.
	days := report.PromiseCoverage(offers, got)
	if len(days) != 2 || days[0].Regions[0].Region != "78" || days[0].Regions[0].RequestsAnswered != 3 ||
		days[0].Regions[0].RequestsWithPromise != 2 || days[0].Regions[0].RequestsWithin[1].Count != 2 {
		t.Fatalf("coverage report = %+v", days)
	}
	// An empty day inside the range has no row, and a range with no fact is empty, not null.
	none, err := s.RequestCoverage(ctx, report.Range{From: nov2.Add(-96 * time.Hour), To: nov2.Add(-72 * time.Hour)})
	if err != nil || none == nil || len(none) != 0 {
		t.Fatalf("an empty range = %#v, %v; want empty non-nil", none, err)
	}
}
