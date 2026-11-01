//go:build integration

package main

// The real-wiring test boots run(ctx), the production entry point, against a
// real Postgres and a real Kafka broker (both testcontainers). It feeds the
// local read models ONLY by producing CloudEvents on the producers' topics,
// drives the service ONLY through REST and reads the result ONLY from Postgres
// and from the output topics. Nothing here is a double: the consumer, the use
// cases, the Postgres repositories, the Idempotency-Key middleware, the outbox
// relay, the encoders and the Kafka writer are the ones the binary runs.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	inboundkafka "github.com/claudioed/delivery-promise/internal/adapters/inbound/kafka"
	"github.com/claudioed/delivery-promise/internal/adapters/kafka/cloudevents"
	outboundkafka "github.com/claudioed/delivery-promise/internal/adapters/outbound/kafka"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/testing/kafkatest"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// service is the running binary under test.
type service struct {
	t      *testing.T
	base   string
	pool   *pgtest.Pool
	cancel context.CancelFunc
	done   chan error
	keys   int
	events int
}

type response struct {
	status int
	header http.Header
	body   []byte
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

// boot creates the input topics (the consumer joins its group on all of them),
// starts run(ctx) on a fresh database (no migration applied: the binary
// migrates at boot) and waits until /healthz answers. The OUTPUT topics are
// deliberately not created.
func boot(t *testing.T) *service {
	t.Helper()
	dbURL := pgtest.EmptyURL(t)
	for _, topic := range inboundkafka.Topics() {
		kafkatest.CreateTopic(t, topic)
	}
	addr := freeAddr(t)
	t.Setenv("DATABASE_URL", dbURL)
	kafkatest.SetBrokersEnv(t)
	t.Setenv("DELIVERY_PROMISE_CONSUMER_GROUP", "delivery-promise-e2e")
	t.Setenv("HTTP_ADDR", addr)
	t.Setenv("SHUTDOWN_DRAIN_DELAY", "0")
	t.Setenv("OUTBOX_RELAY_INTERVAL", "100ms")
	t.Setenv("SWEEP_INTERVAL", "200ms")
	t.Setenv("LOG_LEVEL", "error")

	ctx, cancel := context.WithCancel(context.Background())
	s := &service{t: t, base: "http://" + addr, cancel: cancel, done: make(chan error, 1)}
	go func() { s.done <- run(ctx) }()
	t.Cleanup(func() { cancel(); _ = s.waitStopped(60 * time.Second) })

	s.eventually(90*time.Second, "healthz", func() bool { return s.get("/healthz").status == http.StatusOK })
	s.pool = pgtest.OpenPool(t, dbURL)
	return s
}

func (s *service) waitStopped(timeout time.Duration) error {
	select {
	case err := <-s.done:
		s.done <- err
		return err
	case <-time.After(timeout):
		s.t.Errorf("run(ctx) did not return within %s of the cancellation", timeout)
		return nil
	}
}

func (s *service) eventually(timeout time.Duration, what string, ok func() bool) {
	s.t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			s.t.Fatalf("timed out after %s waiting for %s", timeout, what)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *service) call(method, path, body, key string) response {
	s.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, s.base+path, rd)
	if err != nil {
		s.t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return response{status: -1, body: []byte(err.Error())}
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(resp.Body)
	return response{status: resp.StatusCode, header: resp.Header, body: raw}
}

func (s *service) get(path string) response { return s.call(http.MethodGet, path, "", "") }

// write sends a state-changing request under a fresh key and requires the status.
func (s *service) write(path, body string, want int) map[string]any {
	s.t.Helper()
	s.keys++
	r := s.call(http.MethodPost, path, body, fmt.Sprintf("e2e-%d", s.keys))
	if r.status != want {
		s.t.Fatalf("POST %s = %d %s, want %d", path, r.status, r.body, want)
	}
	return decode(s.t, r.body)
}

func decode(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("not JSON: %s", raw)
	}
	return out
}

func (s *service) count(query string, args ...any) int {
	s.t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), query, args...).Scan(&n); err != nil {
		s.t.Fatalf("%s: %v", query, err)
	}
	return n
}

func str(m map[string]any, k string) string { v, _ := m[k].(string); return v }

// produce publishes one foreign CloudEvent (structured mode) on a producer topic.
func (s *service) produce(topic, typ string, data any) {
	s.t.Helper()
	s.events++
	e := ce.New()
	e.SetID(fmt.Sprintf("00000000-0000-4000-8000-%012d", s.events))
	e.SetSource("/retailnetwork/e2e")
	e.SetType(typ)
	e.SetSubject(fmt.Sprintf("subject-%d", s.events))
	e.SetTime(time.Now().UTC())
	if err := e.SetData("application/json", data); err != nil {
		s.t.Fatal(err)
	}
	raw, err := json.Marshal(e)
	if err != nil {
		s.t.Fatal(err)
	}
	kafkatest.Produce(s.t, topic, kafkatest.Message{Key: e.Subject(), Value: raw})
}

// network is the topology the producers announce: one fulfilment centre that
// stocks the item, one sort centre, one delivery station, two lanes, a pickup
// at the fulfilment centre, a linehaul and a delivery wave, all relative to now
// so the scenario holds at any wall-clock time.
type network struct {
	pickup, linehaul, waveStart, waveEnd time.Time
	pickupID                             string
}

func hours(start, end string) usecases.HoursDTO { return usecases.HoursDTO{Start: start, End: end} }

func allDays(h usecases.HoursDTO) map[string]usecases.HoursDTO {
	m := map[string]usecases.HoursDTO{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
		m[d] = h
	}
	return m
}

func announceNetwork(s *service) network {
	now := time.Now().UTC().Truncate(time.Minute)
	n := network{
		pickup: now.Add(3 * time.Hour), linehaul: now.Add(5 * time.Hour),
		waveStart: now.Add(9 * time.Hour), waveEnd: now.Add(21 * time.Hour), pickupID: "dep-FC-pickup",
	}
	topo := func(entity, name string) string {
		return fmt.Sprintf("com.retailnetwork.logistics.node-topology.%s.%s", entity, name)
	}
	const tz = "UTC"
	s.produce(inboundkafka.NodeTopologyTopic, topo("region", "RegionDefined"), usecases.RegionDefined{RegionID: "R-SC", Code: "SC", Name: "South Central"})
	for _, node := range []struct{ id, typ, subtype string }{{"N-FC", "FULFILLMENT_CENTER", "SORTABLE"}, {"N-SC", "SORTATION_CENTER", ""}, {"N-DS", "DELIVERY_STATION", ""}} {
		s.produce(inboundkafka.NodeTopologyTopic, topo("node", "NodeRegistered"), usecases.NodeRegistered{
			NodeID: node.id, NodeType: node.typ, Subtype: node.subtype, Operator: "VENDOR", RegionID: "R-SC", Timezone: tz,
		})
	}
	s.produce(inboundkafka.NodeTopologyTopic, topo("node", "NodeOperationalProfileDeclared"), usecases.NodeProfileDeclared{
		NodeID: "N-FC", ProfileRevision: 1, Timezone: tz, OperatingHoursByDay: allDays(hours("00:00", "23:59")), Holidays: []string{},
		HandlingTimeMinutes: 5, Cutoffs: []usecases.CutoffDTO{{LocalTime: n.pickup.Format("15:04"), MaxOrders: 50}},
		ThroughputCap: usecases.ThroughputCapDTO{Value: 400, TimeUnit: "HOUR"},
	})
	s.produce(inboundkafka.NodeTopologyTopic, topo("node", "NodeOperationalProfileDeclared"), usecases.NodeProfileDeclared{
		NodeID: "N-SC", ProfileRevision: 1, Timezone: tz, OperatingHoursByDay: allDays(hours("00:00", "23:59")), Holidays: []string{},
		Cutoffs: []usecases.CutoffDTO{}, ThroughputCap: usecases.ThroughputCapDTO{Value: 9999, TimeUnit: "HOUR"}, SortDwellMinutes: 30,
	})
	for _, id := range []string{"N-FC", "N-SC", "N-DS"} {
		s.produce(inboundkafka.NodeTopologyTopic, topo("node", "NodeActivated"), usecases.NodeStateChanged{NodeID: id})
	}
	s.produce(inboundkafka.NodeTopologyTopic, topo("lane", "LaneOpened"), usecases.LaneOpened{LaneID: "lane-1", OriginNodeID: "N-FC", DestinationNodeID: "N-SC", NominalTransitMinutes: 60})
	s.produce(inboundkafka.NodeTopologyTopic, topo("lane", "LaneOpened"), usecases.LaneOpened{LaneID: "lane-2", OriginNodeID: "N-SC", DestinationNodeID: "N-DS", NominalTransitMinutes: 90})
	s.produce(inboundkafka.NodeTopologyTopic, topo("servicearea", "ServiceAreaAssigned"), usecases.ServiceAreaAssigned{PostalPrefix: "787", DeliveryStationNodeID: "N-DS", RegionID: "R-SC", AssignmentVersion: 1})

	avail := func(entity, name string) string {
		return fmt.Sprintf("com.retailnetwork.commerce.offer-availability.%s.%s", entity, name)
	}
	s.produce(inboundkafka.OfferAvailabilityTopic, avail("catalogitem", "CatalogItemCreated"), usecases.CatalogItemChanged{CatalogItemID: "ci-1001"})
	s.produce(inboundkafka.OfferAvailabilityTopic, avail("listing", "ListingCreated"), usecases.ListingCreated{ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-FC"})
	s.produce(inboundkafka.OfferAvailabilityTopic, avail("listing", "ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-FC", AvailableQty: 37, AsOf: time.Now().UTC(),
	})

	lh := func(name string) string {
		return "com.retailnetwork.logistics.linehaul-transportation.departure." + name
	}
	s.produce(inboundkafka.LinehaulTopic, lh("DepartureScheduled"), usecases.DepartureScheduled{
		DepartureID: n.pickupID, LaneID: "lane-1", Kind: "PICKUP", OriginNodeID: "N-FC", DestinationNodeID: "N-SC",
		CutoffAt: n.pickup, DepartsAt: n.pickup.Add(15 * time.Minute), ArrivesAt: n.pickup.Add(75 * time.Minute), CapacityPackages: 40,
	})
	s.produce(inboundkafka.LinehaulTopic, lh("DepartureScheduled"), usecases.DepartureScheduled{
		DepartureID: "dep-SC-DS", LaneID: "lane-2", Kind: "LINEHAUL", OriginNodeID: "N-SC", DestinationNodeID: "N-DS",
		CutoffAt: n.linehaul, DepartsAt: n.linehaul.Add(15 * time.Minute), ArrivesAt: n.linehaul.Add(2 * time.Hour), CapacityPackages: 140,
	})
	s.produce(inboundkafka.LastMileTopic, "com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled", usecases.WaveScheduled{
		WaveID: "W-DS-1", StationNodeID: "N-DS", InductCutoffAt: now.Add(8 * time.Hour), WindowStart: n.waveStart, WindowEnd: n.waveEnd, StopCapacity: 320,
	})
	return n
}

const quoteBody = `{"destinationPostalPrefix":"787","lines":[{"catalogItemId":"ci-1001","quantity":2}],"tiers":["NEXT_DAY"]}`

// quoteWhenProjected quotes until the consumed events made the path feasible.
func quoteWhenProjected(s *service) map[string]any {
	s.t.Helper()
	var offers []any
	var last response
	s.eventually(90*time.Second, "a NEXT_DAY offer once the read models are projected", func() bool {
		// A fresh key per attempt: a replay would repeat the first, unprojected answer.
		last = s.call(http.MethodPost, "/delivery-quotes", quoteBody, fmt.Sprintf("quote-%d", time.Now().UnixNano()))
		if last.status != http.StatusOK {
			return false
		}
		offers, _ = decode(s.t, last.body)["offers"].([]any)
		return len(offers) == 1
	})
	return offers[0].(map[string]any)
}

func TestRealWiring(t *testing.T) {
	s := boot(t)

	t.Run("ready while the output topics do not exist", func(t *testing.T) {
		r := s.get("/readyz")
		if r.status != http.StatusOK || strings.TrimSpace(string(r.body)) != `{"status":"ready"}` {
			t.Fatalf("/readyz = %d %s, want 200 ready", r.status, r.body)
		}
		if n := kafkatest.Count(t, outboundkafka.TopicEvents) + kafkatest.Count(t, outboundkafka.TopicAnalytics); n != 0 {
			t.Fatalf("the output topics already hold %d messages before the first write", n)
		}
	})

	var nw network
	var promiseID string
	t.Run("producers announce the network", func(t *testing.T) { nw = announceNetwork(s) })
	t.Run("quote, promise and confirm over REST", func(t *testing.T) { promiseID = restFlow(t, s, nw) })
	t.Run("the order is shipped and delivered over Kafka", func(t *testing.T) { deliver(t, s, promiseID) })
	t.Run("Postgres rows", func(t *testing.T) { assertRows(t, s) })
	t.Run("idempotent replay", func(t *testing.T) { assertReplay(t, s, promiseID) })
	t.Run("redelivered events change nothing", func(t *testing.T) { assertInbox(t, s) })
	t.Run("exact CloudEvents on the topics", func(t *testing.T) { assertTopics(t, s) })
	t.Run("cancel route", func(t *testing.T) { assertCancelRoute(t, s, promiseID) })
	t.Run("Kafka down", func(t *testing.T) { assertKafkaDown(t, s) })
}

// assertCancelRoute drives POST /promises/{id}/cancellations over REST: a KEPT promise
// is refused (409 illegal-transition), an unknown id is 404, the key is
// required, and a freshly confirmed promise is cancelled (200 CANCELLED, the
// confirmed hold given back, one PromiseCancelled row) and a repeat is a 200
// no-op that appends nothing.
func assertCancelRoute(t *testing.T, s *service, keptID string) {
	t.Helper()
	if r := s.call(http.MethodPost, "/promises/"+keptID+"/cancellations", "", "cancel-kept"); r.status != http.StatusConflict || !strings.Contains(string(r.body), "illegal-transition") {
		t.Fatalf("cancel of a KEPT promise = %d %s, want 409 illegal-transition", r.status, r.body)
	}
	if r := s.call(http.MethodPost, "/promises/prm-nope/cancellations", "", "cancel-missing"); r.status != http.StatusNotFound || !strings.Contains(string(r.body), "not-found") {
		t.Fatalf("cancel of an unknown promise = %d %s, want 404 not-found", r.status, r.body)
	}
	if r := s.call(http.MethodPost, "/promises/"+keptID+"/cancellations", "", ""); r.status != http.StatusBadRequest {
		t.Fatalf("cancel without Idempotency-Key = %d, want 400", r.status)
	}

	offer := quoteWhenProjected(s)
	made := s.write("/promises", `{"offerId":"`+str(offer, "offerId")+`","lines":[{"catalogItemId":"ci-1001","quantity":2}]}`, http.StatusCreated)
	id := str(made, "promiseId")
	s.write("/promises/"+id+"/confirmations", `{"orderId":"ord-cancel","groupNo":1}`, http.StatusOK)
	confirmed := s.count(`SELECT COALESCE(sum((snapshot->>'Confirmed')::int), 0) FROM promise_books`)

	cancelled := decode(t, s.call(http.MethodPost, "/promises/"+id+"/cancellations", "", "cancel-1").body)
	if str(cancelled, "state") != "CANCELLED" || str(cancelled, "promiseId") != id || str(cancelled, "cancelReason") != usecases.CancelReasonRequested {
		t.Fatalf("cancelled = %v", cancelled)
	}
	if got := s.count(`SELECT COALESCE(sum((snapshot->>'Confirmed')::int), 0) FROM promise_books`); got != confirmed-1 {
		t.Fatalf("confirmed holds %d -> %d, want one given back", confirmed, got)
	}
	rows := s.count(`SELECT count(*) FROM outbox_events WHERE event_type LIKE '%.PromiseCancelled'`)
	if rows != 1 {
		t.Fatalf("PromiseCancelled outbox rows = %d, want 1", rows)
	}
	again := s.call(http.MethodPost, "/promises/"+id+"/cancellations", "", "cancel-2")
	if again.status != http.StatusOK || str(decode(t, again.body), "state") != "CANCELLED" {
		t.Fatalf("repeat cancel = %d %s, want 200 CANCELLED", again.status, again.body)
	}
	replay := s.call(http.MethodPost, "/promises/"+id+"/cancellations", "", "cancel-1")
	if replay.status != http.StatusOK || replay.header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("replay of the first key = %d replayed=%q", replay.status, replay.header.Get("Idempotency-Replayed"))
	}
	if got := s.count(`SELECT count(*) FROM outbox_events WHERE event_type LIKE '%.PromiseCancelled'`); got != rows {
		t.Fatalf("a repeat appended PromiseCancelled rows: %d -> %d", rows, got)
	}
}

func restFlow(t *testing.T, s *service, nw network) string {
	t.Helper()
	offer := quoteWhenProjected(s)
	witness, _ := offer["witnessPath"].(map[string]any)
	if str(offer, "tier") != "NEXT_DAY" || str(offer, "state") != "QUOTED" || str(offer, "basis") != "SCHEDULED" ||
		str(witness, "nodeId") != "N-FC" || str(witness, "pickupDepartureId") != nw.pickupID || str(witness, "deliveryWaveId") != "W-DS-1" {
		t.Fatalf("offer = %v", offer)
	}
	if g := s.get("/delivery-offers/" + str(offer, "offerId")); g.status != http.StatusOK {
		t.Fatalf("GET offer = %d %s", g.status, g.body)
	}

	made := s.write("/promises", `{"offerId":"`+str(offer, "offerId")+`","lines":[{"catalogItemId":"ci-1001","quantity":2}]}`, http.StatusCreated)
	promiseID := str(made, "promiseId")
	if str(made, "state") != "PROVISIONAL" || str(made, "offerId") != str(offer, "offerId") {
		t.Fatalf("promise = %v", made)
	}
	confirmed := s.write("/promises/"+promiseID+"/confirmations", `{"orderId":"ord-1","groupNo":1}`, http.StatusOK)
	if str(confirmed, "state") != "CONFIRMED" || str(confirmed, "orderId") != "ord-1" {
		t.Fatalf("confirmed = %v", confirmed)
	}
	if r := s.get("/promises?orderId=ord-1"); r.status != http.StatusOK || !strings.Contains(string(r.body), promiseID) {
		t.Fatalf("GET promises by order = %d %s", r.status, r.body)
	}
	day := nw.pickup.Format("2006-01-02")
	if r := s.get("/promise-books?nodeId=N-FC&date=" + day); r.status != http.StatusOK || !strings.Contains(string(r.body), `"confirmed":1`) {
		t.Fatalf("GET books = %d %s", r.status, r.body)
	}
	return promiseID
}

func deliver(t *testing.T, s *service, promiseID string) {
	t.Helper()
	ordering := func(name string) string { return "com.retailnetwork.commerce.order-sourcing.assignment." + name }
	s.produce(inboundkafka.OrderSourcingTopic, ordering("FulfillmentLocked"), usecases.FulfillmentLocked{AssignmentID: "asg-1", OrderID: "ord-1", GroupNo: 1, PromiseID: promiseID, NodeID: "N-FC"})
	s.produce(inboundkafka.OrderSourcingTopic, ordering("FulfillmentShipped"), usecases.FulfillmentShipped{AssignmentID: "asg-1", TrackingNumbers: []string{"TRK-1", "TRK-2"}})
	st := func(name string) string { return "com.retailnetwork.logistics.shipping-and-tracking.journey." + name }
	s.produce(inboundkafka.ShippingTrackingTopic, st("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-1", AssignmentID: "asg-1", DeliveredAt: time.Now().UTC()})
	s.eventually(30*time.Second, "the first package to be recorded", func() bool {
		return s.count(`SELECT count(*) FROM rm_outcomes`) == 1
	})
	if r := decode(t, s.get("/promises/"+promiseID).body); str(r, "state") != "CONFIRMED" {
		t.Fatalf("one of two packages delivered: promise is %s, want CONFIRMED", str(r, "state"))
	}
	s.produce(inboundkafka.ShippingTrackingTopic, st("PackageDelivered"), usecases.PackageDelivered{TrackingNumber: "TRK-2", AssignmentID: "asg-1", DeliveredAt: time.Now().UTC()})
	s.eventually(30*time.Second, "the promise to be KEPT", func() bool {
		return str(decode(t, s.get("/promises/"+promiseID).body), "state") == "KEPT"
	})
}

func assertRows(t *testing.T, s *service) {
	t.Helper()
	want := map[string]int{
		"delivery_offers": 1, "promises": 1, "promise_books": 1, "idempotency_keys": 2,
		"rm_nodes": 3, "rm_lanes": 2, "rm_departures": 2, "rm_waves": 1, "rm_assignments": 1, "rm_shipments": 1, "rm_outcomes": 2,
	}
	for table, n := range want {
		if got := s.count(`SELECT count(*) FROM ` + table); got != n {
			t.Errorf("%s holds %d rows, want %d", table, got, n)
		}
	}
	// 3 + 3 + 3 + 1 node/region events, 2 lanes, 1 area, 3 availability, 2 departures, 1 wave, locked, shipped, 2 delivered
	if got := s.count(`SELECT count(*) FROM processed_events`); got != 22 {
		t.Errorf("processed_events holds %d rows, want 22 (one per consumed CloudEvents id)", got)
	}
	var state, bookState, order string
	if err := s.pool.QueryRow(context.Background(), `SELECT state, order_id FROM promises`).Scan(&state, &order); err != nil || state != "KEPT" || order != "ord-1" {
		t.Errorf("promise row: state %q order %q err %v", state, order, err)
	}
	if err := s.pool.QueryRow(context.Background(), `SELECT state FROM promise_books WHERE id = 'N-FC:dep-FC-pickup'`).Scan(&bookState); err != nil || bookState != "OPEN" {
		t.Errorf("book row: state %q err %v", bookState, err)
	}
	// DeliveryOfferQuoted per quote poll that found an offer, PromiseMade, PromiseConfirmed, PromiseKept.
	for typ, atLeast := range map[string]int{"PromiseMade": 1, "PromiseConfirmed": 1, "PromiseKept": 1, "DeliveryOfferQuoted": 1} {
		if got := s.count(`SELECT count(*) FROM outbox_events WHERE event_type LIKE '%.' || $1`, typ); got < atLeast {
			t.Errorf("outbox holds %d %s rows, want at least %d", got, typ, atLeast)
		}
	}
	if n := s.count(`SELECT count(*) FROM outbox_events WHERE event_type LIKE '%.PromiseKept'`); n != 1 {
		t.Errorf("PromiseKept outbox rows = %d, want exactly 1", n)
	}
}

func assertReplay(t *testing.T, s *service, promiseID string) {
	t.Helper()
	body := `{"orderId":"ord-1","groupNo":1}`
	first := s.call(http.MethodPost, "/promises/"+promiseID+"/confirmations", body, "replay-1")
	second := s.call(http.MethodPost, "/promises/"+promiseID+"/confirmations", body, "replay-1")
	if first.status != second.status || !bytes.Equal(first.body, second.body) || second.header.Get("Idempotency-Replayed") != "true" {
		t.Fatalf("first %d %s, replay %d %s", first.status, first.body, second.status, second.body)
	}
	before := s.count(`SELECT count(*) FROM outbox_events`)
	if r := s.call(http.MethodPost, "/promises/"+promiseID+"/confirmations", `{"orderId":"ord-2","groupNo":1}`, "replay-1"); r.status != http.StatusConflict ||
		!strings.Contains(string(r.body), "idempotency-key-reused") {
		t.Fatalf("conflicting body = %d %s, want 409 idempotency-key-reused", r.status, r.body)
	}
	if r := s.call(http.MethodPost, "/promises/"+promiseID+"/confirmations", body, ""); r.status != http.StatusBadRequest {
		t.Fatalf("no key = %d, want 400", r.status)
	}
	if got := s.count(`SELECT count(*) FROM outbox_events`); got != before {
		t.Fatalf("a replayed or refused write appended outbox rows: %d -> %d", before, got)
	}
}

// assertInbox republishes an already processed event with the SAME CloudEvents
// id: the inbox must swallow it.
func assertInbox(t *testing.T, s *service) {
	t.Helper()
	var id string
	if err := s.pool.QueryRow(context.Background(), `SELECT event_id FROM processed_events WHERE event_type LIKE '%PackageDelivered' LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.pool.QueryRow(context.Background(), `SELECT data::text FROM rm_outcomes WHERE tracking_number = 'TRK-1'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	e := ce.New()
	e.SetID(id)
	e.SetSource("/retailnetwork/e2e")
	e.SetType("com.retailnetwork.logistics.shipping-and-tracking.journey.PackageDelivered")
	e.SetSubject("again")
	e.SetTime(time.Now().UTC())
	_ = e.SetData("application/json", usecases.PackageDelivered{TrackingNumber: "TRK-1", AssignmentID: "asg-1", DeliveredAt: time.Now().UTC().Add(48 * time.Hour)})
	raw, _ := json.Marshal(e)
	kafkatest.Produce(t, inboundkafka.ShippingTrackingTopic, kafkatest.Message{Key: "again", Value: raw})
	// A marker event produced after it is processed once the duplicate was consumed.
	s.produce(inboundkafka.ShippingTrackingTopic, "com.retailnetwork.logistics.shipping-and-tracking.journey.PackageLost", usecases.PackageLost{TrackingNumber: "TRK-unknown", DeclaredAt: time.Now().UTC()})
	s.eventually(30*time.Second, "the marker event to be processed", func() bool {
		return s.count(`SELECT count(*) FROM processed_events WHERE event_type LIKE '%PackageLost'`) == 1
	})
	var after string
	if err := s.pool.QueryRow(context.Background(), `SELECT data::text FROM rm_outcomes WHERE tracking_number = 'TRK-1'`).Scan(&after); err != nil || after != before {
		t.Fatalf("the redelivered duplicate overwrote an outcome: %s -> %s (%v)", before, after, err)
	}
	if n := s.count(`SELECT count(*) FROM processed_events WHERE event_id = $1`, id); n != 1 {
		t.Fatalf("the event id is recorded %d times", n)
	}
}

type outboxRow struct {
	eventID, typ, subj string
	value              []byte
}

func outboxRows(t *testing.T, s *service, topic string) []outboxRow {
	t.Helper()
	rows, err := s.pool.Query(context.Background(), `SELECT event_id, event_type, subject, value FROM outbox_events WHERE topic = $1 ORDER BY id`, topic)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []outboxRow
	for rows.Next() {
		var r outboxRow
		if err := rows.Scan(&r.eventID, &r.typ, &r.subj, &r.value); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func assertTopics(t *testing.T, s *service) {
	t.Helper()
	s.eventually(60*time.Second, "all outbox rows published", func() bool {
		return s.count(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`) == 0
	})
	for _, c := range []struct {
		topic, stream string
		types         []string
	}{
		{outboundkafka.TopicEvents, "events", []string{"PromiseKept"}},
		{outboundkafka.TopicAnalytics, "analytics", []string{"DeliveryOfferQuoted", "PromiseMade", "PromiseConfirmed"}},
	} {
		rows := outboxRows(t, s, c.topic)
		got := kafkatest.ReadN(t, c.topic, len(rows), 90*time.Second)
		for i, msg := range got {
			assertMessage(t, msg, rows[i], c.stream)
		}
		if n := kafkatest.Count(t, c.topic); int(n) != len(rows) {
			t.Fatalf("%s holds %d messages, want exactly %d", c.topic, n, len(rows))
		}
		seen := map[string]bool{}
		for _, r := range rows {
			seen[r.typ[strings.LastIndex(r.typ, ".")+1:]] = true
		}
		for _, name := range c.types {
			if !seen[name] {
				t.Errorf("%s carries no %s (has %v)", c.topic, name, seen)
			}
		}
		if len(seen) != len(c.types) {
			t.Errorf("%s carries %v, want exactly %v", c.topic, seen, c.types)
		}
	}
	assertKept(t, s)
}

// onlyTraceExtras reports that, besides content-type, the headers are at most
// the W3C trace context the outbox stamps (ADR 0009); no other header may appear.
func onlyTraceExtras(headers map[string]string) bool {
	for k := range headers {
		if k != "content-type" && k != "traceparent" && k != "tracestate" {
			return false
		}
	}
	return true
}

func assertMessage(t *testing.T, msg kafkatest.Message, row outboxRow, stream string) {
	t.Helper()
	e, err := cloudevents.Decode(msg.Value)
	if err != nil {
		t.Fatalf("%s: %v", row.typ, err)
	}
	name := row.typ[strings.LastIndex(row.typ, ".")+1:]
	switch {
	case !bytes.Equal(msg.Value, row.value):
		t.Errorf("%s: the message is not the stored outbox bytes", name)
	case e.Type() != row.typ || e.ID() != row.eventID || e.Subject() != row.subj || msg.Key != row.subj:
		t.Errorf("%s: type %q id %q subject %q key %q, want the outbox row %+v", name, e.Type(), e.ID(), e.Subject(), msg.Key, row)
	case !strings.HasPrefix(e.Type(), "com.retailnetwork.commerce.delivery-promise.") || e.Source() != "/retailnetwork/delivery-promise" ||
		e.DataContentType() != "application/json" || e.SpecVersion() != "1.0" ||
		e.DataSchema() != "urn:retailnetwork:delivery-promise:"+stream+":"+name+":v1":
		t.Errorf("%s: attributes type %q source %q dct %q dataschema %q spec %q", name, e.Type(), e.Source(), e.DataContentType(), e.DataSchema(), e.SpecVersion())
	case msg.Headers["content-type"] != cloudevents.MediaType || !onlyTraceExtras(msg.Headers):
		t.Errorf("%s: headers %v", name, msg.Headers)
	case e.Time().IsZero() || e.Time().Location() != time.UTC:
		t.Errorf("%s: time %v is not a UTC instant", name, e.Time())
	}
}

func assertKept(t *testing.T, s *service) {
	t.Helper()
	rows := outboxRows(t, s, outboundkafka.TopicEvents)
	e, err := cloudevents.Decode(rows[0].value)
	if err != nil {
		t.Fatal(err)
	}
	var data map[string]any
	if err := json.Unmarshal(e.Data(), &data); err != nil {
		t.Fatal(err)
	}
	if e.Type() != "com.retailnetwork.commerce.delivery-promise.promise.PromiseKept" || str(data, "orderId") != "ord-1" || str(data, "nodeId") != "N-FC" ||
		str(data, "deliveredAt") == "" || str(data, "deliverByLatest") == "" || str(data, "promiseId") == "" {
		t.Fatalf("PromiseKept = %s %v", e.Type(), data)
	}
}

// assertKafkaDown stops the broker: /readyz must answer 503
// dependency-unavailable (the metadata check dials a fresh connection, so a
// cached answer cannot hide the outage), /healthz stays 200, a write is still
// accepted (the outbox decouples it from Kafka) and the process shuts down
// cleanly.
func assertKafkaDown(t *testing.T, s *service) {
	t.Helper()
	kafkatest.Stop(t)

	var last response
	s.eventually(60*time.Second, "/readyz 503", func() bool {
		last = s.get("/readyz")
		return last.status == http.StatusServiceUnavailable
	})
	if !strings.HasPrefix(last.header.Get("Content-Type"), "application/problem+json") || !strings.Contains(string(last.body), "dependency-unavailable") ||
		!strings.Contains(string(last.body), "kafka") || strings.Contains(string(last.body), "postgres") {
		t.Fatalf("/readyz = %d %s, want 503 dependency-unavailable naming only kafka", last.status, last.body)
	}
	if h := s.get("/healthz"); h.status != http.StatusOK {
		t.Fatalf("/healthz = %d with Kafka down, want 200 (liveness)", h.status)
	}
	before := s.count(`SELECT count(*) FROM outbox_events`)
	s.write("/promise-policies", `{"offerTtlMinutes":10,"holdTtlMinutes":15,"packagesPerOrder":1,"capacitySharePercent":100,"orderDeadlines":[]}`, http.StatusCreated)
	if got := s.count(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`); got < 1 || s.count(`SELECT count(*) FROM outbox_events`) != before+1 {
		t.Fatalf("a write with Kafka down left %d unpublished outbox rows, want it queued", got)
	}

	s.cancel()
	if err := s.waitStopped(60 * time.Second); err != nil {
		t.Fatalf("run(ctx) returned %v on a cancellation, want a clean shutdown", err)
	}
	if r := s.get("/healthz"); r.status != -1 {
		t.Fatalf("the listener still answers after the shutdown: %d", r.status)
	}
}
