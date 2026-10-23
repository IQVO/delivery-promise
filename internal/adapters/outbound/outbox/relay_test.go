package outbox

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

var errSend = errors.New("send failed")

// fakeStore drains scripted rows: it sends them in order and stops at the first error.
type fakeStore struct {
	mu          sync.Mutex
	rows        []Message
	lastMax     int
	lastLimit   int
	failWith    error // returned instead of draining
	published   []string
	drainCalls  int
	attemptsOut int // Attempts reported in the SendFailure
}

func (s *fakeStore) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.drainCalls
}

func (s *fakeStore) Drain(ctx context.Context, limit, maxAttempts int, send func(context.Context, Message) error) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drainCalls++
	s.lastLimit, s.lastMax = limit, maxAttempts
	if s.failWith != nil {
		return 0, s.failWith
	}
	n := 0
	for len(s.rows) > 0 && n < limit {
		m := s.rows[0]
		if err := send(ctx, m); err != nil {
			return n, &SendFailure{RowID: int64(n + 1), Message: m, Attempts: s.attemptsOut, Err: err}
		}
		s.published = append(s.published, m.EventID)
		s.rows = s.rows[1:]
		n++
	}
	return n, nil
}

type fakeSink struct {
	mu  sync.Mutex
	got []Message
	err error
}

func (s *fakeSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.got)
}

func (s *fakeSink) Send(_ context.Context, msgs ...Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	s.got = append(s.got, msgs...)
	return nil
}

func msg(id string) Message {
	return Message{EventID: id, Topic: "t", EventType: "type." + id, Key: []byte("k-" + id)}
}

func TestDefaultsAndOptionsAreClamped(t *testing.T) {
	r := NewOutboxRelay(&fakeStore{}, &fakeSink{}, nil)
	if r.interval != DefaultInterval || r.batchSize != DefaultBatchSize || r.maxAttempts != 0 || r.logger == nil {
		t.Fatalf("defaults = %+v", r)
	}
	r = NewOutboxRelay(&fakeStore{}, &fakeSink{}, nil, WithInterval(0), WithBatchSize(-1), WithMaxAttempts(-3))
	if r.interval != DefaultInterval || r.batchSize != DefaultBatchSize || r.maxAttempts != 0 {
		t.Fatalf("non-positive options must be ignored: %+v", r)
	}
	r = NewOutboxRelay(&fakeStore{}, &fakeSink{}, nil, WithInterval(time.Minute), WithBatchSize(7), WithMaxAttempts(5))
	if r.interval != time.Minute || r.batchSize != 7 || r.maxAttempts != 5 {
		t.Fatalf("options = %+v", r)
	}
	r = NewOutboxRelay(&fakeStore{}, &fakeSink{}, nil, WithMaxAttempts(0))
	if r.maxAttempts != 0 {
		t.Fatal("0 (unlimited) must be accepted")
	}
}

func TestRelayOncePublishesBatchToTheSinkAndPassesTheCap(t *testing.T) {
	st := &fakeStore{rows: []Message{msg("a"), msg("b"), msg("c")}}
	sink := &fakeSink{}
	r := NewOutboxRelay(st, sink, nil, WithBatchSize(2), WithMaxAttempts(9))
	n, err := r.RelayOnce(context.Background())
	if err != nil || n != 2 || len(sink.got) != 2 || sink.got[0].EventID != "a" || sink.got[1].EventID != "b" {
		t.Fatalf("n=%d err=%v sent=%v", n, err, sink.got)
	}
	if st.lastLimit != 2 || st.lastMax != 9 {
		t.Fatalf("store saw limit %d cap %d", st.lastLimit, st.lastMax)
	}
}

func logBuffer() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})), &buf
}

func TestTheRowThatReachesTheCapIsLoggedAtErrorOnce(t *testing.T) {
	cases := []struct {
		name       string
		max, tries int
		wantLog    bool
	}{
		{"below the cap", 3, 2, false},
		{"at the cap", 3, 3, true},
		{"no cap configured", 0, 50, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log, buf := logBuffer()
			st := &fakeStore{rows: []Message{msg("a")}, attemptsOut: c.tries}
			r := NewOutboxRelay(st, &fakeSink{err: errSend}, log, WithMaxAttempts(c.max))
			_, err := r.RelayOnce(context.Background())
			var sf *SendFailure
			if !errors.As(err, &sf) || !errors.Is(err, errSend) || !strings.Contains(err.Error(), "type.a") {
				t.Fatalf("err = %v", err)
			}
			out := buf.String()
			if got := strings.Contains(out, AttemptCapReachedMsg); got != c.wantLog {
				t.Fatalf("cap message logged = %v, want %v:\n%s", got, c.wantLog, out)
			}
			if c.wantLog && (!strings.Contains(out, "level=ERROR") || !strings.Contains(out, "outbox_id=1") || !strings.Contains(out, "topic=t") ||
				!strings.Contains(out, "event_type=type.a") || !strings.Contains(out, "key=k-a")) {
				t.Fatalf("cap log lacks fields:\n%s", out)
			}
		})
	}
}

func TestANonSendFailureErrorIsNotLoggedAsACap(t *testing.T) {
	log, buf := logBuffer()
	r := NewOutboxRelay(&fakeStore{failWith: errors.New("db down")}, &fakeSink{}, log, WithMaxAttempts(1))
	if _, err := r.RelayOnce(context.Background()); err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(buf.String(), AttemptCapReachedMsg) {
		t.Fatal("a store error is not a capped row")
	}
}

func TestRunDrainsUntilCancelledAndLogsFailedPasses(t *testing.T) {
	log, buf := logBuffer()
	st := &fakeStore{rows: []Message{msg("a"), msg("b"), msg("c")}}
	sink := &fakeSink{}
	r := NewOutboxRelay(st, sink, log, WithBatchSize(2), WithInterval(time.Millisecond))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && sink.count() < 3 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned %v", err)
	}
	if sink.count() != 3 {
		t.Fatalf("published %d of 3", sink.count())
	}
	// a failing store is logged, not fatal
	st2 := &fakeStore{failWith: errors.New("db down")}
	r2 := NewOutboxRelay(st2, &fakeSink{}, log, WithInterval(time.Millisecond))
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan error, 1)
	go func() { done2 <- r2.Run(ctx2) }()
	for time.Now().Before(deadline) && st2.calls() < 2 {
		time.Sleep(time.Millisecond)
	}
	cancel2()
	<-done2
	if !strings.Contains(buf.String(), "outbox relay pass failed") {
		t.Fatalf("failed pass not logged:\n%s", buf.String())
	}
	// an already-cancelled context never drains
	st3 := &fakeStore{rows: []Message{msg("z")}}
	if err := NewOutboxRelay(st3, &fakeSink{}, nil).Run(ctx2); !errors.Is(err, context.Canceled) || st3.calls() != 0 {
		t.Fatalf("err=%v calls=%d", err, st3.calls())
	}
}

func TestACancelledPassIsNotLoggedAsAFailure(t *testing.T) {
	log, buf := logBuffer()
	r := NewOutboxRelay(&fakeStore{failWith: context.Canceled}, &fakeSink{}, log, WithInterval(time.Hour))
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()
	_ = r.Run(ctx)
	if strings.Contains(buf.String(), "pass failed") {
		t.Fatalf("a shutdown is not a failure:\n%s", buf.String())
	}
}
