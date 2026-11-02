package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The unit tests of the transactional Idempotency-Key middleware's in-memory
// half: capturing the handler's answer, replaying it to the client, restoring
// the stored headers, naming the route and settling the savepoint. The parts
// that need a real pool (claiming the key, replaying a stored row, the wrapped
// request) are proven against Postgres by idempotency_integration_test.go.

// fakeTx is a pgx.Tx that records Begin/Commit/Rollback and every statement,
// so a unit test can prove which transaction the middleware runs and what it
// persists, without a database.
type fakeTx struct {
	begins, commits, rollbacks int
	execSQL                    []string
	execArgs                   [][]any
}

func (f *fakeTx) Begin(context.Context) (pgx.Tx, error) { f.begins++; return f, nil }
func (f *fakeTx) Commit(context.Context) error          { f.commits++; return nil }
func (f *fakeTx) Rollback(context.Context) error        { f.rollbacks++; return nil }
func (f *fakeTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	f.execSQL = append(f.execSQL, sql)
	f.execArgs = append(f.execArgs, args)
	return pgconn.NewCommandTag("INSERT 0 1"), nil
}
func (f *fakeTx) Query(context.Context, string, ...any) (pgx.Rows, error) {
	panic("not needed by the unit tests")
}
func (f *fakeTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	f.execSQL = append(f.execSQL, sql)
	f.execArgs = append(f.execArgs, args)
	return pgxRow{}
}
func (f *fakeTx) CopyFrom(context.Context, pgx.Identifier, []string, pgx.CopyFromSource) (int64, error) {
	return 0, nil
}
func (f *fakeTx) SendBatch(context.Context, *pgx.Batch) pgx.BatchResults { return nil }
func (f *fakeTx) LargeObjects() pgx.LargeObjects                         { return pgx.LargeObjects{} }
func (f *fakeTx) Prepare(context.Context, string, string) (*pgconn.StatementDescription, error) {
	return nil, nil
}
func (f *fakeTx) Conn() *pgx.Conn { return nil }

// pgxRow answers nothing: the unit tests never read a row through it.
type pgxRow struct{}

func (pgxRow) Scan(...any) error { return errors.New("the unit-test tx has no rows") }

func TestCaptureRecordsTheHandlersAnswer(t *testing.T) {
	rec := &capture{header: http.Header{}}
	rec.Header().Set("Content-Type", "application/json")
	rec.WriteHeader(http.StatusCreated)
	rec.WriteHeader(http.StatusInternalServerError) // the first code wins
	_, _ = rec.Write([]byte(`{"a":1}`))
	if rec.code != http.StatusCreated || rec.body.String() != `{"a":1}` || rec.header.Get("Content-Type") != "application/json" {
		t.Fatalf("captured %+v %q", rec.code, rec.body.String())
	}

	bare := &capture{header: http.Header{}}
	_, _ = bare.Write([]byte("x"))
	if bare.code != http.StatusOK {
		t.Fatalf("a write without WriteHeader recorded %d, want 200", bare.code)
	}
}

func TestFlushCopiesTheCapturedAnswer(t *testing.T) {
	rec := &capture{header: http.Header{}}
	rec.Header().Add("X-Answer", "one")
	rec.Header().Add("X-Answer", "two")
	rec.WriteHeader(http.StatusConflict)
	_, _ = rec.Write([]byte("no room"))

	out := httptest.NewRecorder()
	flush(out, rec)
	if out.Code != http.StatusConflict || out.Body.String() != "no room" {
		t.Fatalf("flush wrote %d %q", out.Code, out.Body.String())
	}
	if got := out.Header().Values("X-Answer"); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Fatalf("flush copied headers %v", got)
	}
}

func TestCopyStoredHeaders(t *testing.T) {
	raw, err := json.Marshal(http.Header{"Idempotency-Replayed": []string{"true"}, "Content-Type": []string{"application/json"}})
	if err != nil {
		t.Fatal(err)
	}
	dst := http.Header{}
	copyStoredHeaders(dst, raw)
	if dst.Get("Idempotency-Replayed") != "true" || dst.Get("Content-Type") != "application/json" {
		t.Fatalf("stored headers not restored: %v", dst)
	}

	for name, b := range map[string][]byte{"empty": {}, "not json": []byte("nope")} {
		empty := http.Header{}
		copyStoredHeaders(empty, b)
		if len(empty) != 0 {
			t.Errorf("%s: copyStoredHeaders wrote %v", name, empty)
		}
	}
}

func TestValidKeyAcceptsOneTo128PrintableASCIICharacters(t *testing.T) {
	for key, want := range map[string]bool{
		"":                       false,
		"k":                      true,
		strings.Repeat("a", 128): true,
		strings.Repeat("a", 129): false,
		"tab\tkey":               false,
		"caf\u00e9":              false,
		"9b2f6c1e-uuid-like-key": true,
	} {
		if got := validKey(key); got != want {
			t.Errorf("validKey(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestSettleSavepointKeeps2xxAndDiscards4xx(t *testing.T) {
	ctx := context.Background()
	for name, c := range map[string]struct {
		code      int
		commits   int
		rollbacks int
	}{
		"created":     {http.StatusCreated, 1, 0},
		"ok":          {http.StatusOK, 1, 0},
		"bad request": {http.StatusBadRequest, 0, 1},
		"conflict":    {http.StatusConflict, 0, 1},
		"invalid":     {http.StatusUnprocessableEntity, 0, 1},
	} {
		tx := &fakeTx{}
		if err := settleSavepoint(ctx, tx, c.code); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if tx.commits != c.commits || tx.rollbacks != c.rollbacks {
			t.Errorf("%s: %d commits %d rollbacks, want %d and %d", name, tx.commits, tx.rollbacks, c.commits, c.rollbacks)
		}
	}
}

func TestStoreOutcomeWritesTheAnswerIntoTheTransaction(t *testing.T) {
	rec := &capture{header: http.Header{}}
	rec.Header().Set("Location", "/promises/prm-1")
	rec.WriteHeader(http.StatusCreated)
	_, _ = rec.Write([]byte(`{"promiseId":"prm-1"}`))

	tx := &fakeTx{}
	if err := storeOutcome(context.Background(), tx, "key-1", rec); err != nil {
		t.Fatal(err)
	}
	if len(tx.execSQL) != 1 || !strings.Contains(tx.execSQL[0], "UPDATE idempotency_keys") {
		t.Fatalf("storeOutcome ran %v", tx.execSQL)
	}
	args := tx.execArgs[0]
	if args[0] != http.StatusCreated || string(args[1].([]byte)) != `{"promiseId":"prm-1"}` ||
		args[3] != "key-1" {
		t.Fatalf("storeOutcome bound %v", args)
	}
	var stored http.Header
	if err := json.Unmarshal(args[2].([]byte), &stored); err != nil || stored.Get("Location") != "/promises/prm-1" {
		t.Fatalf("stored headers = %s (%v)", args[2], err)
	}
}

func TestErrorString(t *testing.T) {
	if got := errorString("x").Error(); got != "x" {
		t.Fatalf("Error() = %q", got)
	}
}
