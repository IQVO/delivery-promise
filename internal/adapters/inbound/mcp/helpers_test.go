package mcp_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	inboundmcp "github.com/claudioed/delivery-promise/internal/adapters/inbound/mcp"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/memory"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// now is the fixture's fixed "now": Thursday 2026-10-22 14:05 Austin time,
// the worked example of the promise model.
var now = time.Date(2026, 10, 22, 14, 5, 0, 0, time.FixedZone("CDT", -5*3600)).UTC()

// harness is a connected in-memory MCP client over the real server wired to a
// real usecases.Service on the in-memory doubles, exactly as cmd/mcp wires
// them (minus Postgres). The write use cases (the consumer projections,
// quote, make, confirm, publish) exist ONLY here, to seed data the way
// cmd/api's consumers and checkout would: the MCP surface itself has none.
type harness struct {
	session *sdk.ClientSession
	svc     *usecases.Service
	store   *memory.Store
	clock   *memory.FakeClock
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	store := memory.NewStore()
	clock := memory.NewFakeClock(now)
	svc := usecases.New(usecases.Deps{
		UoW: store.UnitOfWork(), Offers: store.Offers(), Promises: store.Promises(), Books: store.Books(), Policies: store.Policies(),
		Network: store.Network(), Shipments: store.Shipments(), Outbox: store.Outbox(), Inbox: store.Inbox(),
		Clock: clock, IDs: &memory.SeqIDs{},
	})
	h := &harness{svc: svc, store: store, clock: clock}
	h.projectWorkedExample(t)
	h.session = connectSession(t, svc)
	return h
}

func connectSession(t *testing.T, reads inboundmcp.Reads) *sdk.ClientSession {
	t.Helper()
	ctx := context.Background()
	clientTransport, serverTransport := sdk.NewInMemoryTransports()
	serverSession, err := inboundmcp.NewServer(inboundmcp.Deps{Reads: reads}).Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })
	client := sdk.NewClient(&sdk.Implementation{Name: "test-client", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func at(day, h, m int) time.Time {
	return time.Date(2026, 10, day, h, m, 0, 0, time.FixedZone("CDT", -5*3600)).UTC()
}

func hours(start, end string) usecases.HoursDTO { return usecases.HoursDTO{Start: start, End: end} }

func allWeek(start, end string) map[string]usecases.HoursDTO {
	m := map[string]usecases.HoursDTO{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
		m[d] = hours(start, end)
	}
	return m
}

// projectWorkedExample feeds the local read models through the REAL consumer
// use cases (the way cmd/api's Kafka consumer does): the nodes, region,
// service area, lanes, stock, the pickup departure and the delivery wave of
// the worked example, then quotes and confirms one NEXT_DAY promise for
// order ord-1001 and publishes policy v1.
func (h *harness) projectWorkedExample(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	svc, n := h.svc, 0
	meta := func(typ string) usecases.Meta {
		n++
		return usecases.Meta{ID: fmt.Sprintf("evt-%04d", n), Type: typ, Time: h.clock.Now()}
	}

	must(t, svc.OnNodeRegistered(ctx, meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: "N-DAU1", NodeType: "DELIVERY_STATION", Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago",
	}))
	must(t, svc.OnNodeRegistered(ctx, meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: "N-SAU1", NodeType: "SORTATION_CENTER", Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago",
	}))
	must(t, svc.OnNodeRegistered(ctx, meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: "N-AUS3", NodeType: "FULFILLMENT_CENTER", Subtype: "SORTABLE", Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago",
	}))
	must(t, svc.OnNodeProfileDeclared(ctx, meta("NodeOperationalProfileDeclared"), usecases.NodeProfileDeclared{
		NodeID: "N-AUS3", ProfileRevision: 1, Timezone: "America/Chicago", OperatingHoursByDay: allWeek("06:00", "22:00"),
		HandlingTimeMinutes: 95, Cutoffs: []usecases.CutoffDTO{{LocalTime: "11:00"}, {LocalTime: "18:00", MaxOrders: 2500}},
		ThroughputCap: usecases.ThroughputCapDTO{Value: 400, TimeUnit: "HOUR"},
	}))
	must(t, svc.OnNodeStateChanged(ctx, meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-AUS3"}, "ACTIVE"))
	must(t, svc.OnNodeStateChanged(ctx, meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-SAU1"}, "ACTIVE"))
	must(t, svc.OnNodeStateChanged(ctx, meta("NodeActivated"), usecases.NodeStateChanged{NodeID: "N-DAU1"}, "ACTIVE"))
	must(t, svc.OnRegionDefined(ctx, meta("RegionDefined"), usecases.RegionDefined{RegionID: "R-SC", Code: "SC", Name: "South Central"}))
	must(t, svc.OnServiceAreaAssigned(ctx, meta("ServiceAreaAssigned"), usecases.ServiceAreaAssigned{
		PostalPrefix: "787", DeliveryStationNodeID: "N-DAU1", RegionID: "R-SC", AssignmentVersion: 1,
	}))
	must(t, svc.OnLaneOpened(ctx, meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-1", OriginNodeID: "N-AUS3", DestinationNodeID: "N-SAU1", NominalTransitMinutes: 75}))
	must(t, svc.OnLaneOpened(ctx, meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-2", OriginNodeID: "N-SAU1", DestinationNodeID: "N-DAU1", NominalTransitMinutes: 90}))
	must(t, svc.OnCatalogItemChanged(ctx, meta("CatalogItemCreated"), usecases.CatalogItemChanged{CatalogItemID: "ci-1001"}, false))
	must(t, svc.OnListingCreated(ctx, meta("ListingCreated"), usecases.ListingCreated{ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3"}))
	must(t, svc.OnListingAvailabilityChanged(ctx, meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: "lst-1", CatalogItemID: "ci-1001", NodeID: "N-AUS3", AvailableQty: 37, AsOf: at(22, 14, 0),
	}))
	must(t, svc.OnDepartureScheduled(ctx, meta("DepartureScheduled"), usecases.DepartureScheduled{
		DepartureID: "dep-AUS3-1800", LaneID: "lane-1", Kind: "PICKUP", OriginNodeID: "N-AUS3", DestinationNodeID: "N-SAU1",
		CutoffAt: at(22, 18, 0), DepartsAt: at(22, 18, 15), ArrivesAt: at(22, 19, 15), CapacityPackages: 40,
	}))
	must(t, svc.OnDepartureScheduled(ctx, meta("DepartureScheduled"), usecases.DepartureScheduled{
		DepartureID: "dep-SAU1-DAU1-2200", LaneID: "lane-2", Kind: "LINEHAUL", OriginNodeID: "N-SAU1", DestinationNodeID: "N-DAU1",
		CutoffAt: at(22, 22, 0), DepartsAt: at(22, 22, 15), ArrivesAt: at(22, 23, 30), CapacityPackages: 140,
	}))
	must(t, svc.OnWaveScheduled(ctx, meta("DeliveryWaveScheduled"), usecases.WaveScheduled{
		WaveID: "W-DAU1-1023-A", StationNodeID: "N-DAU1", InductCutoffAt: at(23, 6, 0), WindowStart: at(23, 9, 0), WindowEnd: at(23, 21, 0), StopCapacity: 320,
	}))

	// checkout: quote, make and confirm the worked-example NEXT_DAY promise.
	quote, err := svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		DestinationPostalPrefix: "787",
		Lines:                   []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
		Tiers:                   []shared.Tier{shared.TierNextDay},
	})
	must(t, err)
	if len(quote.Offers) != 1 {
		t.Fatalf("worked example quotes one NEXT_DAY offer, got %+v (unavailable %+v)", quote.Offers, quote.Unavailable)
	}
	made, err := svc.MakePromise(ctx, usecases.MakePromiseCommand{
		OfferID: quote.Offers[0].OfferID, Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
	})
	must(t, err)
	_, err = svc.ConfirmPromise(ctx, made.Promise.PromiseID, "ord-1001", 1)
	must(t, err)

	// the operator publishes policy v1.
	_, err = svc.PublishPromisePolicy(ctx, policy.Input{
		OfferTTLMinutes: 5, HoldTTLMinutes: 20, PackagesPerOrder: 2, SharePercent: 50,
		Deadlines: []policy.Deadline{{Tier: shared.TierNextDay, RegionID: "R-SC", Minute: 23*60 + 59}},
	})
	must(t, err)
}

// call invokes a tool and fails the test on a TRANSPORT/protocol error;
// tool-level failures come back as res.IsError.
func call(t *testing.T, session *sdk.ClientSession, name string, args map[string]any) *sdk.CallToolResult {
	t.Helper()
	res, err := session.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: transport/protocol error (want a tool result): %v", name, err)
	}
	return res
}

// ok calls a tool, requires success, and returns its structured content.
func (h *harness) ok(t *testing.T, name string, args map[string]any) map[string]any {
	t.Helper()
	res := call(t, h.session, name, args)
	if res.IsError {
		t.Fatalf("%s returned a tool error: %s", name, text(res))
	}
	sc, isMap := res.StructuredContent.(map[string]any)
	if !isMap {
		t.Fatalf("%s: structured content = %#v, want an object", name, res.StructuredContent)
	}
	return sc
}

// failWith calls a tool and requires an isError result whose text starts
// with the slug want.
func failWith(t *testing.T, session *sdk.ClientSession, name string, args map[string]any, want string) {
	t.Helper()
	res := call(t, session, name, args)
	if !res.IsError {
		t.Fatalf("%s: expected an isError tool result, got %#v", name, res.StructuredContent)
	}
	if got := text(res); !strings.HasPrefix(got, want+": ") {
		t.Fatalf("%s: error text %q does not start with %q", name, got, want+": ")
	}
}

func text(res *sdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if tc, isText := c.(*sdk.TextContent); isText {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}
