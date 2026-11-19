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
		map[string]any{"region": "78", "requests": 4.0, "answeredWithPromise": 4.0, "answeredWithinDays": within(3.0, 3.0, 3.0, 4.0, 4)},
		map[string]any{"region": "10", "requests": 2.0, "answeredWithPromise": 0.0, "answeredWithinDays": within(0.0, 0.0, 0.0, 0.0, 2)},
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
