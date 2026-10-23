package http

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serve(h http.Handler) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/x", nil))
	return rr
}

func TestRecovererWritesAnInternalErrorProblem(t *testing.T) {
	rr := serve(Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic("boom") })))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q", ct)
	}
	var p problemDetail
	if err := json.Unmarshal(rr.Body.Bytes(), &p); err != nil || p.Type != problemBaseURI+"internal-error" || strings.Contains(p.Detail, "boom") {
		t.Fatalf("problem = %+v err=%v", p, err)
	}
}

func TestRecovererRepanicsTheAbortSentinel(t *testing.T) {
	defer func() {
		if recover() != http.ErrAbortHandler { //nolint:errorlint // identity
			t.Fatal("ErrAbortHandler must propagate")
		}
	}()
	serve(Recoverer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { panic(http.ErrAbortHandler) })))
}

func TestAccessLogRecordsStatusAndLevel(t *testing.T) {
	cases := []struct {
		name    string
		handler http.HandlerFunc
		status  string
		level   string
	}{
		{"implicit 200", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }, `"status":200`, `"level":"INFO"`},
		{"explicit 404", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) }, `"status":404`, `"level":"INFO"`},
		{"panic is a logged 500", func(http.ResponseWriter, *http.Request) { panic("x") }, `"status":500`, `"level":"ERROR"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewJSONHandler(&buf, nil))
			serve(AccessLog(log)(Recoverer(tc.handler)))
			out := buf.String()
			if !strings.Contains(out, tc.status) || !strings.Contains(out, tc.level) || !strings.Contains(out, `"path":"/x"`) {
				t.Fatalf("log = %s", out)
			}
		})
	}
}

func TestAccessLogWithNilLoggerUsesTheDefault(t *testing.T) {
	if rr := serve(AccessLog(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))); rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestStatusRecorderKeepsTheFirstStatus(t *testing.T) {
	rec := &statusRecorder{ResponseWriter: httptest.NewRecorder()}
	rec.WriteHeader(http.StatusCreated)
	rec.WriteHeader(http.StatusTeapot)
	if rec.status != http.StatusCreated {
		t.Fatalf("status = %d", rec.status)
	}
}
