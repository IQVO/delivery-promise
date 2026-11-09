//go:build integration

package features_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/claudioed/delivery-promise/internal/adapters/inbound/kafka"
	"github.com/claudioed/delivery-promise/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/delivery-promise/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/delivery-promise/internal/adapters/outbound/kafka"
	relay "github.com/claudioed/delivery-promise/internal/adapters/outbound/outbox"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
	"github.com/claudioed/delivery-promise/internal/testing/kafkatest"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// The whole analytics read side (ADR 0018) against real infrastructure, all
// started with testcontainers (never skip-gated, never a DSN from env): the
// REAL encoder shapes this context's CloudEvents, the REAL Postgres outbox
// stores them in the OLTP database, the relay drains them to a real Kafka (on
// BOTH own topics, losing one ack, so a row is republished under the SAME id),
// the projector's consumer projects .events and .analytics into a SEPARATE
// analytical Postgres (its own container), and the reports answer from it over
// a read-only pool -- while legacy, unknown-type, poison and duplicate
// messages share the topics. The DLQ is read back, and a later marker event
// proves the duplicate was consumed, not stuck. The use cases that raise these
// events are covered against the same outbox by cmd/api's TestRealWiring.

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

func e2eAnalyticalPostgres(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	c, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("promise_analytics"), tcpostgres.WithUsername("itest"), tcpostgres.WithPassword("itest"),
		testcontainers.WithWaitStrategy(wait.ForLog("database system is ready to accept connections").WithOccurrence(2).WithStartupTimeout(90*time.Second)))
	if err != nil {
		t.Fatalf("start the analytical postgres: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(c) })
	url, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return url
}

// e2eLostAckOnce delivers to the real sink and THEN reports failure once: the
// "published, but the row was not marked" crash window, so the relay
// republishes the first row with the SAME CloudEvents id.
type e2eLostAckOnce struct {
	inner relay.Sink
	mu    sync.Mutex
	fired bool
}

func (f *e2eLostAckOnce) Send(ctx context.Context, msgs ...relay.Message) error {
	if err := f.inner.Send(ctx, msgs...); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.fired {
		f.fired = true
		return errors.New("simulated crash after the broker ack, before the row was marked published")
	}
	return nil
}

func e2eAt(day, hour int) time.Time { return time.Date(2026, 11, day, hour, 0, 0, 0, time.UTC) }

func e2eHeader(at time.Time) shared.Header { return shared.Header{Version: 1, At: at} }

// e2eMade is a PromiseMade of the given terms, raised at the given instant.
func e2eMade(id string, at time.Time, tier shared.Tier, basis shared.Basis, w shared.Window) promise.PromiseMade {
	return promise.PromiseMade{Header: e2eHeader(at), PromiseID: promise.ID(id), OfferID: "off-" + id,
		Terms:   promise.Terms{Tier: tier, Basis: basis, ShipBy: at.Add(time.Hour), Window: w, PolicyVersion: "v1"},
		Witness: shared.Witness{NodeID: "N-1"}, HoldExpiresAt: at.Add(15 * time.Minute), Lines: []shared.Line{{CatalogItemID: "ci-1", Quantity: 1}}}
}

func e2eQuoted(id string, at time.Time, tier shared.Tier) offer.DeliveryOfferQuoted {
	return offer.DeliveryOfferQuoted{Header: e2eHeader(at), OfferID: offer.ID(id), Tier: tier, DestinationPrefix: "787", NodeID: "N-1",
		Window: shared.Window{Earliest: at.Add(24 * time.Hour), Latest: at.Add(36 * time.Hour), TimeZone: "America/Chicago"},
		Basis:  shared.BasisScheduled, PolicyVersion: "v1", ExpiresAt: at.Add(10 * time.Minute)}
}

func e2eBroken(id string, at time.Time, cause promise.Cause, lateBy int) promise.PromiseBroken {
	return promise.PromiseBroken{Header: e2eHeader(at), PromiseID: promise.ID(id), OrderID: "ord-" + id, NodeID: "N-1",
		DeliverByLatest: at.Add(-time.Hour), LateByMinutes: lateBy, Cause: cause}
}

// e2eEvents is what the use cases raise over three days, in outbox order: the
// quotes, the promises and their judgements, plus acknowledged-only types.
func e2eEvents() []shared.Event {
	// 09:00-21:00 in Chicago on 2026-11-04 (CST, UTC-6): one local day.
	chicagoDay := shared.Window{Earliest: e2eAt(4, 15), Latest: e2eAt(5, 3), TimeZone: "America/Chicago"}
	noZone := shared.Window{Earliest: e2eAt(4, 15), Latest: e2eAt(5, 3)} // read in UTC: two days
	twoDays := shared.Window{Earliest: e2eAt(4, 15), Latest: e2eAt(6, 3), TimeZone: "America/Chicago"}
	return []shared.Event{
		e2eQuoted("off-A", e2eAt(3, 8), shared.TierNextDay),
		e2eQuoted("off-B", e2eAt(3, 8), shared.TierNextDay),
		e2eQuoted("off-C", e2eAt(3, 8), shared.TierSameDay),
		e2eQuoted("off-D", e2eAt(3, 8), shared.TierStandard),
		e2eMade("A", e2eAt(3, 9), shared.TierNextDay, shared.BasisScheduled, chicagoDay),
		promise.PromiseConfirmed{Header: e2eHeader(e2eAt(3, 9)), PromiseID: "A", OrderID: "ord-A", GroupNo: 1},
		e2eMade("B", e2eAt(3, 10), shared.TierNextDay, shared.BasisMeasured, noZone),
		e2eMade("D", e2eAt(3, 11), shared.TierStandard, shared.BasisNominal, twoDays),
		promise.PromiseRevised{Header: e2eHeader(e2eAt(4, 10)), PromiseID: "B", OrderID: "ord-B", RevisionNo: 1, NodeID: "N-1",
			PreviousDeliverByLatest: e2eAt(5, 3), Terms: promise.Terms{Tier: shared.TierNextDay, Basis: shared.BasisMeasured, ShipBy: e2eAt(4, 11),
				Window: shared.Window{Earliest: e2eAt(5, 15), Latest: e2eAt(6, 3)}, PolicyVersion: "v1"}, Reason: "DEPARTURE_CANCELLED"},
		promise.PromiseAtRisk{Header: e2eHeader(e2eAt(4, 11)), PromiseID: "D", OrderID: "ord-D", Reason: "DEPARTURE_CANCELLED", DetectedAt: e2eAt(4, 11)},
		promise.PromiseUnkeepable{Header: e2eHeader(e2eAt(4, 12)), PromiseID: "D", OrderID: "ord-D", GroupNo: 1, Reason: "DEPARTURE_CANCELLED"},
		promise.PromiseKept{Header: e2eHeader(e2eAt(5, 15)), PromiseID: "A", OrderID: "ord-A", NodeID: "N-1", DeliveredAt: e2eAt(5, 1), DeliverByLatest: e2eAt(5, 3)},
		e2eBroken("B", e2eAt(5, 16), promise.CauseLate, 120),
		e2eBroken("D", e2eAt(5, 17), promise.CauseLost, 0),
		// Z was made before the projector's retention: its judgement reports under UNKNOWN.
		e2eBroken("Z", e2eAt(5, 18), promise.CauseReturned, 0),
	}
}

// e2eReports is the reports server over a read-only pool, answering as of 11-06.
type e2eReports struct {
	t   *testing.T
	url string
}

const e2eSpan = "?from=2026-11-03&to=2026-11-05"

func (r e2eReports) days(path string) []map[string]any {
	r.t.Helper()
	var out struct {
		Days []map[string]any `json:"days"`
	}
	e2eGetJSON(r.t, r.url+path+e2eSpan, &out)
	return out.Days
}

func TestAnalyticsReadSide_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	brokers := kafkatest.Brokers(t)
	oltp := pgtest.NewPool(t)
	analyticalURL := e2eAnalyticalPostgres(t)
	if err := analyticsstore.RunMigrations(analyticalURL); err != nil {
		t.Fatal(err)
	}
	// The group joins only existing topics: create both own topics (and the DLQ) first.
	for _, topic := range append(kafka.OwnTopics(), kafka.AnalyticsDLQTopic) {
		kafkatest.CreateTopic(t, topic)
	}

	// ---- OLTP: the real encoder through the real Postgres outbox ----
	out := postgres.NewOutbox(oltp, outboundkafka.NewEncoder())
	if err := out.Append(ctx, e2eEvents()...); err != nil {
		t.Fatal(err)
	}

	// ---- the topics also carry messages the projector must survive ----
	legacy := []byte(`{"event_id":"e-1","event_type":"PromiseKept","payload":{"promiseId":"x"}}`)
	unknown := e2eHand(t, "evt-unknown", cloudevents.Type("promise", "PromiseTeleported"), "prm-U", e2eAt(3, 13), map[string]any{"promiseId": "prm-U"})
	poison := e2eHand(t, "evt-poison", cloudevents.Type("promise", "PromiseBroken"), "prm-P", e2eAt(3, 13),
		map[string]any{"promiseId": "prm-OTHER", "cause": "LOST", "lateByMinutes": 0}) // subject != aggregate id
	kafkatest.Produce(t, kafka.OwnAnalyticsTopic, kafkatest.Message{Key: "x", Value: legacy}, kafkatest.Message{Key: "prm-U", Value: unknown})
	kafkatest.Produce(t, kafka.OwnEventsTopic, kafkatest.Message{Key: "prm-P", Value: poison})

	// ---- relay -> real Kafka (with one lost ack: a republish under the SAME id) ----
	writer := outboundkafka.NewWriter(kafkatest.BrokerList(t))
	t.Cleanup(func() { _ = writer.Close() })
	r := relay.NewOutboxRelay(out, &e2eLostAckOnce{inner: outboundkafka.NewRelaySink(writer)}, quiet, relay.WithInterval(50*time.Millisecond))
	relayDone := make(chan struct{})
	go func() { defer close(relayDone); _ = r.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-relayDone })

	// ---- the projector: consumer + writer pool over the analytical database ----
	pool, err := analyticsstore.NewPool(ctx, analyticalURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	consumer := kafka.NewAnalyticsConsumer(brokers, kafka.OwnTopics(), kafka.AnalyticsDLQTopic,
		fmt.Sprintf("dp-projector-e2e-%d", time.Now().UnixNano()), analyticsstore.NewProjection(pool), quiet)
	consumer.Retry = kafka.RetryPolicy{Initial: 50 * time.Millisecond, Max: 200 * time.Millisecond}
	consumerDone := make(chan struct{})
	go func() { defer close(consumerDone); _ = consumer.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-consumerDone; _ = consumer.Close() })

	// ---- the reports over a READ-ONLY pool ----
	readOnly, err := analyticsstore.NewReadOnlyPool(ctx, analyticalURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(readOnly.Close)
	reader := analyticsstore.NewReader(readOnly)
	srv := httptest.NewServer(reportshttp.NewRouter(&reportshttp.Server{Reader: reader, Ping: reader, Now: func() time.Time { return e2eAt(6, 0) }}, quiet, "e2e"))
	t.Cleanup(srv.Close)
	reports := e2eReports{t: t, url: srv.URL}
	e2eWaitFor(t, 90*time.Second, "Z's PromiseBroken (the newest OLTP event) projected", func() bool {
		var f struct {
			AsOf *time.Time `json:"asOf"`
		}
		e2eGetJSON(t, srv.URL+"/reports/freshness", &f)
		return f.AsOf != nil && f.AsOf.Equal(e2eAt(5, 18)) && len(reports.days("/reports/promise-reliability")) == 4 &&
			len(reports.days("/reports/promise-precision")) == 2 && len(reports.days("/reports/quote-conversion")) == 3
	})

	e2eAssertReports(t, reports)
	e2eAssertOnlyThePoisonWasDeadLettered(t, brokers, poison)
	e2eAssertDuplicateIsANoOp(t, oltp, reports)
	pgtest.WaitAllPublished(t, oltp)
}

func e2eAssertReports(t *testing.T, reports e2eReports) {
	t.Helper()
	nullLate := map[string]any{"p50": nil, "p90": nil}
	causes := func(late, lost, returned float64) map[string]any {
		return map[string]any{"late": late, "lost": lost, "returned": returned}
	}
	wantReliability := []map[string]any{
		{"day": "2026-11-05", "tier": "NEXT_DAY", "basis": "MEASURED", "judged": 1.0, "kept": 0.0, "broken": 1.0, "keptRate": 0.0,
			"brokenByCause": causes(1, 0, 0), "lateByMinutes": map[string]any{"p50": 120.0, "p90": 120.0}},
		{"day": "2026-11-05", "tier": "NEXT_DAY", "basis": "SCHEDULED", "judged": 1.0, "kept": 1.0, "broken": 0.0, "keptRate": 1.0,
			"brokenByCause": causes(0, 0, 0), "lateByMinutes": nullLate},
		{"day": "2026-11-05", "tier": "STANDARD", "basis": "NOMINAL", "judged": 1.0, "kept": 0.0, "broken": 1.0, "keptRate": 0.0,
			"brokenByCause": causes(0, 1, 0), "lateByMinutes": nullLate},
		{"day": "2026-11-05", "tier": "UNKNOWN", "basis": "UNKNOWN", "judged": 1.0, "kept": 0.0, "broken": 1.0, "keptRate": 0.0,
			"brokenByCause": causes(0, 0, 1), "lateByMinutes": nullLate},
	}
	if got := reports.days("/reports/promise-reliability"); !reflect.DeepEqual(got, wantReliability) {
		t.Errorf("promise-reliability = %v\nwant %v", got, wantReliability)
	}
	// A (Chicago, one local day), B (no zone: read in UTC, two days, revised); D (two Chicago days, unkeepable).
	wantPrecision := []map[string]any{
		{"day": "2026-11-03", "tier": "NEXT_DAY", "promisesMade": 2.0, "windowMinutes": map[string]any{"mean": 720.0, "p50": 720.0, "p90": 720.0},
			"exactDayShare": 0.5, "zoneUnknown": 1.0, "revisedShare": 0.5, "unkeepableCount": 0.0},
		{"day": "2026-11-03", "tier": "STANDARD", "promisesMade": 1.0, "windowMinutes": map[string]any{"mean": 2160.0, "p50": 2160.0, "p90": 2160.0},
			"exactDayShare": 0.0, "zoneUnknown": 0.0, "revisedShare": 0.0, "unkeepableCount": 1.0},
	}
	if got := reports.days("/reports/promise-precision"); !reflect.DeepEqual(got, wantPrecision) {
		t.Errorf("promise-precision = %v\nwant %v", got, wantPrecision)
	}
	// off-A was the first outbox row, republished after the lost ack: still counted once.
	wantConversion := []map[string]any{
		{"day": "2026-11-03", "tier": "NEXT_DAY", "offersQuoted": 2.0, "promisesMade": 2.0, "conversion": 1.0},
		{"day": "2026-11-03", "tier": "SAME_DAY", "offersQuoted": 1.0, "promisesMade": 0.0, "conversion": 0.0},
		{"day": "2026-11-03", "tier": "STANDARD", "offersQuoted": 1.0, "promisesMade": 1.0, "conversion": 1.0},
	}
	if got := reports.days("/reports/quote-conversion"); !reflect.DeepEqual(got, wantConversion) {
		t.Errorf("quote-conversion = %v\nwant %v", got, wantConversion)
	}
}

// e2eAssertDuplicateIsANoOp redelivers A's PromiseKept (same id) and then a
// marker judgement: the marker proves the duplicate was consumed, and the kept
// count does not move.
func e2eAssertDuplicateIsANoOp(t *testing.T, oltp *pgtest.Pool, reports e2eReports) {
	t.Helper()
	var dup []byte
	if err := oltp.QueryRow(context.Background(), `SELECT value FROM outbox_events WHERE subject = 'A' AND event_type LIKE '%PromiseKept' LIMIT 1`).Scan(&dup); err != nil {
		t.Fatal(err)
	}
	marker := e2eHand(t, "evt-marker", cloudevents.Type("promise", "PromiseBroken"), "MARKER", e2eAt(5, 19),
		map[string]any{"promiseId": "MARKER", "cause": "RETURNED", "lateByMinutes": 0})
	kafkatest.Produce(t, kafka.OwnEventsTopic, kafkatest.Message{Key: "A", Value: dup}, kafkatest.Message{Key: "MARKER", Value: marker})
	e2eWaitFor(t, 60*time.Second, "the marker projected", func() bool {
		days := reports.days("/reports/promise-reliability")
		return len(days) == 4 && days[3]["broken"] == 2.0
	})
	if kept := reports.days("/reports/promise-reliability")[1]; kept["kept"] != 1.0 {
		t.Errorf("after a redelivered PromiseKept the NEXT_DAY/SCHEDULED row = %v, want still one kept (idempotent on the CloudEvents id)", kept)
	}
}

// e2eAssertOnlyThePoisonWasDeadLettered reads the DLQ: exactly the poison
// bytes, nothing for the legacy or unknown-type messages.
func e2eAssertOnlyThePoisonWasDeadLettered(t *testing.T, brokers []string, poison []byte) {
	t.Helper()
	dlq := kafkago.NewReader(kafkago.ReaderConfig{Brokers: brokers, Topic: kafka.AnalyticsDLQTopic, Partition: 0, MinBytes: 1, MaxBytes: 10e6})
	defer func() { _ = dlq.Close() }()
	readCtx, stop := context.WithTimeout(context.Background(), 60*time.Second)
	defer stop()
	m, err := dlq.ReadMessage(readCtx)
	if err != nil {
		t.Fatalf("nothing reached the DLQ: %v", err)
	}
	if !bytes.Equal(m.Value, poison) {
		t.Errorf("DLQ value differs from the poison message:\n%s", m.Value)
	}
	quietCtx, quietStop := context.WithTimeout(context.Background(), 3*time.Second)
	defer quietStop()
	if extra, err := dlq.ReadMessage(quietCtx); err == nil {
		t.Errorf("a second message reached the DLQ (legacy and unknown types are skipped, not dead-lettered): %s", extra.Value)
	}
}

func e2eHand(t *testing.T, id, typ, subject string, at time.Time, data map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]any{
		"specversion": "1.0", "id": id, "source": "/retailnetwork/delivery-promise", "type": typ, "subject": subject,
		"datacontenttype": "application/json", "time": at.Format(time.RFC3339), "data": data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func e2eGetJSON(t *testing.T, url string, into any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d %s", url, resp.StatusCode, body)
	}
	if err := json.Unmarshal(body, into); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

func e2eWaitFor(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(timeout); ; time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
	}
}
