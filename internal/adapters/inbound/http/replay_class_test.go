package http

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/claudioed/delivery-promise/internal/application/ports"
)

// The replay classes (ADR 0031): every problem type declares what a retry under
// the same Idempotency-Key gets, and the middleware acts on the class and on
// nothing else. These tests need no database: the middleware's choice is driven
// over a spy transaction, the Postgres half is idempotency_replay_classes_integration_test.go.

func TestEveryCatalogueEntryDeclaresAReplayClass(t *testing.T) {
	if len(problemCatalogue) == 0 {
		t.Fatal("the catalogue is empty: the test would pass on nothing")
	}
	classOf := map[string]replayClass{}
	for _, e := range problemCatalogue {
		t.Run(fmt.Sprintf("%s/%d", e.p.slug, e.p.status), func(t *testing.T) {
			if !e.replay.declared() {
				t.Errorf("problem type %q (%d) declares no replay class (%s): set FINAL, OUTCOME or RETRYABLE on its catalogue entry (.claude/rules/fleet/idempotency-and-outbox.md)", e.p.slug, e.p.status, e.replay)
			}
			if e.p.status >= http.StatusInternalServerError && e.replay != replayRetryable {
				t.Errorf("problem type %q answers %d: a 5xx is never stored, so its class is RETRYABLE, got %s", e.p.slug, e.p.status, e.replay)
			}
			if prev, seen := classOf[e.p.slug]; seen && prev != e.replay {
				t.Errorf("slug %q is declared %s and %s: one problem type has exactly one class", e.p.slug, prev, e.replay)
			}
			classOf[e.p.slug] = e.replay
		})
	}
}

func TestTheReplayClassCheckBites(t *testing.T) {
	for _, c := range []replayClass{replayUnset, replayClass(4), replayClass(255)} {
		if c.declared() {
			t.Errorf("class %d is accepted; only FINAL, OUTCOME and RETRYABLE are classes", c)
		}
	}
	for c, name := range map[replayClass]string{replayFinal: "FINAL", replayOutcome: "OUTCOME", replayRetryable: "RETRYABLE"} {
		if !c.declared() || c.String() != name {
			t.Errorf("class %d = %s declared=%v, want %s declared", c, c, c.declared(), name)
		}
	}
	if replayUnset.String() != "UNSET" {
		t.Errorf("the zero class reads %q, want UNSET", replayUnset)
	}
}

// writeError is the one production error writer: it must tell the capturing
// writer the class of the entry it answers with, and nothing for a 5xx.
func TestWriteErrorMarksTheCaptureWithTheClassOfItsEntry(t *testing.T) {
	for _, e := range problemCatalogue {
		rec := &capture{header: http.Header{}}
		writeError(rec, httptest.NewRequest(http.MethodPost, "/promises", nil), fmt.Errorf("%w: detail", e.err))
		if rec.class != e.replay || rec.code != e.p.status {
			t.Errorf("%s: capture holds %s/%d, want %s/%d", e.p.slug, rec.class, rec.code, e.replay, e.p.status)
		}
	}
	rec := &capture{header: http.Header{}}
	writeError(rec, httptest.NewRequest(http.MethodPost, "/promises", nil), errors.New("disk on fire"))
	if rec.class != replayUnset || rec.code != http.StatusInternalServerError {
		t.Errorf("an unmapped error is %s/%d, want UNSET/500", rec.class, rec.code)
	}
	plain := httptest.NewRecorder() // a writer that is not the capture: no marking, no panic
	writeError(plain, httptest.NewRequest(http.MethodGet, "/promises", nil), ports.ErrNotFound)
	if plain.Code != http.StatusNotFound {
		t.Errorf("a plain writer answered %d, want 404", plain.Code)
	}
}

func TestTheVerdictReadsTheStatusFamilyAndTheClassOnly(t *testing.T) {
	for name, c := range map[string]struct {
		code  int
		class replayClass
		want  verdict
	}{
		"200 is stored with its writes":          {http.StatusOK, replayUnset, storeAndKeep},
		"201 is stored with its writes":          {http.StatusCreated, replayUnset, storeAndKeep},
		"a FINAL 409 is stored alone":            {http.StatusConflict, replayFinal, storeRefusal},
		"a FINAL 422 is stored alone":            {http.StatusUnprocessableEntity, replayFinal, storeRefusal},
		"an OUTCOME 409 keeps its writes":        {http.StatusConflict, replayOutcome, storeAndKeep},
		"a RETRYABLE 409 releases the key":       {http.StatusConflict, replayRetryable, releaseKey},
		"a RETRYABLE 404 releases the key":       {http.StatusNotFound, replayRetryable, releaseKey},
		"an unclassified 4xx releases the key":   {http.StatusConflict, replayUnset, releaseKey},
		"a 500 is never stored":                  {http.StatusInternalServerError, replayUnset, releaseKey},
		"a 503 is never stored":                  {http.StatusServiceUnavailable, replayUnset, releaseKey},
		"a 5xx marked FINAL is never stored":     {http.StatusInternalServerError, replayFinal, releaseKey},
		"a 5xx marked OUTCOME is never stored":   {http.StatusBadGateway, replayOutcome, releaseKey},
		"a 3xx has no class, so the key is free": {http.StatusFound, replayUnset, releaseKey},
	} {
		rec := &capture{header: http.Header{}, code: c.code, class: c.class}
		if got := rec.verdict(); got != c.want {
			t.Errorf("%s: verdict %d, want %d", name, got, c.want)
		}
	}
}

// spyTx is a transaction whose savepoint is a second spy, so a test can tell what
// the middleware did to the request's transaction from what it did to the handler's.
type spyTx struct {
	fakeTx
	sp *fakeTx
}

func newSpyTx() *spyTx { return &spyTx{sp: &fakeTx{}} }

func (s *spyTx) Begin(context.Context) (pgx.Tx, error) { s.begins++; return s.sp, nil }

func (s *spyTx) stored() bool {
	for _, q := range s.execSQL {
		if strings.Contains(q, "UPDATE idempotency_keys") {
			return true
		}
	}
	return false
}

// runHandler drives runFreshRequest with a handler that answers through the
// production error writer, so the class marking is the one production runs.
func runHandler(t *testing.T, answer func(w http.ResponseWriter, r *http.Request)) (*spyTx, *httptest.ResponseRecorder) {
	t.Helper()
	tx := newSpyTx()
	out := httptest.NewRecorder()
	bind := func(ctx context.Context, _ pgx.Tx) context.Context { return ctx }
	runFreshRequest(context.Background(), out, httptest.NewRequest(http.MethodPost, "/promises", nil), tx, bind, "k", http.HandlerFunc(answer))
	return tx, out
}

func refuseWith(err error) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) { writeError(w, r, err) }
}

func TestTheMiddlewareActsOnTheClassOfTheRefusal(t *testing.T) {
	for name, c := range map[string]struct {
		answer                                   func(http.ResponseWriter, *http.Request)
		code                                     int
		stored                                   bool
		txCommits, txRollbacks, spCommit, spRoll int
	}{
		"FINAL: the writes roll back, the refusal is stored":   {refuseWith(errMalformedRequest), 400, true, 1, 0, 0, 1},
		"RETRYABLE: everything rolls back, nothing is stored":  {refuseWith(ports.ErrConcurrentUpdate), 409, false, 0, 1, 0, 0},
		"RETRYABLE 404: everything rolls back, nothing stored": {refuseWith(ports.ErrNotFound), 404, false, 0, 1, 0, 0},
		"2xx: the writes commit with the stored answer": {func(w http.ResponseWriter, _ *http.Request) {
			writeJSON(w, http.StatusCreated, map[string]string{"id": "x"})
		}, 201, true, 1, 0, 1, 0},
		"5xx: everything rolls back, nothing is stored": {refuseWith(errors.New("boom")), 500, false, 0, 1, 0, 0},
		"OUTCOME: the writes commit with the stored refusal": {func(w http.ResponseWriter, r *http.Request) {
			w.(replayMarker).MarkReplay(replayOutcome)
			writeProblem(w, r, problem{http.StatusConflict, "recorded-then-refused", "Recorded then refused"}, "the command committed and then refused")
		}, 409, true, 1, 0, 1, 0},
	} {
		tx, out := runHandler(t, c.answer)
		if out.Code != c.code {
			t.Errorf("%s: answered %d, want %d", name, out.Code, c.code)
		}
		if tx.stored() != c.stored {
			t.Errorf("%s: answer stored = %v, want %v", name, tx.stored(), c.stored)
		}
		if tx.commits != c.txCommits || tx.rollbacks != c.txRollbacks || tx.sp.commits != c.spCommit || tx.sp.rollbacks != c.spRoll {
			t.Errorf("%s: transaction %d commits %d rollbacks, savepoint %d commits %d rollbacks; want %d/%d and %d/%d",
				name, tx.commits, tx.rollbacks, tx.sp.commits, tx.sp.rollbacks, c.txCommits, c.txRollbacks, c.spCommit, c.spRoll)
		}
	}
}

func TestEveryEntryIsReleasedOrStoredAsItsClassSays(t *testing.T) {
	for _, e := range problemCatalogue {
		tx, out := runHandler(t, refuseWith(fmt.Errorf("%w: x", e.err)))
		wantStored := e.replay == replayFinal || e.replay == replayOutcome
		if out.Code != e.p.status || tx.stored() != wantStored {
			t.Errorf("%s (%s): answered %d stored=%v, want %d stored=%v", e.p.slug, e.replay, out.Code, tx.stored(), e.p.status, wantStored)
		}
		if released := tx.rollbacks == 1 && tx.commits == 0; released == wantStored {
			t.Errorf("%s (%s): transaction %d commits %d rollbacks, released=%v", e.p.slug, e.replay, tx.commits, tx.rollbacks, released)
		}
	}
}

func TestSettleSavepointKeepsTheWritesOnlyWhenTheVerdictSaysSo(t *testing.T) {
	for name, c := range map[string]struct {
		v         verdict
		commits   int
		rollbacks int
	}{
		"stored with its writes": {storeAndKeep, 1, 0},
		"stored alone":           {storeRefusal, 0, 1},
		"released":               {releaseKey, 0, 1},
	} {
		tx := &fakeTx{}
		if err := settleSavepoint(context.Background(), tx, c.v); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if tx.commits != c.commits || tx.rollbacks != c.rollbacks {
			t.Errorf("%s: %d commits %d rollbacks, want %d and %d", name, tx.commits, tx.rollbacks, c.commits, c.rollbacks)
		}
	}
}

// ---- the records that must say what the code does ----------------------------------------------------

var adapterRow = regexp.MustCompile("(?m)^\\| `([a-z0-9-]+)` \\| (FINAL|OUTCOME|RETRYABLE) \\|")

func repoFile(t *testing.T, rel ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(append([]string{"..", "..", "..", ".."}, rel...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func catalogueClasses() map[string]string {
	out := map[string]string{}
	for _, e := range problemCatalogue {
		out[e.p.slug] = e.replay.String()
	}
	return out
}

func TestTheADRDeclaresTheClassOfEveryProblem(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join("..", "..", "..", "..", "docs", "adr", "0031-*.md"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("docs/adr/0031-*.md = %v (%v), want exactly one ADR", matches, err)
	}
	raw, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	declared := map[string]string{}
	for _, m := range adapterRow.FindAllStringSubmatch(string(raw), -1) {
		if prev, dup := declared[m[1]]; dup {
			t.Errorf("the ADR lists %q twice (%s and %s)", m[1], prev, m[2])
		}
		declared[m[1]] = m[2]
	}
	want := catalogueClasses()
	for slug, class := range want {
		if declared[slug] != class {
			t.Errorf("ADR 0031 says %q is %q, the catalogue says %s", slug, declared[slug], class)
		}
	}
	for slug := range declared {
		if _, ok := want[slug]; !ok {
			t.Errorf("ADR 0031 classifies %q, which is not in the catalogue", slug)
		}
	}
}

var retryableSentence = regexp.MustCompile(`release the key \(RETRYABLE\): ([a-z0-9, -]+)\.`)

func TestTheSpecListsTheRetryableProblemTypes(t *testing.T) {
	spec := strings.Join(strings.Fields(repoFile(t, "apis", "openapi.yaml")), " ")
	m := retryableSentence.FindStringSubmatch(spec)
	if m == nil {
		t.Fatal("apis/openapi.yaml does not name the problem types that release the key: \"... release the key (RETRYABLE): a, b, c.\"")
	}
	var want []string
	for slug, class := range catalogueClasses() {
		if class == "RETRYABLE" {
			want = append(want, slug)
		}
	}
	sort.Strings(want)
	got := strings.Split(m[1], ", ")
	sort.Strings(got)
	if !slices.Equal(got, want) {
		t.Errorf("the spec lists %v as RETRYABLE, the catalogue declares %v", got, want)
	}
	if strings.Contains(spec, "24 hours with the Idempotency-Replayed header. The one exception") {
		t.Error("the spec still says every repeat replays the first response: a RETRYABLE refusal is not replayed")
	}
	if !strings.Contains(spec, "when that response is final") {
		t.Error("the spec does not say that only a final response is replayed")
	}
}
