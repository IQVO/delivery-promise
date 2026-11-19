package reportshttp_test

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/claudioed/delivery-promise/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

func days(n int) *int { return &n }

func TestQuoteConversionCarriesThePromiseCoverageBlockPerDay(t *testing.T) {
	r := &fakeReader{
		conversion: []report.ConversionDay{{Day: nov3, Tier: "NEXT_DAY", OffersQuoted: 4, PromisesMade: 1}},
		coverage: []report.CoverageBucket{
			{Day: nov3, Region: "78", PromisedDays: days(1), Count: 3},
			{Day: nov3, Region: "78", PromisedDays: days(4), Count: 1},
			{Day: nov3, Region: "10", Count: 2},
		},
	}
	code, _, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/quote-conversion?to=2026-11-03")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	within := func(n1, n2, n3, n5 any, req float64) []any {
		share := func(n any) any {
			if n == nil || req == 0 {
				return nil
			}
			return n.(float64) / req
		}
		out := []any{}
		for i, d := range []float64{1, 2, 3, 5} {
			n := []any{n1, n2, n3, n5}[i]
			out = append(out, map[string]any{"days": d, "count": n, "share": share(n)})
		}
		return out
	}
	want := []any{map[string]any{"day": "2026-11-03", "regions": []any{
		map[string]any{"region": "78", "requests": 4.0, "answeredWithPromise": 4.0, "answeredWithinDays": within(3.0, 3.0, 3.0, 4.0, 4),
			"requestsAnswered": 0.0, "requestsWithPromise": 0.0, "requestsWithinDays": within(0.0, 0.0, 0.0, 0.0, 0), "requestShare": nil},
		map[string]any{"region": "10", "requests": 2.0, "answeredWithPromise": 0.0, "answeredWithinDays": within(0.0, 0.0, 0.0, 0.0, 2),
			"requestsAnswered": 0.0, "requestsWithPromise": 0.0, "requestsWithinDays": within(0.0, 0.0, 0.0, 0.0, 0), "requestShare": nil},
	}}}
	if !reflect.DeepEqual(body["promiseCoverage"], want) {
		t.Fatalf("promiseCoverage = %v\nwant %v", body["promiseCoverage"], want)
	}
	// The existing fields are unchanged.
	if !reflect.DeepEqual(body["days"], []any{map[string]any{"day": "2026-11-03", "tier": "NEXT_DAY", "offersQuoted": 4.0, "promisesMade": 1.0, "conversion": 0.25}}) {
		t.Fatalf("days = %v", body["days"])
	}
	if body["from"] != "2026-10-05" || body["to"] != "2026-11-03" || len(body) != 4 {
		t.Fatalf("body = %v, want from, to, days and promiseCoverage only", body)
	}
}

func TestAnEmptyRangeHasAnEmptyPromiseCoverageArray(t *testing.T) {
	_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/quote-conversion?from=2026-11-01&to=2026-11-01")
	cov, ok := body["promiseCoverage"].([]any)
	if !ok || len(cov) != 0 {
		t.Fatalf("promiseCoverage = %#v, want []", body["promiseCoverage"])
	}
}

func TestACoverageReadFailureIsAnOpaque500(t *testing.T) {
	r := &fakeReader{coverageErr: errors.New("pq: password authentication failed for secret-role")}
	code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/quote-conversion")
	assertProblem(t, code, ct, body, http.StatusInternalServerError, "internal-error")
}

func TestQuoteConversionCarriesTheRequestBasedFieldsBesideTheOffersBasedOnes(t *testing.T) {
	r := &fakeReader{
		coverage: []report.CoverageBucket{{Day: nov3, Region: "78", PromisedDays: days(1), Count: 7}},
		requests: []report.RequestBucket{
			{Day: nov3, Region: "78", Answered: true, ShortestDays: days(1), Count: 2},
			{Day: nov3, Region: "78", Answered: true, ShortestDays: days(4), Count: 1},
			{Day: nov3, Region: "78", Count: 1},
			{Day: nov3, Region: "55", Count: 3},
		},
	}
	code, _, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/quote-conversion?to=2026-11-03")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	regions := body["promiseCoverage"].([]any)[0].(map[string]any)["regions"].([]any)
	if len(regions) != 2 {
		t.Fatalf("regions = %v", regions)
	}
	r78 := regions[0].(map[string]any) // 4 requests answered rank before 3
	if r78["region"] != "78" || r78["requests"] != 7.0 || r78["answeredWithPromise"] != 7.0 || r78["requestsAnswered"] != 4.0 || r78["requestsWithPromise"] != 3.0 || r78["requestShare"] != 0.75 {
		t.Fatalf("region 78 = %v", r78)
	}
	wantWithin := []any{
		map[string]any{"days": 1.0, "count": 2.0, "share": 0.5}, map[string]any{"days": 2.0, "count": 2.0, "share": 0.5},
		map[string]any{"days": 3.0, "count": 2.0, "share": 0.5}, map[string]any{"days": 5.0, "count": 3.0, "share": 0.75},
	}
	if !reflect.DeepEqual(r78["requestsWithinDays"], wantWithin) {
		t.Fatalf("requestsWithinDays = %v\nwant %v", r78["requestsWithinDays"], wantWithin)
	}
	r55 := regions[1].(map[string]any) // only unanswered requests: a share of 0, never null
	if r55["region"] != "55" || r55["requests"] != 0.0 || r55["requestsAnswered"] != 3.0 || r55["requestsWithPromise"] != 0.0 || r55["requestShare"] != 0.0 {
		t.Fatalf("region 55 = %v", r55)
	}
}

func TestARequestCoverageReadFailureIsAnOpaque500(t *testing.T) {
	r := &fakeReader{requestsErr: errors.New("pq: relation requests_answered does not exist")}
	code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/quote-conversion")
	assertProblem(t, code, ct, body, http.StatusInternalServerError, "internal-error")
}
