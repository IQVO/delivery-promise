package kafka

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
)

// fakeReader serves scripted messages, then blocks until ctx is done (or fails
// with io.EOF when failAfter is set).
type fakeReader struct {
	mu        sync.Mutex
	msgs      []kafkago.Message
	committed []kafkago.Message
	commitErr []error // consumed one per CommitMessages call
	eofAtEnd  bool
	closed    bool
}

func (r *fakeReader) FetchMessage(ctx context.Context) (kafkago.Message, error) {
	r.mu.Lock()
	if len(r.msgs) > 0 {
		m := r.msgs[0]
		r.msgs = r.msgs[1:]
		r.mu.Unlock()
		return m, nil
	}
	eof := r.eofAtEnd
	r.mu.Unlock()
	if eof {
		return kafkago.Message{}, io.EOF
	}
	<-ctx.Done()
	return kafkago.Message{}, ctx.Err()
}

func (r *fakeReader) CommitMessages(_ context.Context, msgs ...kafkago.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.commitErr) > 0 {
		err := r.commitErr[0]
		r.commitErr = r.commitErr[1:]
		if err != nil {
			return err
		}
	}
	r.committed = append(r.committed, msgs...)
	return nil
}

func (r *fakeReader) Close() error { r.closed = true; return nil }

type fakeDLQ struct {
	mu     sync.Mutex
	got    []kafkago.Message
	errs   []error
	closed bool
}

func (d *fakeDLQ) WriteMessages(_ context.Context, msgs ...kafkago.Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.errs) > 0 {
		err := d.errs[0]
		d.errs = d.errs[1:]
		if err != nil {
			return err
		}
	}
	d.got = append(d.got, msgs...)
	return nil
}

func (d *fakeDLQ) Close() error { d.closed = true; return nil }

func noSleep(context.Context, time.Duration) error { return nil }

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func msg(offset int64, value string) kafkago.Message {
	return kafkago.Message{Topic: "retailnetwork.x.events", Partition: 1, Offset: offset, Key: []byte("k"), Value: []byte(value)}
}

func TestTheLoopCommitsOnlyAfterSuccessfulHandling(t *testing.T) {
	r := &fakeReader{msgs: []kafkago.Message{msg(1, "a"), msg(2, "b")}, eofAtEnd: true}
	var handled []string
	l := &consumeLoop{reader: r, logger: discard(), name: "t", sleep: noSleep,
		handle: func(_ context.Context, m kafkago.Message) error {
			if len(r.committed) != len(handled) {
				t.Errorf("message %s fetched before the previous commit", m.Value)
			}
			handled = append(handled, string(m.Value))
			return nil
		}}
	if err := l.run(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("run = %v", err)
	}
	if strings.Join(handled, "") != "ab" || len(r.committed) != 2 {
		t.Fatalf("handled %v committed %d", handled, len(r.committed))
	}
}

func TestATransientFailureIsRetriedWithDoublingBackoffThenSucceeds(t *testing.T) {
	r := &fakeReader{msgs: []kafkago.Message{msg(1, "a")}, eofAtEnd: true}
	var delays []time.Duration
	calls := 0
	l := &consumeLoop{reader: r, logger: discard(), name: "t", retry: RetryPolicy{Initial: 10 * time.Millisecond, Max: 25 * time.Millisecond},
		sleep: func(_ context.Context, d time.Duration) error { delays = append(delays, d); return nil },
		handle: func(context.Context, kafkago.Message) error {
			calls++
			if calls < 4 {
				return errors.New("transient")
			}
			return nil
		}}
	_ = l.run(context.Background())
	want := []time.Duration{10 * time.Millisecond, 20 * time.Millisecond, 25 * time.Millisecond}
	if calls != 4 || len(delays) != 3 || delays[0] != want[0] || delays[1] != want[1] || delays[2] != want[2] {
		t.Fatalf("calls %d delays %v", calls, delays)
	}
	if len(r.committed) != 1 {
		t.Fatalf("committed %d", len(r.committed))
	}
}

func TestAMessageThatKeepsFailingIsDeadLetteredAfterTheMaximumAttempts(t *testing.T) {
	r := &fakeReader{msgs: []kafkago.Message{msg(7, "stuck")}, eofAtEnd: true}
	dlq := &fakeDLQ{}
	calls := 0
	l := &consumeLoop{reader: r, logger: discard(), name: "t", sleep: noSleep, maxAttempts: 3,
		handle: func(context.Context, kafkago.Message) error { calls++; return errors.New("always") },
		deadLetter: func(ctx context.Context, m kafkago.Message, cause error) error {
			return publishDeadLetter(ctx, dlq, m.Topic+DLQSuffix, m, cause)
		}}
	_ = l.run(context.Background())
	if calls != 3 || len(dlq.got) != 1 || len(r.committed) != 1 {
		t.Fatalf("calls %d dlq %d committed %d", calls, len(dlq.got), len(r.committed))
	}
	d := dlq.got[0]
	headers := map[string]string{}
	for _, h := range d.Headers {
		headers[h.Key] = string(h.Value)
	}
	if d.Topic != "retailnetwork.x.events.dlq" || string(d.Value) != "stuck" || string(d.Key) != "k" ||
		headers["x-dlq-source-topic"] != "retailnetwork.x.events" || headers["x-dlq-source-partition"] != "1" ||
		headers["x-dlq-source-offset"] != "7" || headers["x-dlq-error"] != "always" || headers["x-dlq-failed-at"] == "" {
		t.Fatalf("dead letter = %+v headers %v", d, headers)
	}
}

func TestAPoisonMessageIsDeadLetteredAtOnceAndTheDeadLetterPublishIsRetried(t *testing.T) {
	r := &fakeReader{msgs: []kafkago.Message{msg(1, "poison")}, eofAtEnd: true}
	dlq := &fakeDLQ{errs: []error{errors.New("broker blip"), nil}}
	calls := 0
	l := &consumeLoop{reader: r, logger: discard(), name: "t", sleep: noSleep, maxAttempts: 5,
		handle: func(context.Context, kafkago.Message) error { calls++; return errPoison },
		deadLetter: func(ctx context.Context, m kafkago.Message, cause error) error {
			return publishDeadLetter(ctx, dlq, m.Topic+DLQSuffix, m, cause)
		}}
	_ = l.run(context.Background())
	if calls != 1 || len(dlq.got) != 1 || len(r.committed) != 1 {
		t.Fatalf("calls %d dlq %d committed %d", calls, len(dlq.got), len(r.committed))
	}
}

func TestWithoutADeadLetterSinkTheLoopRetriesTheSameMessageForever(t *testing.T) {
	r := &fakeReader{msgs: []kafkago.Message{msg(1, "a")}}
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	l := &consumeLoop{reader: r, logger: discard(), name: "t",
		sleep: func(context.Context, time.Duration) error { return nil },
		handle: func(context.Context, kafkago.Message) error {
			calls++
			if calls == 6 {
				cancel()
			}
			return errors.New("down")
		}}
	if err := l.run(ctx); !errors.Is(err, context.Canceled) || calls != 6 || len(r.committed) != 0 {
		t.Fatalf("err %v calls %d committed %d", err, calls, len(r.committed))
	}
}

func TestAFailedCommitIsRetriedAndSleepErrorsStopTheLoop(t *testing.T) {
	r := &fakeReader{msgs: []kafkago.Message{msg(1, "a")}, commitErr: []error{errors.New("rebalance")}, eofAtEnd: true}
	l := &consumeLoop{reader: r, logger: discard(), name: "t", sleep: noSleep, handle: func(context.Context, kafkago.Message) error { return nil }}
	_ = l.run(context.Background())
	if len(r.committed) != 1 {
		t.Fatalf("committed %d", len(r.committed))
	}
	stop := errors.New("stop")
	r2 := &fakeReader{msgs: []kafkago.Message{msg(1, "a")}}
	l2 := &consumeLoop{reader: r2, logger: discard(), name: "t", sleep: func(context.Context, time.Duration) error { return stop },
		handle: func(context.Context, kafkago.Message) error { return errors.New("x") }}
	if err := l2.run(context.Background()); !errors.Is(err, stop) {
		t.Fatalf("run = %v", err)
	}
}

func TestSleepCtxHonoursCancellation(t *testing.T) {
	if err := sleepCtx(context.Background(), time.Millisecond); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepCtx(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestRetryPolicyDefaultsAndCap(t *testing.T) {
	p := RetryPolicy{}.withDefaults()
	if p.Initial != DefaultRetryInitial || p.Max != DefaultRetryMax {
		t.Fatalf("defaults = %+v", p)
	}
	if got := p.next(4 * time.Second); got != DefaultRetryMax {
		t.Fatalf("next = %v", got)
	}
	if got := p.next(time.Second); got != 2*time.Second {
		t.Fatalf("next = %v", got)
	}
}

func TestReaderConfigNamesTheGroupAndNeverAutoCommits(t *testing.T) {
	cfg := readerConfig([]string{"b:9092"}, Topics(), "grp")
	if cfg.GroupID != "grp" || len(cfg.GroupTopics) != 8 || cfg.CommitInterval != 0 || cfg.Topic != "" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestConsumerRunUsesTheDeadLetterSinkAndClosesEverything(t *testing.T) {
	r := &fakeReader{msgs: []kafkago.Message{msg(1, `{"specversion":"1.0","id":"e","source":"/s","type":"com.retailnetwork.logistics.node-topology.node.NodeRegistered","data":{"nodeId":""},"datacontenttype":"application/json"}`)}, eofAtEnd: true}
	dlq := &fakeDLQ{}
	var buf bytes.Buffer
	svc, _ := newService(nil)
	c := &Consumer{Name: "t", Topics: Topics(), Reader: r, Handlers: Handlers(svc), Logger: slog.New(slog.NewTextHandler(&buf, nil)), DLQ: dlq, sleep: noSleep}
	if err := c.Run(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("run = %v", err)
	}
	if len(dlq.got) != 1 || dlq.got[0].Topic != "retailnetwork.x.events.dlq" || len(r.committed) != 1 {
		t.Fatalf("dlq %d committed %d log %s", len(dlq.got), len(r.committed), buf.String())
	}
	if err := c.Close(); err != nil || !r.closed || !dlq.closed {
		t.Fatalf("close err %v reader %v dlq %v", err, r.closed, dlq.closed)
	}
}

func TestTopicNotReadyIsRetriedUntilTheTopicExists(t *testing.T) {
	notReady := kafkago.WriteErrors{kafkago.LeaderNotAvailable}
	if !isTopicNotReady(notReady) || !isTopicNotReady(kafkago.UnknownTopicOrPartition) {
		t.Fatal("leader-not-available and unknown-topic are transient")
	}
	if isTopicNotReady(errors.New("other")) || isTopicNotReady(kafkago.WriteErrors{errors.New("other")}) || isTopicNotReady(kafkago.WriteErrors{}) {
		t.Fatal("anything else is not")
	}
	w := &fakeDLQ{errs: []error{notReady}}
	if err := writeDLQ(context.Background(), w, kafkago.Message{}); err != nil || len(w.got) != 1 {
		t.Fatalf("err %v got %d", err, len(w.got))
	}
	hard := errors.New("auth")
	if err := writeDLQ(context.Background(), &fakeDLQ{errs: []error{hard}}, kafkago.Message{}); !errors.Is(err, hard) {
		t.Fatalf("err = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := writeDLQ(ctx, &fakeDLQ{errs: []error{notReady, notReady}}, kafkago.Message{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}

func TestNewConsumerBuildsWithoutDialingAndCloses(t *testing.T) {
	svc, _ := newService(nil)
	c := NewConsumer("t", []string{"127.0.0.1:1"}, Topics(), "grp", Handlers(svc), nil)
	if c.Reader == nil || c.DLQ == nil || c.Logger == nil {
		t.Fatalf("consumer = %+v", c)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}
