package reportshttp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// fakeReader answers fixed rows and records the range it was asked for.
type fakeReader struct {
	reliability []report.ReliabilityDay
	precision   []report.PrecisionDay
	conversion  []report.ConversionDay
	last        *time.Time
	err         error
	asked       report.Range
}

func (f *fakeReader) PromiseReliability(_ context.Context, r report.Range) ([]report.ReliabilityDay, error) {
	f.asked = r
	return f.reliability, f.err
}

func (f *fakeReader) PromisePrecision(_ context.Context, r report.Range) ([]report.PrecisionDay, error) {
	f.asked = r
	return f.precision, f.err
}

func (f *fakeReader) QuoteConversion(_ context.Context, r report.Range) ([]report.ConversionDay, error) {
	f.asked = r
	return f.conversion, f.err
}

func (f *fakeReader) LastEventAt(context.Context) (*time.Time, error) { return f.last, f.err }

type fakePing struct{ err error }

func (p fakePing) Ping(context.Context) error { return p.err }

var (
	now  = time.Date(2026, 11, 8, 18, 30, 0, 0, time.UTC)
	nov3 = time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
)

func ptr(v float64) *float64 { return &v }

// reportPaths are the four documented report routes.
var reportPaths = []string{"/reports/promise-reliability", "/reports/promise-precision", "/reports/quote-conversion", "/reports/freshness"}

func serve(t *testing.T, s *reportshttp.Server, method, target string) (int, string, map[string]any) {
	t.Helper()
	if s.Now == nil {
		s.Now = func() time.Time { return now }
	}
	h := reportshttp.NewRouter(s, slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), method, target, nil))
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("%s %s: body %q is not JSON: %v", method, target, rec.Body.String(), err)
	}
	return rec.Code, rec.Header().Get("Content-Type"), body
}

func TestPromiseReliabilityReport(t *testing.T) {
	r := &fakeReader{reliability: []report.ReliabilityDay{
		{Day: nov3, Tier: "NEXT_DAY", Basis: "SCHEDULED", Kept: 3, Broken: 1, BrokenLate: 1, LateByP50: ptr(30), LateByP90: ptr(42)},
		{Day: nov3, Tier: "UNKNOWN", Basis: "UNKNOWN", Broken: 2, BrokenLost: 1, BrokenReturned: 1},
	}}
	code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/promise-reliability?from=2026-11-01&to=2026-11-07")
	if code != http.StatusOK || ct != "application/json" {
		t.Fatalf("status %d content-type %s", code, ct)
	}
	want := map[string]any{"from": "2026-11-01", "to": "2026-11-07", "days": []any{
		map[string]any{"day": "2026-11-03", "tier": "NEXT_DAY", "basis": "SCHEDULED", "judged": 4.0, "kept": 3.0, "broken": 1.0,
			"keptRate": 0.75, "brokenByCause": map[string]any{"late": 1.0, "lost": 0.0, "returned": 0.0},
			"lateByMinutes": map[string]any{"p50": 30.0, "p90": 42.0}},
		map[string]any{"day": "2026-11-03", "tier": "UNKNOWN", "basis": "UNKNOWN", "judged": 2.0, "kept": 0.0, "broken": 2.0,
			"keptRate": 0.0, "brokenByCause": map[string]any{"late": 0.0, "lost": 1.0, "returned": 1.0},
			"lateByMinutes": map[string]any{"p50": nil, "p90": nil}},
	}}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
	if !r.asked.From.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) || !r.asked.To.Equal(time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("asked range = %+v, want [11-01, 11-08)", r.asked)
	}
}

func TestPromisePrecisionReport(t *testing.T) {
	r := &fakeReader{precision: []report.PrecisionDay{{Day: nov3, Tier: "NEXT_DAY", PromisesMade: 4, WindowMinutesSum: 3000,
		WindowP50: 720, WindowP90: 1100, ExactDay: 3, ZoneUnknown: 1, Revised: 1, Unkeepable: 2}}}
	code, _, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/promise-precision")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	want := map[string]any{"from": "2026-10-10", "to": "2026-11-08", "days": []any{
		map[string]any{"day": "2026-11-03", "tier": "NEXT_DAY", "promisesMade": 4.0,
			"windowMinutes": map[string]any{"mean": 750.0, "p50": 720.0, "p90": 1100.0},
			"exactDayShare": 0.75, "zoneUnknown": 1.0, "revisedShare": 0.25, "unkeepableCount": 2.0},
	}}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
}

func TestQuoteConversionReport(t *testing.T) {
	r := &fakeReader{conversion: []report.ConversionDay{
		{Day: nov3, Tier: "NEXT_DAY", OffersQuoted: 4, PromisesMade: 1},
		{Day: nov3, Tier: "SAME_DAY", PromisesMade: 2},
	}}
	code, _, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, "/reports/quote-conversion?to=2026-11-03")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	want := map[string]any{"from": "2026-10-05", "to": "2026-11-03", "days": []any{
		map[string]any{"day": "2026-11-03", "tier": "NEXT_DAY", "offersQuoted": 4.0, "promisesMade": 1.0, "conversion": 0.25},
		map[string]any{"day": "2026-11-03", "tier": "SAME_DAY", "offersQuoted": 0.0, "promisesMade": 2.0, "conversion": nil},
	}}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v\nwant %v", body, want)
	}
}

func TestEmptyReportsAreEmptyArrays(t *testing.T) {
	for _, path := range reportPaths[:3] {
		_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, path)
		if days, ok := body["days"].([]any); !ok || len(days) != 0 {
			t.Errorf("%s days = %#v, want []", path, body["days"])
		}
	}
}

func TestFreshness(t *testing.T) {
	_, _, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/freshness")
	if v, ok := body["asOf"]; !ok || v != nil || body["lagSeconds"] != nil {
		t.Fatalf("before the first event = %v, want explicit nulls", body)
	}
	asOf := now.Add(-90 * time.Second)
	_, _, body = serve(t, &reportshttp.Server{Reader: &fakeReader{last: &asOf}}, http.MethodGet, "/reports/freshness")
	if body["asOf"] != "2026-11-08T18:28:30Z" || body["lagSeconds"] != 90.0 {
		t.Fatalf("freshness = %v", body)
	}
}

func assertProblem(t *testing.T, code int, ct string, body map[string]any, wantCode int, wantType string) {
	t.Helper()
	if code != wantCode || ct != "application/problem+json" || body["type"] != "https://retailnetwork.example/problems/"+wantType || body["status"] != float64(wantCode) {
		t.Fatalf("got %d %s %v, want %d problem %s", code, ct, body, wantCode, wantType)
	}
}

func TestInvalidRangeIsAProblem(t *testing.T) {
	for _, path := range reportPaths[:3] {
		for _, q := range []string{"?from=2026-11-05&to=2026-11-04", "?from=nope", "?to=2026-13-01", "?from=2025-01-01&to=2026-11-01"} {
			code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, path+q)
			assertProblem(t, code, ct, body, http.StatusBadRequest, "invalid-report-range")
		}
	}
}

func TestReaderFailureIsAnOpaque500(t *testing.T) {
	r := &fakeReader{err: errors.New("pq: password authentication failed for secret-role")}
	for _, path := range reportPaths {
		code, ct, body := serve(t, &reportshttp.Server{Reader: r}, http.MethodGet, path)
		assertProblem(t, code, ct, body, http.StatusInternalServerError, "internal-error")
		if strings.Contains(body["detail"].(string), "secret") {
			t.Fatalf("%s leaked the cause: %v", path, body)
		}
	}
}

func TestUnknownPathAndWriteMethodsAreProblems(t *testing.T) {
	code, ct, body := serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodGet, "/reports/nope")
	assertProblem(t, code, ct, body, http.StatusNotFound, "not-found")
	code, ct, body = serve(t, &reportshttp.Server{Reader: &fakeReader{}}, http.MethodPost, "/reports/freshness")
	assertProblem(t, code, ct, body, http.StatusMethodNotAllowed, "method-not-allowed")
}

func TestHealthAndReadiness(t *testing.T) {
	if code, _, body := serve(t, &reportshttp.Server{}, http.MethodGet, "/healthz"); code != http.StatusOK || body["status"] != "ok" {
		t.Fatalf("healthz = %d %v", code, body)
	}
	if code, _, body := serve(t, &reportshttp.Server{Ping: fakePing{}}, http.MethodGet, "/readyz"); code != http.StatusOK || body["status"] != "ready" {
		t.Fatalf("readyz = %d %v", code, body)
	}
	code, ct, body := serve(t, &reportshttp.Server{Ping: fakePing{err: errors.New("down")}}, http.MethodGet, "/readyz")
	assertProblem(t, code, ct, body, http.StatusServiceUnavailable, "dependency-unavailable")
	draining := &reportshttp.Server{}
	draining.SetNotReady()
	if code, _, body := serve(t, draining, http.MethodGet, "/readyz"); code != http.StatusServiceUnavailable || body["status"] != "not_ready" {
		t.Fatalf("draining readyz = %d %v", code, body)
	}
}
