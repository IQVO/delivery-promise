package kafka

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/claudioed/delivery-promise/internal/adapters/kafka/cloudevents"
)

// warnCounter counts the WARN records and keeps the last one's attributes.
type warnCounter struct {
	mu    sync.Mutex
	warns int
	last  map[string]any
}

func (w *warnCounter) Enabled(context.Context, slog.Level) bool { return true }

func (w *warnCounter) Handle(_ context.Context, r slog.Record) error {
	if r.Level != slog.LevelWarn {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.warns++
	w.last = map[string]any{}
	r.Attrs(func(a slog.Attr) bool { w.last[a.Key] = a.Value.Any(); return true })
	return nil
}

func (w *warnCounter) WithAttrs([]slog.Attr) slog.Handler { return w }
func (w *warnCounter) WithGroup(string) slog.Handler      { return w }

func TestAnalyticsConsumerSkipsNonCloudEventsAndUnknownTypesWithASampledWarn(t *testing.T) {
	warns := &warnCounter{}
	p, dlq := &fakeProjection{}, &fakeDLQ{}
	c := analyticsConsumer(p, dlq, slog.New(warns))
	clock := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	c.Now = func() time.Time { return clock }
	skip := []kafkago.Message{
		{Value: []byte(`{"event_id":"e-1","event_type":"PromiseKept"}`)},
		{Value: []byte("not json")},
		{Value: ownEvent(t, entityPromise, "PromiseTeleported", "prm-1", map[string]any{"promiseId": "prm-1"})},
	}
	for _, m := range skip {
		if err := c.HandleMessage(context.Background(), m); err != nil {
			t.Fatalf("a skipped message must be committed past, got %v", err)
		}
	}
	if warns.warns != 1 {
		t.Fatalf("warns = %d, want 1 (sampled)", warns.warns)
	}
	clock = clock.Add(59 * time.Second)
	_ = c.HandleMessage(context.Background(), skip[0])
	if warns.warns != 1 {
		t.Fatalf("a skip inside the interval must stay silent, warns = %d", warns.warns)
	}
	clock = clock.Add(time.Second)
	_ = c.HandleMessage(context.Background(), skip[1])
	if warns.warns != 2 || warns.last["suppressed_since_last_warning"] != int64(3) {
		t.Fatalf("warns = %d last = %v, want a second warning reporting 3 suppressed", warns.warns, warns.last)
	}
	if len(p.events) != 0 || len(dlq.got) != 0 {
		t.Fatalf("a skipped message is neither projected nor dead-lettered: applied=%d dlq=%d", len(p.events), len(dlq.got))
	}
}

func TestAnalyticsConsumerAcknowledgesTheUnprojectedOwnTypesSilently(t *testing.T) {
	warns := &warnCounter{}
	p, dlq := &fakeProjection{}, &fakeDLQ{}
	c := analyticsConsumer(p, dlq, slog.New(warns))
	for _, m := range []struct{ entity, name string }{
		{entityPromise, "PromiseConfirmed"}, {entityPromise, "PromiseAtRisk"}, {entityPromise, "PromiseCancelled"},
		{entityPromise, "PromiseReleased"}, {entityPromiseBook, "PromiseBookExhausted"}, {entityPromisePolicy, "PromisePolicyPublished"},
	} {
		msg := kafkago.Message{Value: ownEvent(t, m.entity, m.name, "x-1", map[string]any{"promiseId": "x-1"})}
		if err := c.HandleMessage(context.Background(), msg); err != nil {
			t.Fatalf("%s: %v", m.name, err)
		}
	}
	if warns.warns != 0 || len(p.events) != 0 || len(dlq.got) != 0 {
		t.Fatalf("warns=%d applied=%d dlq=%d, want all zero", warns.warns, len(p.events), len(dlq.got))
	}
}

// The projected and acknowledged types together are exactly the twelve types
// this context publishes (ADR 0004), each named by its FULL type string.
func TestAnalyticsConsumerCoversEveryPublishedType(t *testing.T) {
	var got []string
	for typ := range projectedKinds() {
		got = append(got, typ)
	}
	for typ := range acknowledgedTypes() {
		got = append(got, typ)
	}
	want := []string{
		cloudevents.Type("deliveryoffer", "DeliveryOfferQuoted"),
		cloudevents.Type("promise", "PromiseAtRisk"), cloudevents.Type("promise", "PromiseBroken"),
		cloudevents.Type("promise", "PromiseCancelled"), cloudevents.Type("promise", "PromiseConfirmed"),
		cloudevents.Type("promise", "PromiseKept"), cloudevents.Type("promise", "PromiseMade"),
		cloudevents.Type("promise", "PromiseReleased"), cloudevents.Type("promise", "PromiseRevised"),
		cloudevents.Type("promise", "PromiseUnkeepable"), cloudevents.Type("promisebook", "PromiseBookExhausted"),
		cloudevents.Type("promisepolicy", "PromisePolicyPublished"),
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("types = %v\nwant %v", got, want)
	}
	if want[0] != "com.retailnetwork.commerce.delivery-promise.deliveryoffer.DeliveryOfferQuoted" {
		t.Fatalf("full type = %q", want[0])
	}
}

func TestSkipWarnInterval(t *testing.T) {
	if skipWarnEvery() != time.Minute {
		t.Fatalf("skipWarnEvery = %v", skipWarnEvery())
	}
}

func TestNewAnalyticsConsumerWiring(t *testing.T) {
	c := NewAnalyticsConsumer([]string{"localhost:1"}, OwnTopics(), AnalyticsDLQTopic, "group-x", &fakeProjection{}, nil)
	if !slices.Equal(c.Topics, OwnTopics()) || c.DLQTopic != AnalyticsDLQTopic || c.Reader == nil || c.DLQ == nil || c.Logger == nil {
		t.Fatalf("consumer = %+v", c)
	}
	if !slices.Equal(OwnTopics(), []string{"retailnetwork.delivery-promise.events", "retailnetwork.delivery-promise.analytics"}) ||
		AnalyticsDLQTopic != "retailnetwork.delivery-promise.analytics.dlq" {
		t.Fatal("the projector reads its own two topics and dead-letters to the analytics DLQ")
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
}
