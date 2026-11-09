package kafka

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/delivery-promise/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// fakeProjection records the applied events; errs are returned one per Apply
// call first (nil entries succeed).
type fakeProjection struct {
	mu     sync.Mutex
	events []report.Event
	errs   []error
}

func (f *fakeProjection) Apply(_ context.Context, e report.Event) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.errs) > 0 {
		err := f.errs[0]
		f.errs = f.errs[1:]
		if err != nil {
			return false, err
		}
	}
	f.events = append(f.events, e)
	return true, nil
}

func (f *fakeProjection) applied() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.events)
}

var (
	eventTime  = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	earliest   = time.Date(2026, 11, 4, 14, 0, 0, 0, time.UTC)
	latest     = time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC)
	earliestJS = "2026-11-04T14:00:00Z"
	latestJS   = "2026-11-05T02:00:00Z"
)

// ownEvent encodes one of this context's events through the shared envelope
// builder (the one the outbox encoder uses).
func ownEvent(t *testing.T, entity, name, subject string, data map[string]any) []byte {
	t.Helper()
	b, err := cloudevents.New(cloudevents.Spec{ID: "evt-" + name, Entity: entity, EventName: name, Subject: subject,
		Time: eventTime, Stream: cloudevents.StreamEvents, Version: 1, Data: data})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func lostEvent(t *testing.T, subject string) []byte {
	return ownEvent(t, entityPromise, "PromiseBroken", subject, map[string]any{"promiseId": "prm-1", "orderId": "ord-1",
		"nodeId": "N-1", "deliverByLatest": latestJS, "lateByMinutes": 0, "cause": "LOST"})
}

func analyticsConsumer(p report.Projection, dlq DeadLetterWriter, logger *slog.Logger) *AnalyticsConsumer {
	if logger == nil {
		logger = discard()
	}
	return &AnalyticsConsumer{Projection: p, DLQ: dlq, Topics: OwnTopics(), DLQTopic: AnalyticsDLQTopic, Logger: logger}
}

type projectedCase struct {
	entity, name, subject string
	data                  map[string]any
	want                  func(e report.Event) report.Event
}

var projectedCases = []projectedCase{
	{entityDeliveryOffer, "DeliveryOfferQuoted", "off-1", map[string]any{"offerId": "off-1", "tier": "NEXT_DAY", "nodeId": "N-1",
		"destinationPostalPrefix": "787", "deliverByEarliest": earliestJS, "deliverByLatest": latestJS, "basis": "SCHEDULED"},
		func(e report.Event) report.Event {
			// The union decode also carries the quote's window and basis (unused).
			e.Kind, e.PromiseID, e.OfferID, e.Basis = report.KindDeliveryOfferQuoted, "", "off-1", "SCHEDULED"
			e.DeliverByEarliest, e.DeliverByLatest = earliest, latest
			return e
		}},
	{entityPromise, "PromiseMade", "prm-1", map[string]any{"promiseId": "prm-1", "offerId": "off-1", "tier": "NEXT_DAY", "basis": "MEASURED",
		"deliverByEarliest": earliestJS, "deliverByLatest": latestJS, "timeZone": "America/Chicago", "lines": []any{}},
		func(e report.Event) report.Event {
			e.Kind, e.OfferID, e.Basis, e.TimeZone = report.KindPromiseMade, "off-1", "MEASURED", "America/Chicago"
			e.DeliverByEarliest, e.DeliverByLatest = earliest, latest
			return e
		}},
	{entityPromise, "PromiseRevised", "prm-1", map[string]any{"promiseId": "prm-1", "revisionNo": 2, "reason": "x"},
		func(e report.Event) report.Event {
			e.Kind, e.Tier, e.RevisionNo = report.KindPromiseRevised, "", 2
			return e
		}},
	{entityPromise, "PromiseUnkeepable", "prm-1", map[string]any{"promiseId": "prm-1", "groupNo": 1},
		func(e report.Event) report.Event { e.Kind, e.Tier = report.KindPromiseUnkeepable, ""; return e }},
	{entityPromise, "PromiseKept", "prm-1", map[string]any{"promiseId": "prm-1", "deliveredAt": earliestJS},
		func(e report.Event) report.Event { e.Kind, e.Tier = report.KindPromiseKept, ""; return e }},
	{entityPromise, "PromiseBroken", "prm-1", map[string]any{"promiseId": "prm-1", "cause": "LATE", "lateByMinutes": 45},
		func(e report.Event) report.Event {
			e.Kind, e.Tier, e.Cause, e.LateByMinutes = report.KindPromiseBroken, "", "LATE", 45
			return e
		}},
}

func TestAnalyticsConsumerProjectsTheSixTypes(t *testing.T) {
	base := report.Event{At: eventTime, PromiseID: "prm-1", Tier: "NEXT_DAY"}
	for _, c := range projectedCases {
		p, dlq := &fakeProjection{}, &fakeDLQ{}
		err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(),
			kafkago.Message{Value: ownEvent(t, c.entity, c.name, c.subject, c.data)})
		if err != nil || len(dlq.got) != 0 || len(p.events) != 1 {
			t.Fatalf("%s: err=%v dlq=%d applied=%d", c.name, err, len(dlq.got), len(p.events))
		}
		want := c.want(base)
		want.EventID = "evt-" + c.name
		if !reflect.DeepEqual(p.events[0], want) {
			t.Errorf("%s projected %+v\nwant %+v", c.name, p.events[0], want)
		}
	}
}

type poisonCase struct {
	name string
	msg  func(t *testing.T) []byte
	errs []error
}

var poisonCases = []poisonCase{
	{"undecodable timestamp", func(t *testing.T) []byte {
		return ownEvent(t, entityPromise, "PromiseMade", "prm-1", map[string]any{"promiseId": "prm-1", "deliverByEarliest": "tomorrow"})
	}, nil},
	{"subject is not the aggregate", func(t *testing.T) []byte { return lostEvent(t, "prm-9") }, nil},
	{"offer subject is not the offerId", func(t *testing.T) []byte {
		return ownEvent(t, entityDeliveryOffer, "DeliveryOfferQuoted", "prm-1", map[string]any{"offerId": "off-1", "tier": "NEXT_DAY"})
	}, nil},
	{"a required field missing", func(t *testing.T) []byte {
		return ownEvent(t, entityPromise, "PromiseMade", "prm-1", map[string]any{"promiseId": "prm-1", "offerId": "off-1", "tier": "NEXT_DAY"})
	}, nil},
	{"a cause outside the vocabulary", func(t *testing.T) []byte {
		return ownEvent(t, entityPromise, "PromiseBroken", "prm-1", map[string]any{"promiseId": "prm-1", "cause": "EARLY"})
	}, nil},
	{"the store rejects it", func(t *testing.T) []byte { return lostEvent(t, "prm-1") },
		[]error{fmt.Errorf("%w: check violation", report.ErrRejected)}},
}

func TestAnalyticsConsumerDeadLettersPoisonOnce(t *testing.T) {
	for _, c := range poisonCases {
		p, dlq := &fakeProjection{errs: c.errs}, &fakeDLQ{}
		value := c.msg(t)
		m := kafkago.Message{Topic: OwnEventsTopic, Partition: 2, Offset: 7, Key: []byte("prm-1"), Value: value}
		if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), m); err != nil {
			t.Fatalf("%s: poison must be dead-lettered and committed past, got %v", c.name, err)
		}
		if len(dlq.got) != 1 || len(p.events) != 0 {
			t.Fatalf("%s: dlq=%d applied=%d, want exactly one dead letter", c.name, len(dlq.got), len(p.events))
		}
		d := dlq.got[0]
		if d.Topic != AnalyticsDLQTopic || !bytes.Equal(d.Value, value) || string(d.Key) != "prm-1" || dlqHeader(d, "x-dlq-source-topic") != OwnEventsTopic {
			t.Errorf("%s: dead letter = topic %s key %s headers %v", c.name, d.Topic, d.Key, d.Headers)
		}
		if dlqHeader(d, "x-dlq-error") == "" {
			t.Errorf("%s: the dead letter must carry the cause", c.name)
		}
	}
}

func dlqHeader(m kafkago.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func TestAnalyticsConsumerTransientFailureIsReturnedNotDeadLettered(t *testing.T) {
	p, dlq := &fakeProjection{errs: []error{errors.New("connection refused")}}, &fakeDLQ{}
	m := kafkago.Message{Value: lostEvent(t, "prm-1")}
	if err := analyticsConsumer(p, dlq, nil).HandleMessage(context.Background(), m); err == nil {
		t.Fatal("a transient failure must be returned for the loop to retry")
	}
	if len(dlq.got) != 0 {
		t.Fatal("a transient failure must never be dead-lettered")
	}
}

func TestAnalyticsConsumerFailedDeadLetterIsRetried(t *testing.T) {
	m := kafkago.Message{Value: lostEvent(t, "prm-9")}
	dlq := &fakeDLQ{errs: []error{errors.New("broker down")}}
	err := analyticsConsumer(&fakeProjection{}, dlq, nil).HandleMessage(context.Background(), m)
	if err == nil || !strings.Contains(err.Error(), AnalyticsDLQTopic) {
		t.Fatalf("a failed dead-letter write must be returned (so the message is retried), got %v", err)
	}
}

func TestAnalyticsConsumerRunRetriesTransientFailuresForever(t *testing.T) {
	down := errors.New("down")
	p, dlq := &fakeProjection{errs: []error{down, down, down, down, down, down, nil}}, &fakeDLQ{}
	c := analyticsConsumer(p, dlq, nil)
	r := &fakeReader{msgs: []kafkago.Message{{Topic: OwnEventsTopic, Offset: 1, Value: lostEvent(t, "prm-1")}}}
	c.Reader = r
	var mu sync.Mutex
	var sleeps []time.Duration
	c.Retry = RetryPolicy{Initial: time.Millisecond, Max: 4 * time.Millisecond}
	c.sleep = func(_ context.Context, d time.Duration) error {
		mu.Lock()
		sleeps = append(sleeps, d)
		mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	waitCommitted(t, r, 1)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want context.Canceled", err)
	}
	if p.applied() != 1 || len(dlq.got) != 0 {
		t.Fatalf("applied=%d dlq=%d: six transient failures must be retried past the domain cap, never dead-lettered", p.applied(), len(dlq.got))
	}
	want := []time.Duration{time.Millisecond, 2 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond, 4 * time.Millisecond}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(sleeps, want) {
		t.Fatalf("backoff = %v, want the capped sequence %v", sleeps, want)
	}
}

// waitCommitted waits until r committed n messages.
func waitCommitted(t *testing.T, r *fakeReader, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		got := len(r.committed)
		r.mu.Unlock()
		if got >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the reader did not commit %d message(s) in time", n)
}
