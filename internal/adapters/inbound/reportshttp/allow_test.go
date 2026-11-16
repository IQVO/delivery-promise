package reportshttp_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/claudioed/delivery-promise/internal/adapters/inbound/reportshttp"
)

// TestWrongMethodCarriesAllowHeader pins RFC 9110 section 15.5.6: a 405 MUST
// list the methods the resource supports. chi sets Allow only in its DEFAULT
// 405 handler, so the custom RFC 9457 handler has to derive the list itself.
func TestWrongMethodCarriesAllowHeader(t *testing.T) {
	h := reportshttp.NewRouter(&reportshttp.Server{Reader: &fakeReader{}}, slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	cases := []struct{ method, path string }{
		{http.MethodPost, "/healthz"},
		{http.MethodDelete, "/readyz"},
		{http.MethodPost, "/reports/promise-reliability"},
		{http.MethodPut, "/reports/promise-precision"},
		{http.MethodPatch, "/reports/quote-conversion"},
		{http.MethodDelete, "/reports/freshness"},
	}
	for _, tc := range cases {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), tc.method, tc.path, http.NoBody))
			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			if got := rec.Header().Get("Allow"); got != "GET" {
				t.Errorf("Allow = %q, want %q", got, "GET")
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", ct)
			}
		})
	}
}

// TestUnknownPathHasNoAllowHeader: a 404 is not a 405 and lists no methods.
func TestUnknownPathHasNoAllowHeader(t *testing.T) {
	h := reportshttp.NewRouter(&reportshttp.Server{Reader: &fakeReader{}}, slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/reports/nope", http.NoBody))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "" {
		t.Errorf("Allow = %q, want empty", got)
	}
}
