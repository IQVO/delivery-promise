package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// fakeAPI records what the handlers pass in and answers with a scripted result.
type fakeAPI struct {
	err       error
	created   bool
	quote     usecases.QuoteCommand
	make      usecases.MakePromiseCommand
	policy    policy.Input
	confirm   [3]any
	cancel    string
	books     [2]string
	orderID   string
	offerID   string
	promiseID string
}

func (f *fakeAPI) QuoteDeliveryOffers(_ context.Context, c usecases.QuoteCommand) (views.QuoteResult, error) {
	f.quote = c
	return views.QuoteResult{Offers: []views.Offer{{OfferID: "off-1", Tier: "NEXT_DAY"}}, Unavailable: []views.Unavailable{}}, f.err
}

func (f *fakeAPI) GetOffer(_ context.Context, id string) (views.Offer, error) {
	f.offerID = id
	return views.Offer{OfferID: id}, f.err
}

func (f *fakeAPI) MakePromise(_ context.Context, c usecases.MakePromiseCommand) (usecases.MakePromiseResult, error) {
	f.make = c
	return usecases.MakePromiseResult{Promise: views.Promise{PromiseID: "prm-1", State: "PROVISIONAL"}, Created: f.created}, f.err
}

func (f *fakeAPI) ListPromisesByOrder(_ context.Context, orderID string) (views.PromiseList, error) {
	f.orderID = orderID
	return views.PromiseList{Items: []views.Promise{}}, f.err
}

func (f *fakeAPI) GetPromise(_ context.Context, id string) (views.Promise, error) {
	f.promiseID = id
	return views.Promise{PromiseID: id}, f.err
}

func (f *fakeAPI) ListPromiseBooks(_ context.Context, node, date string) (views.BookList, error) {
	f.books = [2]string{node, date}
	return views.BookList{Items: []views.Book{}}, f.err
}

func (f *fakeAPI) ConfirmPromise(_ context.Context, id, order string, group int) (views.Promise, error) {
	f.confirm = [3]any{id, order, group}
	return views.Promise{PromiseID: id, State: "CONFIRMED"}, f.err
}

func (f *fakeAPI) CancelPromise(_ context.Context, id string) (views.Promise, error) {
	f.cancel = id
	return views.Promise{PromiseID: id, State: "CANCELLED"}, f.err
}

func (f *fakeAPI) PublishPromisePolicy(_ context.Context, in policy.Input) (views.Policy, error) {
	f.policy = in
	return views.Policy{PolicyVersion: "v1"}, f.err
}

func (f *fakeAPI) CurrentPolicy(context.Context) (views.Policy, error) {
	return views.Policy{PolicyVersion: "v0"}, f.err
}

func do(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestEveryOperationCallsItsUseCaseAndAnswers(t *testing.T) {
	api := &fakeAPI{created: true}
	h := NewRouter(&Server{API: api})

	cases := []struct {
		name, method, path, body string
		status                   int
		wantInBody               string
	}{
		{"quote", "POST", "/delivery-offers:quote", `{"destinationPostalPrefix":"787","lines":[{"catalogItemId":"ci-1","quantity":2}],"tiers":["NEXT_DAY"]}`, 200, `"offerId":"off-1"`},
		{"get offer", "GET", "/delivery-offers/off-9", ``, 200, `"offerId":"off-9"`},
		{"make promise", "POST", "/promises", `{"offerId":"off-1","lines":[{"catalogItemId":"ci-1","quantity":2}]}`, 201, `"promiseId":"prm-1"`},
		{"list by order", "GET", "/promises?orderId=ord-1", ``, 200, `"items":[]`},
		{"get promise", "GET", "/promises/prm-7", ``, 200, `"promiseId":"prm-7"`},
		{"confirm", "POST", "/promises/prm-7:confirm", `{"orderId":"ord-1","groupNo":2}`, 200, `"state":"CONFIRMED"`},
		{"cancel", "POST", "/promises/prm-8:cancel", ``, 200, `"state":"CANCELLED"`},
		{"list books", "GET", "/promise-books?nodeId=N-AUS3&date=2026-10-22", ``, 200, `"items":[]`},
		{"publish policy", "POST", "/promise-policies", `{"offerTtlMinutes":5,"holdTtlMinutes":20,"packagesPerOrder":2,"capacitySharePercent":50,"orderDeadlines":[{"tier":"NEXT_DAY","regionId":"R-SC","localTime":"23:59"}]}`, 201, `"policyVersion":"v1"`},
		{"current policy", "GET", "/promise-policies/current", ``, 200, `"policyVersion":"v0"`},
		{"healthz", "GET", "/healthz", ``, 200, `"ok"`},
		{"readyz", "GET", "/readyz", ``, 200, `"ready"`},
	}
	for _, c := range cases {
		rec := do(h, c.method, c.path, c.body)
		if rec.Code != c.status || !strings.Contains(rec.Body.String(), c.wantInBody) {
			t.Errorf("%s: %d %s", c.name, rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: content type %q", c.name, ct)
		}
	}
	assertRecordedCalls(t, api)
}

// assertRecordedCalls checks that every operation handed its use case the
// decoded request.
func assertRecordedCalls(t *testing.T, api *fakeAPI) {
	t.Helper()
	if api.quote.DestinationPostalPrefix != "787" || api.quote.Lines[0].Quantity != 2 || api.quote.Tiers[0] != shared.TierNextDay {
		t.Errorf("quote command = %+v", api.quote)
	}
	if api.make.OfferID != "off-1" || api.make.Lines[0].CatalogItemID != "ci-1" || api.offerID != "off-9" || api.orderID != "ord-1" || api.promiseID != "prm-7" {
		t.Errorf("recorded = %+v", api)
	}
	if api.cancel != "prm-8" {
		t.Errorf("cancel recorded %q", api.cancel)
	}
	if api.confirm != [3]any{"prm-7", "ord-1", 2} || api.books != [2]string{"N-AUS3", "2026-10-22"} {
		t.Errorf("confirm %v books %v", api.confirm, api.books)
	}
	want := policy.Input{OfferTTLMinutes: 5, HoldTTLMinutes: 20, PackagesPerOrder: 2, SharePercent: 50,
		Deadlines: []policy.Deadline{{Tier: shared.TierNextDay, RegionID: "R-SC", Minute: 23*60 + 59}}}
	if fmt.Sprint(api.policy) != fmt.Sprint(want) {
		t.Errorf("policy input = %+v", api.policy)
	}
}

func TestAReplayedMakePromiseAnswers200(t *testing.T) {
	rec := do(NewRouter(&Server{API: &fakeAPI{}}), "POST", "/promises", `{"offerId":"off-1","lines":[{"catalogItemId":"ci-1","quantity":1}]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
}

func TestEveryDomainErrorIsItsDocumentedProblem(t *testing.T) {
	cases := []struct {
		err    error
		status int
		slug   string
	}{
		{ports.ErrNotFound, 404, "not-found"},
		{book.ErrPromiseCapacityExhausted, 409, "promise-capacity-exhausted"},
		{book.ErrBookClosed, 409, "book-closed"},
		{offer.ErrOfferNotQuotable, 409, "offer-not-quotable"},
		{promise.ErrPromiseHoldExpired, 409, "promise-hold-expired"},
		{promise.ErrPromiseClosed, 409, "promise-closed"},
		{promise.ErrRevisionMovesEarlier, 409, "revision-moves-earlier"},
		{shared.ErrIllegalTransition, 409, "illegal-transition"},
		{ports.ErrConcurrentUpdate, 409, "concurrent-update"},
		{policy.ErrVersionNotNext, 409, "concurrent-update"},
		{promise.ErrGroupSpansNodes, 422, "group-spans-nodes"},
		{promise.ErrInvalidPromiseTerms, 422, "invalid-promise-terms"},
		{promise.ErrNominalBasisNotAllowedForTier, 422, "nominal-basis-not-allowed-for-tier"},
		{offer.ErrNominalBasisNotAllowedForTier, 422, "nominal-basis-not-allowed-for-tier"},
		{shared.Invalid("x"), 422, "validation-failed"},
		{fmt.Errorf("wrapped: %w", book.ErrBookClosed), 409, "book-closed"},
		{errors.New("boom: secret detail"), 500, "internal-error"},
	}
	for _, c := range cases {
		rec := do(NewRouter(&Server{API: &fakeAPI{err: c.err}}), "GET", "/promises/prm-1", "")
		var p problemDetail
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatalf("%v: %v", c.err, err)
		}
		if rec.Code != c.status || p.Status != c.status || p.Type != problemBaseURI+c.slug || rec.Header().Get("Content-Type") != "application/problem+json" || p.Title == "" {
			t.Errorf("%v -> %d %+v", c.err, rec.Code, p)
		}
		if p.Instance != "/promises/prm-1" {
			t.Errorf("instance = %q", p.Instance)
		}
		if c.status == 500 && strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("a 500 leaked the internal message: %s", rec.Body)
		}
	}
}

func TestMalformedBodiesAre400AndNeverReachTheUseCase(t *testing.T) {
	api := &fakeAPI{}
	h := NewRouter(&Server{API: api})
	for name, c := range map[string][2]string{
		"bad json":      {"/promises", `{"offerId":`},
		"unknown field": {"/promises", `{"offerId":"o","lines":[],"extra":1}`},
		"trailing data": {"/promises", `{"offerId":"o","lines":[]} {}`},
		"empty body":    {"/promises", ``},
		"wrong type":    {"/delivery-offers:quote", `{"destinationPostalPrefix":787}`},
		"confirm":       {"/promises/prm-1:confirm", `[]`},
	} {
		rec := do(h, "POST", c[0], c[1])
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), "malformed-request") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
	if api.make.OfferID != "" || api.confirm[0] != nil {
		t.Fatal("a malformed request reached a use case")
	}
	rec := do(h, "POST", "/promise-policies", `{"orderDeadlines":[{"tier":"NEXT_DAY","localTime":"9:5"}]}`)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "validation-failed") {
		t.Errorf("bad localTime: %d %s", rec.Code, rec.Body)
	}
}

func TestOmittedPolicyValuesTakeTheDefaults(t *testing.T) {
	api := &fakeAPI{}
	if rec := do(NewRouter(&Server{API: api}), "POST", "/promise-policies", `{}`); rec.Code != 201 {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	d := policy.Default()
	if api.policy.OfferTTLMinutes != int(d.OfferTTL().Minutes()) || api.policy.HoldTTLMinutes != int(d.HoldTTL().Minutes()) ||
		api.policy.PackagesPerOrder != d.PackagesPerOrder() || api.policy.SharePercent != d.SharePercent() {
		t.Fatalf("input = %+v", api.policy)
	}
}

func TestParseLocalTime(t *testing.T) {
	for in, want := range map[string]int{"00:00": 0, "09:05": 545, "23:59": 1439} {
		if got, err := parseLocalTime(in); err != nil || got != want {
			t.Errorf("%s -> %d %v", in, got, err)
		}
	}
	for _, in := range []string{"", "9:05", "09:5", "24:00", "12:60", "ab:cd", "1200", "-1:00", "12:-1"} {
		if _, err := parseLocalTime(in); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%q accepted: %v", in, err)
		}
	}
}

func TestOnlyTheMutatingRoutesAreWrappedByTheIdempotencyMiddleware(t *testing.T) {
	var wrapped []string
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			wrapped = append(wrapped, r.Method+" "+r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
	h := NewRouter(&Server{API: &fakeAPI{}, Idempotency: mw})
	do(h, "POST", "/delivery-offers:quote", `{"destinationPostalPrefix":"787","lines":[]}`)
	do(h, "POST", "/promises", `{"offerId":"o","lines":[]}`)
	do(h, "POST", "/promises/prm-1:confirm", `{"orderId":"o","groupNo":1}`)
	do(h, "POST", "/promises/prm-1:cancel", ``)
	do(h, "POST", "/promise-policies", `{}`)
	do(h, "GET", "/promises/prm-1", ``)
	want := "POST /promises,POST /promises/prm-1:confirm,POST /promises/prm-1:cancel,POST /promise-policies"
	if got := strings.Join(wrapped, ","); got != want {
		t.Fatalf("wrapped = %s, want %s", got, want)
	}
}

func TestNULBytesAreRejectedBeforeAnyUseCase(t *testing.T) {
	api := &fakeAPI{}
	h := NewRouter(&Server{API: api})
	if rec := do(h, "GET", "/promises/prm%00x", ``); rec.Code != 404 {
		t.Errorf("NUL in path: %d", rec.Code)
	}
	if rec := do(h, "GET", "/promises?orderId=a%00b", ``); rec.Code != 400 {
		t.Errorf("NUL in query: %d", rec.Code)
	}
	if rec := do(h, "POST", "/promises", `{"offerId":"a\u0000b","lines":[]}`); rec.Code != 400 {
		t.Errorf("NUL in body: %d", rec.Code)
	}
	if api.promiseID != "" || api.orderID != "" || api.make.OfferID != "" {
		t.Fatal("a NUL request reached a use case")
	}
}

func TestUnknownRoutesAndMethodsAreNotFoundOrNotAllowed(t *testing.T) {
	h := NewRouter(&Server{API: &fakeAPI{}})
	if rec := do(h, "GET", "/nope", ``); rec.Code != 404 {
		t.Errorf("unknown route: %d", rec.Code)
	}
	if rec := do(h, "DELETE", "/promises/prm-1", ``); rec.Code != 405 {
		t.Errorf("wrong method: %d", rec.Code)
	}
}

func TestTheQuoteIsKeyedOnlyWhenTheCallerSendsAKey(t *testing.T) {
	var keyed int
	mw := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { keyed++; next.ServeHTTP(w, r) })
	}
	h := NewRouter(&Server{API: &fakeAPI{}, Idempotency: mw})
	body := `{"destinationPostalPrefix":"787","lines":[{"catalogItemId":"ci-1","quantity":1}]}`
	if rec := do(h, "POST", "/delivery-offers:quote", body); rec.Code != 200 || keyed != 0 {
		t.Fatalf("no key: %d, middleware ran %d times", rec.Code, keyed)
	}
	req := httptest.NewRequest("POST", "/delivery-offers:quote", strings.NewReader(body))
	req.Header.Set(IdempotencyKeyHeader, "k1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || keyed != 1 {
		t.Fatalf("with a key: %d, middleware ran %d times", rec.Code, keyed)
	}
}
