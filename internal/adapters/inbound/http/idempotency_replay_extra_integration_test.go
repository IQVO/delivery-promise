//go:build integration

package http

// What the template's fake handler cannot show and the middleware must still do (ADR 0031): an
// OUTCOME refusal commits the handler's writes with its stored answer, and a request waiting on
// the key while the first ends RETRYABLE runs the handler itself.

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
)

// No catalogue entry is an OUTCOME today (a refused request commits nothing here), so the
// capture is marked by hand: the proof is that the middleware honours the class when one appears.
func TestAnOutcomeRefusalCommitsItsWritesWithItsStoredAnswer(t *testing.T) {
	a := newReplayAdapter(t).(*repoAdapter)
	var calls atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if err := a.Write(r.Context(), "recorded"); err != nil {
			t.Error(err)
		}
		w.(replayMarker).MarkReplay(replayOutcome)
		writeProblem(w, r, problem{http.StatusConflict, "recorded-then-refused", "Recorded, then refused"}, "the command committed and then refused")
	})
	mw := a.Middleware(t, h)

	first := post(t, mw, "k-outcome", `{"n":1}`)
	if first.Code != http.StatusConflict || a.Committed(t, "recorded") != 1 || a.KeyRows(t, "k-outcome") != 1 {
		t.Fatalf("first = %d, committed %d, key rows %d, want 409 with the write committed and the answer stored", first.Code, a.Committed(t, "recorded"), a.KeyRows(t, "k-outcome"))
	}
	for i := range 3 {
		rep := post(t, mw, "k-outcome", `{"n":1}`)
		if rep.Code != http.StatusConflict || !replayed(rep) || rep.Body.String() != first.Body.String() {
			t.Errorf("replay %d = %d replayed=%v, want the stored 409", i, rep.Code, replayed(rep))
		}
	}
	if calls.Load() != 1 || a.Committed(t, "recorded") != 1 {
		t.Errorf("handler ran %d times, %d writes committed: a replay records nothing new", calls.Load(), a.Committed(t, "recorded"))
	}
}

// A FINAL refusal is stored alone: whatever the command wrote before refusing rolls back.
func TestAFinalRefusalStoresTheAnswerAndRollsBackThePartialWrites(t *testing.T) {
	a := newReplayAdapter(t).(*repoAdapter)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := a.Write(r.Context(), "partial"); err != nil {
			t.Error(err)
		}
		writeError(w, r, errMalformedRequest)
	})
	mw := a.Middleware(t, h)
	first := post(t, mw, "k-final-partial", `{"n":1}`)
	rep := post(t, mw, "k-final-partial", `{"n":1}`)
	if first.Code != http.StatusBadRequest || !replayed(rep) || a.KeyRows(t, "k-final-partial") != 1 || a.Committed(t, "partial") != 0 {
		t.Errorf("first %d replayed=%v rows=%d committed=%d, want 400 stored, replayed, no partial write", first.Code, replayed(rep), a.KeyRows(t, "k-final-partial"), a.Committed(t, "partial"))
	}
}

// A concurrent duplicate waits on the key row of the first (INSERT ... ON CONFLICT blocks until
// the first transaction resolves). When the first ends RETRYABLE it rolls back, the row is gone,
// and exactly one waiting copy claims the key and runs the handler; the rest replay THAT answer.
// Nobody replays the released refusal and the handler never runs twice at once.
func TestACopyWaitingOnARetryableFirstRunsTheHandlerItself(t *testing.T) {
	a := newReplayAdapter(t).(*repoAdapter)
	var calls, running, overlap atomic.Int32
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)
		if running.Add(1) > 1 {
			overlap.Add(1)
		}
		defer running.Add(-1)
		if n == 1 {
			time.Sleep(300 * time.Millisecond) // the copies queue on the key row meanwhile
			writeError(w, r, ports.ErrConcurrentUpdate)
			return
		}
		succeeding(a, "ok")(w, r)
	})
	mw := a.Middleware(t, h)

	const copies = 8
	recs := make([]*httptest.ResponseRecorder, copies)
	var wg sync.WaitGroup
	for i := range recs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			recs[i] = post(t, mw, "k-dup", `{"n":1}`)
		}()
	}
	wg.Wait()

	var refused, fresh, replays int
	for _, rec := range recs {
		switch {
		case rec.Code == http.StatusConflict && !replayed(rec):
			refused++
		case rec.Code == http.StatusCreated && !replayed(rec):
			fresh++
		case rec.Code == http.StatusCreated && replayed(rec):
			replays++
		default:
			t.Errorf("unexpected answer %d replayed=%v %s", rec.Code, replayed(rec), rec.Body.String())
		}
	}
	if refused != 1 || fresh != 1 || replays != copies-2 {
		t.Errorf("answers: %d refused, %d fresh 201, %d replayed 201; want 1, 1, %d", refused, fresh, replays, copies-2)
	}
	if calls.Load() != 2 || overlap.Load() != 0 {
		t.Errorf("handler ran %d times with %d overlaps, want 2 and 0", calls.Load(), overlap.Load())
	}
	if a.Committed(t, "ok") != 1 || a.KeyRows(t, "k-dup") != 1 {
		t.Errorf("committed %d writes and %d key rows, want 1 and 1", a.Committed(t, "ok"), a.KeyRows(t, "k-dup"))
	}
}
