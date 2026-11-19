package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IdempotencyKeyHeader is the request header a caller must supply on a route
// wrapped by RequireIdempotencyKey.
const IdempotencyKeyHeader = "Idempotency-Key"

// TxBinder returns a child context carrying tx, so the use case's own unit of
// work (the Postgres adapter's) JOINS the transaction the middleware opened
// instead of beginning a second one. cmd/ passes the outbound adapter's
// binder in: adapters never import each other.
type TxBinder func(ctx context.Context, tx pgx.Tx) context.Context

// IdempotencyReplayedHeader marks a response replayed from the stored outcome.
const IdempotencyReplayedHeader = "Idempotency-Replayed"

// keyRetention is how long a stored outcome is kept; older rows are purged on
// the next keyed write.
const keyRetention = "24 hours"

// RequireIdempotencyKey is the fleet's transactional Idempotency-Key
// middleware, applied route by route to every POST. The key is GLOBAL: the
// stored row is found by key alone and compared by fingerprint, the method
// plus the concrete path (route) and the SHA-256 of the body (requestHash).
// The same key on another operation is therefore a reuse, not a new key.
//
//   - idempotency_keys.status_code starts NULL and is set by the SAME
//     transaction that inserted the row, immediately before it commits. A
//     committed row therefore always carries its outcome: there is no "in
//     progress" state, no timeout and no retry-later answer.
//   - Two concurrent requests with the same key race on INSERT ... ON CONFLICT
//     DO NOTHING. Postgres serialises them on the primary-key unique index:
//     the second statement blocks until the first transaction resolves, so a
//     reader that sees "0 rows inserted" can rely on the original transaction
//     being finished. If it rolled back, the blocked insert succeeds instead.
//   - The wrapped handler runs in a SAVEPOINT of that transaction, with the
//     savepoint bound into its context (TxBinder), so the idempotency row, the
//     aggregates and their outbox rows commit or roll back together. What
//     happens to the answer is its replay class (ADR 0031), declared on the
//     catalogue entry of its problem type and marked on the capture by
//     writeError: a 2xx keeps the handler's writes and is stored; a FINAL
//     refusal rolls the savepoint back (a refused request leaves no trace) and
//     its problem is stored and replayed; an OUTCOME refusal commits the
//     handler's writes with the stored problem; a RETRYABLE refusal, a refusal
//     with no class, a 5xx and a panic roll everything back, key row included,
//     and are never stored, so the same key runs the real work again. A request
//     waiting on the key meanwhile finds no row and runs the handler itself.
//   - A missing key is 400 idempotency-key-required; a key over 128
//     characters or with non-printable ASCII is 400 idempotency-key-invalid.
//   - A different method, path or body under a stored key is 409
//     idempotency-key-reused; under a released key it is a new request.
func RequireIdempotencyKey(pool *pgxpool.Pool, bind TxBinder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := r.Header.Get(IdempotencyKeyHeader)
			if key == "" {
				writeProblem(w, r, idempotencyKeyRequired,
					"send an Idempotency-Key header with a unique value per logical request")
				return
			}
			if !validKey(key) {
				writeProblem(w, r, idempotencyKeyInvalid,
					"the Idempotency-Key must be 1-128 printable ASCII characters")
				return
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
			if err != nil {
				writeError(w, r, errMalformedRequest)
				return
			}
			_ = r.Body.Close()
			r.Body = io.NopCloser(bytes.NewReader(body))
			sum := sha256.Sum256(body)
			requestHash := hex.EncodeToString(sum[:])
			route := r.Method + " " + r.URL.Path

			ctx := r.Context()
			tx, err := pool.Begin(ctx)
			if err != nil {
				writeError(w, r, err)
				return
			}
			fresh, err := claimKey(ctx, tx, route, key, requestHash)
			if err != nil {
				_ = tx.Rollback(ctx)
				writeError(w, r, err)
				return
			}
			if !fresh {
				_ = tx.Rollback(ctx)
				replayCachedResponse(w, r, pool, route, key, requestHash)
				return
			}
			runFreshRequest(ctx, w, r, tx, bind, key, next)
		})
	}
}

// maxKeyLength is the longest Idempotency-Key the fleet accepts.
const maxKeyLength = 128

// validKey reports whether key is 1-128 printable ASCII characters.
func validKey(key string) bool {
	if key == "" || len(key) > maxKeyLength {
		return false
	}
	for i := 0; i < len(key); i++ {
		if key[i] < 0x20 || key[i] > 0x7e {
			return false
		}
	}
	return true
}

// claimKey purges the expired keys and inserts the key row; fresh is false
// when the key was already used, on any operation.
func claimKey(ctx context.Context, tx pgx.Tx, route, key, requestHash string) (bool, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < now() - interval '`+keyRetention+`'`); err != nil {
		return false, err
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO idempotency_keys (route, key, request_hash)
		VALUES ($1, $2, $3)
		ON CONFLICT (key) DO NOTHING
	`, route, key, requestHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// replayCachedResponse answers a request whose key already has a committed
// row: the stored outcome when the fingerprint matches, 409 when it does not.
func replayCachedResponse(w http.ResponseWriter, r *http.Request, pool *pgxpool.Pool, route, key, requestHash string) {
	var (
		storedRoute string
		storedHash  string
		statusCode  *int
		body        []byte
		headersRaw  []byte
	)
	err := pool.QueryRow(r.Context(), `
		SELECT route, request_hash, status_code, response_body, response_headers
		FROM idempotency_keys WHERE key = $1
	`, key).Scan(&storedRoute, &storedHash, &statusCode, &body, &headersRaw)
	if err != nil {
		writeError(w, r, err)
		return
	}
	if storedRoute != route || storedHash != requestHash {
		writeProblem(w, r, idempotencyKeyReused, "the Idempotency-Key was already used with a different method, path or body")
		return
	}
	if statusCode == nil {
		// Unreachable (a committed row always has its outcome); a defensive
		// 500 rather than a fabricated response.
		writeError(w, r, errMissingOutcome)
		return
	}
	copyStoredHeaders(w.Header(), headersRaw)
	w.Header().Set(IdempotencyReplayedHeader, "true")
	w.WriteHeader(*statusCode)
	_, _ = w.Write(body)
}

var errMissingOutcome = errorString("idempotency row committed with no recorded outcome")

type errorString string

func (e errorString) Error() string { return string(e) }

func copyStoredHeaders(dst http.Header, raw []byte) {
	if len(raw) == 0 {
		return
	}
	var stored http.Header
	if err := json.Unmarshal(raw, &stored); err != nil {
		return
	}
	for k, vs := range stored {
		for _, v := range vs {
			dst.Add(k, v)
		}
	}
}

// capture records the handler's response so it can be persisted and
// committed BEFORE anything reaches the client.
type capture struct {
	header http.Header
	code   int
	body   bytes.Buffer
	// class is the replay class writeError marked (replayMarker); replayUnset
	// when the handler answered without a catalogue problem.
	class replayClass
}

func (c *capture) Header() http.Header { return c.header }

// MarkReplay implements replayMarker.
func (c *capture) MarkReplay(class replayClass) { c.class = class }

// verdict is the whole of what the middleware decides about an answer: a 2xx is
// stored with its writes, a 5xx is never stored, and every other answer is
// ruled by the replay class its catalogue entry declares. A refusal that
// declares none is not stored either: the key stays free.
func (c *capture) verdict() verdict {
	switch {
	case c.code >= http.StatusOK && c.code < http.StatusMultipleChoices:
		return storeAndKeep
	case c.code >= http.StatusInternalServerError:
		return releaseKey
	}
	switch c.class {
	case replayFinal:
		return storeRefusal
	case replayOutcome:
		return storeAndKeep
	}
	return releaseKey
}

func (c *capture) WriteHeader(code int) {
	if c.code == 0 {
		c.code = code
	}
}

func (c *capture) Write(b []byte) (int, error) {
	if c.code == 0 {
		c.code = http.StatusOK
	}
	return c.body.Write(b)
}

// runFreshRequest handles a genuinely new key: run the handler in a savepoint,
// persist the outcome in the same transaction, commit, and only then write to
// the real client.
func runFreshRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, tx pgx.Tx, bind TxBinder, key string, next http.Handler) {
	sp, err := tx.Begin(ctx)
	if err != nil {
		_ = tx.Rollback(ctx)
		writeError(w, r, err)
		return
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()
	rec := &capture{header: http.Header{}}
	next.ServeHTTP(rec, r.WithContext(bind(ctx, sp)))
	if rec.code == 0 {
		rec.code = http.StatusOK
	}
	v := rec.verdict()
	if v == releaseKey {
		// A 5xx and a RETRYABLE refusal are never cached: roll everything back,
		// the key row included, so the same key can run the real work again.
		_ = tx.Rollback(ctx)
		flush(w, rec)
		return
	}
	if err := settleSavepoint(ctx, sp, v); err != nil {
		_ = tx.Rollback(ctx)
		writeError(w, r, err)
		return
	}
	if err := storeOutcome(ctx, tx, key, rec); err != nil {
		_ = tx.Rollback(ctx)
		writeError(w, r, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeError(w, r, err)
		return
	}
	flush(w, rec)
}

// settleSavepoint keeps the handler's writes when the verdict says they commit
// with the stored answer (a 2xx, an OUTCOME refusal) and discards them for a
// FINAL refusal, which is stored alone.
func settleSavepoint(ctx context.Context, sp pgx.Tx, v verdict) error {
	if v != storeAndKeep {
		return sp.Rollback(ctx)
	}
	return sp.Commit(ctx)
}

func storeOutcome(ctx context.Context, tx pgx.Tx, key string, rec *capture) error {
	headers, err := json.Marshal(rec.header)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE idempotency_keys
		SET status_code = $1, response_body = $2, response_headers = $3, completed_at = now()
		WHERE key = $4
	`, rec.code, rec.body.Bytes(), headers, key)
	return err
}

// flush copies the captured response onto the real client connection.
func flush(w http.ResponseWriter, rec *capture) {
	for k, vs := range rec.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(rec.code)
	_, _ = w.Write(rec.body.Bytes())
}
