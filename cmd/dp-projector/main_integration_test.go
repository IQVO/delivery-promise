//go:build integration

package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	inboundkafka "github.com/claudioed/delivery-promise/internal/adapters/inbound/kafka"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/analyticsstore"
	outboundkafka "github.com/claudioed/delivery-promise/internal/adapters/outbound/kafka"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
	"github.com/claudioed/delivery-promise/internal/testing/kafkatest"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

func TestMain(m *testing.M) {
	code := m.Run()
	kafkatest.Shutdown()
	pgtest.Shutdown()
	os.Exit(code)
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(90 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

// produceEncoded writes e, encoded by the real outbox encoder, to the topic the
// encoder routes it to, and returns that topic.
func produceEncoded(t *testing.T, e shared.Event) string {
	t.Helper()
	msg, err := outboundkafka.NewEncoder().Encode(e)
	if err != nil {
		t.Fatal(err)
	}
	kafkatest.Produce(t, msg.Topic, kafkatest.Message{Key: string(msg.Key), Value: msg.Value})
	return msg.Topic
}

// The real entry point: env -> embedded analytical migrations on an EMPTY
// database -> /readyz -> the consumer projects events the real encoder wrote
// to BOTH own topics -> a cancelled context drains to a clean exit.
func TestProjectorBootsMigratesProjectsAndStopsCleanly(t *testing.T) {
	url := pgtest.EmptyURL(t)
	brokers := kafkatest.Brokers(t)
	for _, topic := range inboundkafka.OwnTopics() {
		kafkatest.CreateTopic(t, topic)
	}
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	made := promise.PromiseMade{Header: shared.Header{Version: 1, At: at}, PromiseID: "prm-projector-1", OfferID: "off-1",
		Terms: promise.Terms{Tier: shared.TierNextDay, Basis: shared.BasisScheduled, ShipBy: at, PolicyVersion: "v1",
			Window: shared.Window{Earliest: at.Add(26 * time.Hour), Latest: at.Add(38 * time.Hour), TimeZone: "America/Chicago"}},
		Witness: shared.Witness{NodeID: "N-1"}, HoldExpiresAt: at.Add(15 * time.Minute), Lines: []shared.Line{{CatalogItemID: "ci-1", Quantity: 1}}}
	kept := promise.PromiseKept{Header: shared.Header{Version: 4, At: at.Add(30 * time.Hour)}, PromiseID: "prm-projector-1",
		OrderID: "ord-1", NodeID: "N-1", DeliveredAt: at.Add(29 * time.Hour), DeliverByLatest: at.Add(38 * time.Hour)}
	if produceEncoded(t, made) != inboundkafka.OwnAnalyticsTopic || produceEncoded(t, kept) != inboundkafka.OwnEventsTopic {
		t.Fatal("PromiseMade must travel on .analytics and PromiseKept on .events (ADR 0004)")
	}

	addr := freeAddr(t)
	env := map[string]string{"ANALYTICS_DATABASE_URL": url, "KAFKA_BROKERS": brokers[0], "ADMIN_ADDR": addr, "LOG_LEVEL": "error",
		"ANALYTICS_CONSUMER_GROUP": fmt.Sprintf("dp-projector-itest-%d", time.Now().UnixNano())}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx, func(k string) string { return env[k] }) }()
	t.Cleanup(cancel)

	waitUntil(t, "/readyz 200", func() bool {
		resp, err := http.Get("http://" + addr + "/readyz")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	pool, err := analyticsstore.NewPool(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	waitUntil(t, "the PromiseMade and the PromiseKept projected", func() bool {
		var n int
		err := pool.QueryRow(context.Background(), `SELECT count(*) FROM promises_made m JOIN promise_judgements j USING (promise_id)
			WHERE promise_id = 'prm-projector-1' AND m.tier = 'NEXT_DAY' AND m.exact_day AND j.verdict = 'KEPT'`).Scan(&n)
		return err == nil && n == 1
	})
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("a cancelled projector must drain cleanly, got %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("the projector did not stop")
	}
}
