package http

import (
	"encoding/json"
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
