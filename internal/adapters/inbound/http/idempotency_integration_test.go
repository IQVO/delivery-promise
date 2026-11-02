//go:build integration

package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres/pgtx"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// idemRig is the middleware over a real database. The handler writes one
// processed_events row through the unit-of-work transaction the middleware
// bound, then answers with the status the test script asks for.
type idemRig struct {
	t      *testing.T
	pool   *pgtest.Pool
	router http.Handler
	calls  atomic.Int32
	status atomic.Int32
	panics atomic.Bool
}

func newIdemRig(t *testing.T) *idemRig {
	t.Helper()
	rig := &idemRig{t: t, pool: pgtest.NewPool(t)}
	rig.status.Store(http.StatusCreated)
	mw := RequireIdempotencyKey(rig.pool, pgtx.With)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := rig.calls.Add(1)
		tx, ok := pgtx.From(r.Context())
		if !ok {
			t.Error("the middleware did not bind a transaction")
			return
		}
		if _, err := tx.Exec(r.Context(), `INSERT INTO processed_events (event_id, event_type, processed_at) VALUES ($1, 'test', now())`,
			r.URL.Path+"#"+string(rune('a'+n)), // unique per call
		); err != nil {
			t.Errorf("handler write: %v", err)
		}
		if rig.panics.Load() {
			panic("handler blew up")
		}
		writeJSON(w, int(rig.status.Load()), map[string]int{"call": int(n)})
	})
	r := chi.NewRouter()
	r.Method(http.MethodPost, "/things/{id}", mw(handler))
	r.Method(http.MethodPost, "/other", mw(handler))
	rig.router = Recoverer(r)
	return rig
}

func (g *idemRig) post(path, key, body string) *httptest.ResponseRecorder {
	g.t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	if key != "" {
		req.Header.Set(IdempotencyKeyHeader, key)
	}
	rec := httptest.NewRecorder()
	g.router.ServeHTTP(rec, req)
	return rec
}

func (g *idemRig) rows(table string) int {
	g.t.Helper()
	var n int
	if err := g.pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		g.t.Fatal(err)
	}
	return n
}

func TestAKeyIsRequired(t *testing.T) {
	g := newIdemRig(t)
	rec := g.post("/things/1", "", `{}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "idempotency-key-required") || g.calls.Load() != 0 {
		t.Fatalf("%d %s calls %d", rec.Code, rec.Body, g.calls.Load())
	}
}

func TestTheSameKeyAndBodyReplaysTheFirstAnswer(t *testing.T) {
	g := newIdemRig(t)
	first := g.post("/things/1", "k1", `{"a":1}`)
	second := g.post("/things/1", "k1", `{"a":1}`)
	if first.Code != http.StatusCreated || second.Code != first.Code || second.Body.String() != first.Body.String() {
		t.Fatalf("first %d %s, replay %d %s", first.Code, first.Body, second.Code, second.Body)
	}
	if second.Header().Get(IdempotencyReplayedHeader) != "true" || first.Header().Get(IdempotencyReplayedHeader) != "" {
		t.Fatalf("replay header: first %q second %q", first.Header().Get(IdempotencyReplayedHeader), second.Header().Get(IdempotencyReplayedHeader))
	}
	if g.calls.Load() != 1 || g.rows("processed_events") != 1 || g.rows("idempotency_keys") != 1 {
		t.Fatalf("calls %d events %d keys %d", g.calls.Load(), g.rows("processed_events"), g.rows("idempotency_keys"))
	}
}

func TestADifferentBodyUnderTheSameKeyIsRefused(t *testing.T) {
	g := newIdemRig(t)
	g.post("/things/1", "k1", `{"a":1}`)
	rec := g.post("/things/1", "k1", `{"a":2}`)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "idempotency-key-reused") || g.calls.Load() != 1 {
		t.Fatalf("%d %s calls %d", rec.Code, rec.Body, g.calls.Load())
	}
}

func TestTheKeyIsGlobalSoAnotherPathIsAReuse(t *testing.T) {
	g := newIdemRig(t)
	g.post("/things/1", "shared", `{}`)
	other := g.post("/other", "shared", `{}`)
	samePattern := g.post("/things/2", "shared", `{}`) // same chi pattern, another concrete path
	for name, rec := range map[string]*httptest.ResponseRecorder{"other route": other, "other path": samePattern} {
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "idempotency-key-reused") ||
			rec.Header().Get(IdempotencyReplayedHeader) != "" {
			t.Fatalf("%s: %d %s, want 409 idempotency-key-reused", name, rec.Code, rec.Body)
		}
	}
	if g.calls.Load() != 1 || g.rows("idempotency_keys") != 1 {
		t.Fatalf("calls %d keys %d", g.calls.Load(), g.rows("idempotency_keys"))
	}
}

func TestAMalformedKeyIsRefusedBeforeTheHandler(t *testing.T) {
	g := newIdemRig(t)
	rec := g.post("/things/1", strings.Repeat("k", 129), `{}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "idempotency-key-invalid") || g.calls.Load() != 0 {
		t.Fatalf("%d %s calls %d", rec.Code, rec.Body, g.calls.Load())
	}
}

func TestARefusalIsStoredButItsWritesAreRolledBack(t *testing.T) {
	g := newIdemRig(t)
	g.status.Store(http.StatusConflict)
	first := g.post("/things/1", "k1", `{}`)
	g.status.Store(http.StatusCreated)
	replay := g.post("/things/1", "k1", `{}`)
	if first.Code != http.StatusConflict || replay.Code != http.StatusConflict || replay.Header().Get(IdempotencyReplayedHeader) != "true" {
		t.Fatalf("first %d replay %d", first.Code, replay.Code)
	}
	if g.rows("processed_events") != 0 || g.rows("idempotency_keys") != 1 || g.calls.Load() != 1 {
		t.Fatalf("a refused request left %d writes, keys %d, calls %d", g.rows("processed_events"), g.rows("idempotency_keys"), g.calls.Load())
	}
}

func TestAServerFailureIsNeverStoredAndTheKeyCanRetry(t *testing.T) {
	g := newIdemRig(t)
	g.status.Store(http.StatusInternalServerError)
	if rec := g.post("/things/1", "k1", `{}`); rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	if g.rows("idempotency_keys") != 0 || g.rows("processed_events") != 0 {
		t.Fatal("a 5xx left rows behind")
	}
	g.status.Store(http.StatusCreated)
	if rec := g.post("/things/1", "k1", `{}`); rec.Code != http.StatusCreated || rec.Header().Get(IdempotencyReplayedHeader) != "" {
		t.Fatalf("retry %d", rec.Code)
	}
	if g.rows("processed_events") != 1 {
		t.Fatalf("events = %d", g.rows("processed_events"))
	}
}

func TestAPanicRollsEverythingBackAndAnswersAProblem(t *testing.T) {
	g := newIdemRig(t)
	g.panics.Store(true)
	rec := g.post("/things/1", "k1", `{}`)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "internal-error") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if g.rows("idempotency_keys") != 0 || g.rows("processed_events") != 0 {
		t.Fatal("a panic left rows behind")
	}
	g.panics.Store(false)
	if rec := g.post("/things/1", "k1", `{}`); rec.Code != http.StatusCreated {
		t.Fatalf("retry %d", rec.Code)
	}
}

func TestExpiredKeysArePurgedOnTheNextKeyedWrite(t *testing.T) {
	g := newIdemRig(t)
	if _, err := g.pool.Exec(context.Background(), `INSERT INTO idempotency_keys (route, key, request_hash, status_code, created_at) VALUES ('POST /old', 'old', 'h', 201, now() - interval '25 hours')`); err != nil {
		t.Fatal(err)
	}
	if _, err := g.pool.Exec(context.Background(), `INSERT INTO idempotency_keys (route, key, request_hash, status_code, created_at) VALUES ('POST /recent', 'recent', 'h', 201, now() - interval '23 hours')`); err != nil {
		t.Fatal(err)
	}
	g.post("/things/1", "k1", `{}`)
	if n := g.rows("idempotency_keys"); n != 2 {
		t.Fatalf("keys = %d, want the recent one and the new one", n)
	}
}

func TestConcurrentRequestsWithOneKeyRunTheHandlerOnce(t *testing.T) {
	g := newIdemRig(t)
	var wg sync.WaitGroup
	codes := make([]int, 8)
	for i := range codes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes[i] = g.post("/things/1", "same", `{}`).Code
		}()
	}
	wg.Wait()
	for _, c := range codes {
		if c != http.StatusCreated {
			t.Fatalf("codes = %v", codes)
		}
	}
	if g.calls.Load() != 1 || g.rows("processed_events") != 1 {
		t.Fatalf("handler ran %d times, %d writes", g.calls.Load(), g.rows("processed_events"))
	}
}
