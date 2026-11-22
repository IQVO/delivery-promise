package report_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

func TestRequestKindsAreTheTwoRequests(t *testing.T) {
	if got, want := report.RequestKinds(), []string{"QUOTE", "ESTIMATE"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("RequestKinds = %v, want %v", got, want)
	}
	for kind, want := range map[string]bool{"QUOTE": true, "ESTIMATE": true, "": false, "quote": false, "OTHER": false} {
		if got := report.IsRequestKind(kind); got != want {
			t.Errorf("IsRequestKind(%q) = %v, want %v", kind, got, want)
		}
	}
}

func validRequest() report.Event {
	return report.Event{Kind: report.KindDeliveryRequestAnswered, EventID: "e7", At: at, RequestID: "req-1", Region: "78", RequestKind: "ESTIMATE", Items: 12, Answered: true, ShortestDays: days(2)}
}

func TestValidateAcceptsACompleteRequestFact(t *testing.T) {
	unanswered := validRequest()
	unanswered.Answered, unanswered.ShortestDays = false, nil
	oneChar := validRequest()
	oneChar.Region = "7"
	sameDay := validRequest()
	sameDay.ShortestDays = days(0)
	oneItem := validRequest()
	oneItem.Items = 1
	for name, e := range map[string]report.Event{"answered": validRequest(), "unanswered": unanswered, "one-character region": oneChar, "same day": sameDay, "one item": oneItem} {
		if err := e.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestValidateNamesTheFirstMissingFieldOfARequestFact(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*report.Event)
	}{
		{"requestId", func(e *report.Event) { e.RequestID = "" }},
		{"destinationRegion", func(e *report.Event) { e.Region = "" }},
		{"destinationRegion", func(e *report.Event) { e.Region = "787" }},
		{"destinationRegion", func(e *report.Event) { e.Region = "éèà" }},
		{"a known kind", func(e *report.Event) { e.RequestKind = "" }},
		{"a known kind", func(e *report.Event) { e.RequestKind = "quote" }},
		{"an itemCount of at least 1", func(e *report.Event) { e.Items = 0 }},
		{"a non-negative shortestPromisedDays", func(e *report.Event) { e.ShortestDays = days(-1) }},
		{"id", func(e *report.Event) { e.EventID = "" }},
		{"time", func(e *report.Event) { e.At = time.Time{} }},
	}
	for _, c := range cases {
		e := validRequest()
		c.mutate(&e)
		err := e.Validate()
		if !errors.Is(err, report.ErrInvalidEvent) || !strings.HasSuffix(err.Error(), ": "+c.name+" is required") {
			t.Errorf("%s: err = %v", c.name, err)
		}
	}
}

func req(day time.Time, region string, answered bool, shortest *int, count int) report.RequestBucket {
	return report.RequestBucket{Day: day, Region: region, Answered: answered, ShortestDays: shortest, Count: count}
}

func reqCounts(r report.RegionCoverage) []int {
	out := []int{}
	for _, w := range r.RequestsWithin {
		out = append(out, w.Count)
	}
	return out
}

func TestRequestCoverageIsCumulativeAndSeparateFromTheOffers(t *testing.T) {
	got := report.PromiseCoverage(
		[]report.CoverageBucket{bucket(cov, "78", days(1), 7), bucket(cov, "78", nil, 1)},
		[]report.RequestBucket{
			req(cov, "78", true, days(0), 1), req(cov, "78", true, days(1), 1), req(cov, "78", true, days(2), 1),
			req(cov, "78", true, days(3), 1), req(cov, "78", true, days(4), 1), req(cov, "78", true, days(5), 1),
			req(cov, "78", true, days(6), 1), req(cov, "78", false, nil, 2),
		})
	if len(got) != 1 || len(got[0].Regions) != 1 {
		t.Fatalf("coverage = %+v", got)
	}
	r := got[0].Regions[0]
	if r.Requests != 8 || r.AnsweredWithPromise != 7 || !reflect.DeepEqual(counts(r), []int{7, 7, 7, 7}) {
		t.Fatalf("the offers-based fields changed: %+v", r)
	}
	if r.RequestsAnswered != 9 || r.RequestsWithPromise != 7 || !reflect.DeepEqual(reqCounts(r), []int{2, 3, 4, 6}) {
		t.Fatalf("request-based fields = %+v", r)
	}
	if r.RequestShare == nil || *r.RequestShare != 7.0/9.0 {
		t.Fatalf("RequestShare = %v, want 7/9", r.RequestShare)
	}
	for i, w := range r.RequestsWithin {
		if w.Days != report.CoverageDays()[i] || w.Share == nil || *w.Share != float64(w.Count)/9 {
			t.Errorf("RequestsWithin[%d] = %+v", i, w)
		}
	}
}

func TestAnOffersOnlyDayHasNoRequestShare(t *testing.T) {
	r := report.PromiseCoverage([]report.CoverageBucket{bucket(cov, "78", days(1), 2)}, nil)[0].Regions[0]
	if r.RequestsAnswered != 0 || r.RequestsWithPromise != 0 || r.RequestShare != nil || len(r.RequestsWithin) != 4 {
		t.Fatalf("region = %+v", r)
	}
	for _, w := range r.RequestsWithin {
		if w.Count != 0 || w.Share != nil {
			t.Fatalf("RequestsWithin = %+v, want zero counts and null shares", r.RequestsWithin)
		}
	}
}

func TestARegionOrDayWithOnlyRequestFactsStillAppears(t *testing.T) {
	d2 := cov.AddDate(0, 0, 1)
	got := report.PromiseCoverage(
		[]report.CoverageBucket{bucket(cov, "78", days(1), 1)},
		[]report.RequestBucket{req(cov, "10", false, nil, 3), req(d2, "55", true, days(2), 1)})
	if len(got) != 2 || !got[1].Day.Equal(d2) {
		t.Fatalf("days = %+v", got)
	}
	first := got[0].Regions
	if len(first) != 2 || first[0].Region != "10" || first[1].Region != "78" {
		t.Fatalf("regions of day 1 = %+v (the request-heavy region ranks first)", first)
	}
	if first[0].Requests != 0 || first[0].AnsweredWithPromise != 0 || first[0].RequestsAnswered != 3 || first[0].RequestShare == nil || *first[0].RequestShare != 0 {
		t.Fatalf("a region with unanswered requests only = %+v (share 0, not null)", first[0])
	}
	if r := got[1].Regions[0]; r.Region != "55" || r.RequestsWithPromise != 1 || r.Requests != 0 || !reflect.DeepEqual(reqCounts(r), []int{0, 1, 1, 1}) {
		t.Fatalf("region of day 2 = %+v", r)
	}
}

func TestRegionsRankByRequestsAnsweredThenOffersThenRegion(t *testing.T) {
	got := report.PromiseCoverage(
		[]report.CoverageBucket{bucket(cov, "AA", days(1), 50), bucket(cov, "BB", days(1), 5), bucket(cov, "CC", days(1), 5), bucket(cov, "DD", days(1), 1)},
		[]report.RequestBucket{req(cov, "DD", true, days(1), 2), req(cov, "CC", true, days(1), 2), req(cov, "ZZ", true, days(1), 1)})
	var order []string
	for _, r := range got[0].Regions {
		order = append(order, r.Region)
	}
	// 2 requests: CC (5 offers) before DD (1 offer); 1 request: ZZ; none: AA (50 offers) before BB (5)
	if want := []string{"CC", "DD", "ZZ", "AA", "BB"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	tie := report.PromiseCoverage(nil, []report.RequestBucket{req(cov, "B", true, days(1), 1), req(cov, "A", true, days(1), 1)})
	if tie[0].Regions[0].Region != "A" || tie[0].Regions[1].Region != "B" {
		t.Fatalf("ties break by region: %+v", tie[0].Regions)
	}
}

func twelveRegions() []report.RegionCoverage {
	var offers []report.CoverageBucket
	var reqs []report.RequestBucket
	for i := 0; i < 12; i++ {
		region := string(rune('A'+i)) + "x"
		reqs = append(reqs, req(cov, region, true, days(1), 12-i))
		offers = append(offers, bucket(cov, region, days(2), 1))
	}
	reqs = append(reqs, req(cov, "Ax", false, nil, 4))
	return report.PromiseCoverage(offers, reqs)[0].Regions
}

func TestRegionsPastTheTenthFoldTheirRequestCountsIntoOther(t *testing.T) {
	regions := twelveRegions()
	if len(regions) != 11 || regions[10].Region != "other" {
		t.Fatalf("regions = %d, last %q", len(regions), regions[len(regions)-1].Region)
	}
	o := regions[10] // Kx (2 requests) and Lx (1 request)
	if o.RequestsAnswered != 3 || o.RequestsWithPromise != 3 || o.Requests != 2 || o.AnsweredWithPromise != 2 {
		t.Fatalf("other = %+v", o)
	}
	if !reflect.DeepEqual(reqCounts(o), []int{3, 3, 3, 3}) || !reflect.DeepEqual(counts(o), []int{0, 2, 2, 2}) {
		t.Fatalf("other cumulative counts: requests %v offers %v", reqCounts(o), counts(o))
	}
	if o.RequestShare == nil || *o.RequestShare != 1 || o.RequestsWithin[0].Share == nil || *o.RequestsWithin[0].Share != 1 {
		t.Fatalf("other shares = %v %+v", o.RequestShare, o.RequestsWithin)
	}
}

func TestTheBusiestRegionStaysAheadOfTheFold(t *testing.T) {
	regions := twelveRegions()
	if first := regions[0]; first.Region != "Ax" || first.RequestsAnswered != 16 || first.RequestsWithPromise != 12 {
		t.Fatalf("first = %+v", first)
	}
}

func TestAnAnsweredRequestWithoutShortestDaysIsAPromiseInNoWindow(t *testing.T) {
	r := report.PromiseCoverage(nil, []report.RequestBucket{req(cov, "78", true, nil, 2)})[0].Regions[0]
	if r.RequestsAnswered != 2 || r.RequestsWithPromise != 2 || !reflect.DeepEqual(reqCounts(r), []int{0, 0, 0, 0}) {
		t.Fatalf("region = %+v", r)
	}
}

func TestRequestCoverageOfNothingIsEmptyNonNil(t *testing.T) {
	if got := report.PromiseCoverage(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("PromiseCoverage(nil, nil) = %#v", got)
	}
	if got := report.PromiseCoverage([]report.CoverageBucket{}, []report.RequestBucket{}); got == nil || len(got) != 0 {
		t.Fatalf("PromiseCoverage(empty, empty) = %#v", got)
	}
}

func TestARequestBucketOfAnotherLocationLandsOnTheUTCDay(t *testing.T) {
	loc := time.FixedZone("x", 3600)
	got := report.PromiseCoverage(nil, []report.RequestBucket{req(cov, "78", true, days(1), 1), req(cov.In(loc), "78", true, days(1), 1)})
	if len(got) != 1 || got[0].Regions[0].RequestsAnswered != 2 || got[0].Day.Location() != time.UTC {
		t.Fatalf("coverage = %+v", got)
	}
}
