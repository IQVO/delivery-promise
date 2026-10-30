//go:build integration

package main

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"

	inbounda2a "github.com/claudioed/delivery-promise/internal/adapters/inbound/a2a"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// fixedClock freezes time at the worked-example instant, so the seed's
// departures and waves are still in the future the way they were authored.
type fixedClock struct{ now time.Time }

func (c fixedClock) Now() time.Time { return c.now }

func TestMain(m *testing.M) {
	code := m.Run()
	pgtest.Shutdown()
	os.Exit(code)
}

// The binary migrates a database itself (idempotently: pgtest's databases are
// already migrated, a second run must be a no-op), then answers both skills
// over the REAL router and JSON-RPC wire from exactly what the write use cases
// persisted in Postgres, and its reads leave the outbox untouched: the
// delegated goal is a read, so no CloudEvent is raised and nothing is waiting
// for a relay.
func TestA2AAgentAgainstPostgres_ReadsWhatTheAPIWroteAndNeverWrites(t *testing.T) {
	url := pgtest.URL(t) // a fresh, already migrated database
	ctx := context.Background()

	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	outbox := func() (total, unpublished int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE published_at IS NULL) FROM outbox_events`).Scan(&total, &unpublished); err != nil {
			t.Fatalf("count outbox_events: %v", err)
		}
		return total, unpublished
	}

	promiseID := seedThroughWriteUseCases(t, url)
	totalBefore, unpublishedBefore := outbox()
	if totalBefore < 2 {
		t.Fatalf("outbox rows after seeding = %d, want the policy publication and the promise events", totalBefore)
	}

	deps, closeFn, err := buildDeps(ctx, quietLogger(), url, url)
	if err != nil {
		t.Fatalf("buildDeps: %v", err)
	}
	t.Cleanup(closeFn)

	srv := httptest.NewServer(newRouter(inbounda2a.Handler(inbounda2a.NewExecutor(deps)), testCard(), quietLogger(), "delivery-promise-a2a"))
	t.Cleanup(srv.Close)
	card := inbounda2a.Card(inbounda2a.Config{AgentURL: srv.URL + "/a2a", Version: "it"})
	client, err := a2aclient.NewFromCard(ctx, card)
	if err != nil {
		t.Fatalf("NewFromCard: %v", err)
	}

	assertSeededReads(t, client, promiseID)

	totalAfter, unpublishedAfter := outbox()
	if totalAfter != totalBefore || unpublishedAfter != unpublishedBefore {
		t.Fatalf("outbox rows %d (%d unpublished) -> %d (%d unpublished): the A2A binary wrote an event",
			totalBefore, unpublishedBefore, totalAfter, unpublishedAfter)
	}
}

// seedThroughWriteUseCases projects the worked-example network through the
// consumer use cases and drives quote, make and confirm the way cmd/api and
// checkout do (same use cases, Postgres adapters, transactional outbox). It
// returns the id of the promise it confirmed (ids.UUID generates random ids).
func seedThroughWriteUseCases(t *testing.T, url string) (promiseID string) {
	t.Helper()
	ctx := context.Background()
	pool, err := postgres.NewPool(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	now := time.Date(2026, 10, 22, 14, 5, 0, 0, time.FixedZone("CDT", -5*3600)).UTC()
	svc := buildServiceWithClock(pool, quietLogger(), fixedClock{now})

	at := func(day, h, m int) time.Time {
		return time.Date(2026, 10, day, h, m, 0, 0, time.FixedZone("CDT", -5*3600)).UTC()
	}
	n := 0
	meta := func(typ string) usecases.Meta {
		n++
		return usecases.Meta{ID: fmt.Sprintf("evt-it-%04d", n), Type: typ, Time: now}
	}
	allWeek := func() map[string]usecases.HoursDTO {
		m := map[string]usecases.HoursDTO{}
		for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
			m[d] = usecases.HoursDTO{Start: "06:00", End: "22:00"}
		}
		return m
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}

	must(svc.OnNodeRegistered(ctx, meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: "N-DAU1", NodeType: "DELIVERY_STATION", Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago",
	}))
	must(svc.OnNodeRegistered(ctx, meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: "N-SAU1", NodeType: "SORTATION_CENTER", Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago",
	}))
	must(svc.OnNodeRegistered(ctx, meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: "N-AUS3", NodeType: "FULFILLMENT_CENTER", Subtype: "SORTABLE", Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago",
	}))
	must(svc.OnNodeProfileDeclared(ctx, meta("NodeOperationalProfileDeclared"), usecases.NodeProfileDeclared{
		NodeID: "N-AUS3", ProfileRevision: 1, Timezone: "America/Chicago", OperatingHoursByDay: allWeek(),
		HandlingTimeMinutes: 95, Cutoffs: []usecases.CutoffDTO{{LocalTime: "11:00"}, {LocalTime: "18:00", MaxOrders: 2500}},
		ThroughputCap: usecases.ThroughputCapDTO{Value: 400, TimeUnit: "HOUR"},
	}))
	must(svc.OnNodeStateChanged(ctx, meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, "ACTIVE"))
	must(svc.OnNodeStateChanged(ctx, meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-SAU1"}, "ACTIVE"))
	must(svc.OnNodeStateChanged(ctx, meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-DAU1"}, "ACTIVE"))
	must(svc.OnRegionDefined(ctx, meta("RegionDefined"), usecases.RegionDefined{RegionID: "R-SC", Code: "SC", Name: "South Central"}))
	must(svc.OnServiceAreaAssigned(ctx, meta("ServiceAreaAssigned"), usecases.ServiceAreaAssigned{
		PostalPrefix: "787", DeliveryStationNodeID: "N-DAU1", RegionID: "R-SC", AssignmentVersion: 1,
	}))
	must(svc.OnLaneOpened(ctx, meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-1", OriginNodeID: "N-AUS3", DestinationNodeID: "N-SAU1", NominalTransitMinutes: 75}))
	must(svc.OnLaneOpened(ctx, meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-2", OriginNodeID: "N-SAU1", DestinationNodeID: "N-DAU1", NominalTransitMinutes: 90}))
	must(svc.OnCatalogItemChanged(ctx, meta("CatalogItemCreated"), usecases.CatalogItemChanged{CatalogItemID: "ci-1001"}, false))
	must(svc.OnListingCreated(ctx, meta("ListingCreated"), usecases.ListingCreated{ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3"}))
	must(svc.OnListingAvailabilityChanged(ctx, meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3", AvailableQty: 37, AsOf: at(22, 14, 0),
	}))
	must(svc.OnDepartureScheduled(ctx, meta("DepartureScheduled"), usecases.DepartureScheduled{
		DepartureID: "dep-AUS3-1800", LaneID: "lane-1", Kind: "PICKUP", OriginNodeID: "N-AUS3", DestinationNodeID: "N-SAU1",
		CutoffAt: at(22, 18, 0), DepartsAt: at(22, 18, 15), ArrivesAt: at(22, 19, 15), CapacityPackages: 40,
	}))
	must(svc.OnDepartureScheduled(ctx, meta("DepartureScheduled"), usecases.DepartureScheduled{
		DepartureID: "dep-SAU1-DAU1-2200", LaneID: "lane-2", Kind: "LINEHAUL", OriginNodeID: "N-SAU1", DestinationNodeID: "N-DAU1",
		CutoffAt: at(22, 22, 0), DepartsAt: at(22, 22, 15), ArrivesAt: at(22, 23, 30), CapacityPackages: 140,
	}))
	must(svc.OnWaveScheduled(ctx, meta("DeliveryWaveScheduled"), usecases.WaveScheduled{
		WaveID: "W-DAU1-1023-A", StationNodeID: "N-DAU1", InductCutoffAt: at(23, 6, 0), WindowStart: at(23, 9, 0), WindowEnd: at(23, 21, 0), StopCapacity: 320,
	}))

	quote, err := svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		DestinationPostalPrefix: "787", Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}}, Tiers: []shared.Tier{shared.TierNextDay},
	})
	must(err)
	if len(quote.Offers) != 1 {
		t.Fatalf("the seed quotes one NEXT_DAY offer, got %+v (unavailable %+v)", quote.Offers, quote.Unavailable)
	}
	made, err := svc.MakePromise(ctx, usecases.MakePromiseCommand{
		OfferID: quote.Offers[0].OfferID, Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
	})
	must(err)
	promiseID = made.Promise.PromiseID
	_, err = svc.ConfirmPromise(ctx, promiseID, "ord-1001", 1)
	must(err)
	_, err = svc.PublishPromisePolicy(ctx, policy.Input{
		OfferTTLMinutes: 5, HoldTTLMinutes: 20, PackagesPerOrder: 2, SharePercent: 50,
		Deadlines: []policy.Deadline{{Tier: shared.TierNextDay, RegionID: "R-SC", Minute: 23*60 + 59}},
	})
	must(err)
	return promiseID
}

// ask sends one skill request over the wire and returns the answer's data,
// failing the test on a transport error or a problem answer.
func ask(t *testing.T, client *a2aclient.Client, skill string, args map[string]any) map[string]any {
	t.Helper()
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewDataPart(map[string]any{"skill": skill, "arguments": args}))
	resp, err := client.SendMessage(context.Background(), &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		t.Fatalf("%s: %v", skill, err)
	}
	reply, ok := resp.(*a2a.Message)
	if !ok || len(reply.Parts) < 1 {
		t.Fatalf("%s: reply = %T", skill, resp)
	}
	data, ok := reply.Parts[0].Content.(a2a.Data)
	if !ok {
		t.Fatalf("%s: first part is %T", skill, reply.Parts[0].Content)
	}
	env, ok := data.Value.(map[string]any)
	if !ok || env["problem"] != nil {
		t.Fatalf("%s: envelope = %#v", skill, data.Value)
	}
	out, _ := env["data"].(map[string]any)
	return out
}

// assertSeededReads reads back, through both skills over the wire, what
// seedThroughWriteUseCases persisted.
func assertSeededReads(t *testing.T, client *a2aclient.Client, promiseID string) {
	t.Helper()
	assertPromiseReads(t, client, promiseID)
	assertBookRead(t, client)
}

func assertPromiseReads(t *testing.T, client *a2aclient.Client, promiseID string) {
	t.Helper()
	byOrder, _ := ask(t, client, "promise_status", map[string]any{"orderId": "ord-1001"})["promiseStatus"].(map[string]any)
	promises, _ := byOrder["promises"].([]any)
	if len(promises) != 1 {
		t.Fatalf("promise_status by order = %v", byOrder)
	}
	p, _ := promises[0].(map[string]any)
	witness, _ := p["witnessPath"].(map[string]any)
	if p["promiseId"] != promiseID || p["state"] != "CONFIRMED" || p["orderId"] != "ord-1001" || witness["nodeId"] != "N-AUS3" {
		t.Fatalf("promise = %v", p)
	}

	byID, _ := ask(t, client, "promise_status", map[string]any{"promiseId": promiseID})["promiseStatus"].(map[string]any)
	got, _ := byID["promises"].([]any)
	if len(got) != 1 {
		t.Fatalf("promise_status by id = %v", byID)
	}
	if first, _ := got[0].(map[string]any); first["promiseId"] != promiseID {
		t.Fatalf("promise_status by id = %v", byID)
	}
}

func assertBookRead(t *testing.T, client *a2aclient.Client) {
	t.Helper()
	books, _ := ask(t, client, "book_capacity", map[string]any{"nodeId": "N-AUS3", "date": "2026-10-22"})["bookCapacity"].(map[string]any)
	items, _ := books["books"].([]any)
	if len(items) != 1 {
		t.Fatalf("book_capacity = %v", books)
	}
	b, _ := items[0].(map[string]any)
	capacity, _ := b["capacity"].(map[string]any)
	if b["bookId"] != "N-AUS3:dep-AUS3-1800" || capacity["confirmed"] != 1.0 {
		t.Fatalf("book = %v: the confirmed hold must be on the book", b)
	}
}
