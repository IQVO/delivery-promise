package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/requestfacts"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/requestfact"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

const quoteBody = `{"destinationPostalPrefix":"78701","lines":[{"catalogItemId":"ci-1","quantity":1},{"catalogItemId":"ci-2","quantity":2}]}`

type spyFacts struct {
	mu        sync.Mutex
	quotes    []string
	estimates []int
	prefixes  []string
	items     []int
	seenBody  []string // the response body already written when the hand-over ran
	rec       *httptest.ResponseRecorder
}

func (s *spyFacts) body() string {
	if s.rec == nil {
		return ""
	}
	return s.rec.Body.String()
}

func (s *spyFacts) RecordQuote(prefix string, items int, res views.QuoteResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.quotes = append(s.quotes, res.Offers[0].OfferID)
	s.prefixes, s.items = append(s.prefixes, prefix), append(s.items, items)
	s.seenBody = append(s.seenBody, s.body())
}

func (s *spyFacts) RecordEstimate(items int, res views.EstimateResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.estimates = append(s.estimates, len(res.Estimates))
	s.items = append(s.items, items)
	s.seenBody = append(s.seenBody, s.body())
}

type panickyFacts struct{}

func (panickyFacts) RecordQuote(string, int, views.QuoteResult) { panic("boom") }
func (panickyFacts) RecordEstimate(int, views.EstimateResult)   { panic("boom") }

func postJSON(h http.Handler, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Idempotency-Key", "k-1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestTheFactIsHandedOverAfterTheResponseWasWritten(t *testing.T) {
	api := &fakeAPI{estimated: sampleEstimate()}
	spy := &spyFacts{}
	h := NewRouter(&Server{API: api, Facts: spy}, nil, "")
	for _, route := range []struct{ path, body string }{{"/delivery-quotes", quoteBody}, {"/delivery-estimates", estimateBody}} {
		req := httptest.NewRequest(http.MethodPost, route.path, strings.NewReader(route.body))
		rec := httptest.NewRecorder()
		spy.rec = rec
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", route.path, rec.Code)
		}
	}
	if len(spy.quotes) != 1 || spy.quotes[0] != "off-1" || spy.prefixes[0] != "78701" || spy.items[0] != 2 {
		t.Fatalf("quote hand-over = %v %v %v", spy.quotes, spy.prefixes, spy.items)
	}
	if len(spy.estimates) != 1 || spy.estimates[0] != 2 || spy.items[1] != 2 {
		t.Fatalf("estimate hand-over = %v %v", spy.estimates, spy.items)
	}
	for i, seen := range spy.seenBody {
		if !strings.HasPrefix(seen, "{") || !strings.HasSuffix(seen, "}\n") {
			t.Errorf("hand-over %d ran before the response was written: %q", i, seen)
		}
	}
}

func TestNoFactForARefusalOrAFailure(t *testing.T) {
	for _, err := range []error{shared.Invalid("bad"), errors.New("db down")} {
		spy := &spyFacts{}
		h := NewRouter(&Server{API: &fakeAPI{err: err}, Facts: spy}, nil, "")
		if rec := postJSON(h, "/delivery-quotes", quoteBody); rec.Code < 400 {
			t.Fatalf("status %d", rec.Code)
		}
		if rec := postJSON(h, "/delivery-estimates", estimateBody); rec.Code < 400 {
			t.Fatalf("status %d", rec.Code)
		}
		if len(spy.quotes)+len(spy.estimates) != 0 {
			t.Fatalf("a failed request left a fact: %+v", spy)
		}
	}
	spy := &spyFacts{}
	h := NewRouter(&Server{API: &fakeAPI{}, Facts: spy}, nil, "")
	if rec := postJSON(h, "/delivery-quotes", `{"nope":`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: %d", rec.Code)
	}
	if len(spy.quotes) != 0 {
		t.Fatal("a malformed request left a fact")
	}
}

// A quote runs inside the Idempotency-Key transaction: when that middleware
// answers anything but 200 (a replay, a failed commit) there is no fact.
func TestNoFactWhenTheIdempotencyLayerDoesNotAnswer200(t *testing.T) {
	answers := map[string]func(next http.Handler) http.Handler{
		"replay: the handler never runs": func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set(IdempotencyReplayedHeader, "true")
				w.WriteHeader(http.StatusOK)
			})
		},
		"the commit failed after the handler answered": func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				next.ServeHTTP(httptest.NewRecorder(), r) // the handler answers into the buffer
				writeProblem(w, r, internalProblem, "commit failed")
			})
		},
	}
	for name, mw := range answers {
		spy := &spyFacts{}
		h := NewRouter(&Server{API: &fakeAPI{}, Facts: spy, Idempotency: mw}, nil, "")
		postJSON(h, "/delivery-quotes", quoteBody)
		if len(spy.quotes) != 0 {
			t.Errorf("%s: left a fact", name)
		}
	}
}

func TestAPanickingRecorderLeavesTheResponseUntouched(t *testing.T) {
	api := &fakeAPI{estimated: sampleEstimate()}
	for _, route := range []struct{ path, body string }{{"/delivery-quotes", quoteBody}, {"/delivery-estimates", estimateBody}} {
		want := postJSON(NewRouter(&Server{API: api}, nil, ""), route.path, route.body)
		got := postJSON(NewRouter(&Server{API: api, Facts: panickyFacts{}}, nil, ""), route.path, route.body)
		if got.Code != http.StatusOK || got.Code != want.Code || got.Body.String() != want.Body.String() || !sameHeaders(got.Header(), want.Header()) {
			t.Fatalf("%s: a panic in the hand-over changed the response: %d\n%s", route.path, got.Code, got.Body)
		}
	}
}

// The response with the feature off and with it on, over every kind of fact
// writer, is byte for byte the same: status, headers and body.
func TestTheResponseIsByteIdenticalWithTheFactEnabledOrDisabled(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	writers := map[string]requestfacts.Writer{
		"working":   writerFn(func(context.Context, requestfact.Fact) error { return nil }),
		"erroring":  writerFn(func(context.Context, requestfact.Fact) error { return errors.New("outbox down") }),
		"blocking":  writerFn(func(context.Context, requestfact.Fact) error { <-release; return nil }),
		"panicking": writerFn(func(context.Context, requestfact.Fact) error { panic("boom") }),
	}
	api := func() *fakeAPI { return &fakeAPI{estimated: sampleEstimate()} }
	routes := []struct{ name, path, body string }{
		{"quote", "/delivery-quotes", quoteBody},
		{"estimate", "/delivery-estimates", estimateBody},
		{"refused quote", "/delivery-quotes", `{"destinationPostalPrefix":`},
	}
	for _, rt := range routes {
		off := postJSON(NewRouter(&Server{API: api()}, nil, ""), rt.path, rt.body)
		for name, w := range writers {
			q := requestfacts.NewQueue(w, nil, nil, requestfacts.Config{Capacity: 1, FlushInterval: time.Millisecond})
			ctx, stop := context.WithCancel(context.Background())
			go func() { _ = q.Run(ctx) }()
			rec := requestfacts.NewRecorder(q, nil, func() time.Time { return time.Date(2026, 10, 22, 19, 5, 0, 0, time.UTC) })
			h := NewRouter(&Server{API: api(), Facts: rec}, nil, "")
			for i := 0; i < 5; i++ { // more requests than the queue holds: the full queue changes nothing either
				on := postJSON(h, rt.path, rt.body)
				if on.Code != off.Code || on.Body.String() != off.Body.String() || !sameHeaders(on.Header(), off.Header()) {
					t.Fatalf("%s/%s request %d: status %d vs %d\nbody on:  %s\nbody off: %s", rt.name, name, i, on.Code, off.Code, on.Body, off.Body)
				}
			}
			stop()
		}
	}
}

func TestARequestIsNotSlowedByABlockedFactWriter(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	q := requestfacts.NewQueue(writerFn(func(context.Context, requestfact.Fact) error { <-release; return nil }), nil, nil,
		requestfacts.Config{Capacity: 1, FlushInterval: time.Millisecond})
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go func() { _ = q.Run(ctx) }()
	h := NewRouter(&Server{API: &fakeAPI{estimated: sampleEstimate()}, Facts: requestfacts.NewRecorder(q, nil, time.Now)}, nil, "")
	start := time.Now()
	for i := 0; i < 200; i++ {
		if rec := postJSON(h, "/delivery-estimates", estimateBody); rec.Code != http.StatusOK {
			t.Fatalf("status %d", rec.Code)
		}
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("200 requests took %v with the fact writer blocked", elapsed)
	}
}

func TestTheFeatureOffAddsNoWrapper(t *testing.T) {
	s := &Server{}
	inner := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	if got := s.recordsFacts(inner); reflect.ValueOf(got).Pointer() != reflect.ValueOf(inner).Pointer() {
		t.Fatal("with Facts nil the route must be the handler itself")
	}
}

type writerFn func(ctx context.Context, f requestfact.Fact) error

func (w writerFn) Write(ctx context.Context, f requestfact.Fact) error { return w(ctx, f) }

func sameHeaders(a, b http.Header) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if strings.Join(v, ",") != strings.Join(b[k], ",") {
			return false
		}
	}
	return true
}
