package usecases_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/memory"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// The worked example of the promise model, Thursday 2026-10-22 14:05 in Austin
// (CDT, UTC-5): a shopper in postal prefix 787 asks for ci-1001 x2.
var (
	cdt = time.FixedZone("CDT", -5*3600)
	ctx = context.Background()
)

func at(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, cdt).UTC() }

// env is a Service on the in-memory doubles with the worked-example network
// projected through the real consumer use cases.
type env struct {
	t      *testing.T
	svc    *usecases.Service
	store  *memory.Store
	clock  *memory.FakeClock
	ids    *memory.SeqIDs
	events int
}

func newEnv(t *testing.T) *env {
	t.Helper()
	store := memory.NewStore()
	clock := memory.NewFakeClock(at(22, 14, 5))
	e := &env{t: t, store: store, clock: clock, ids: &memory.SeqIDs{}}
	e.svc = usecases.New(usecases.Deps{
		UoW: store.UnitOfWork(), Offers: store.Offers(), Promises: store.Promises(), Books: store.Books(), Policies: store.Policies(),
		Network: store.Network(), Shipments: store.Shipments(), Outbox: store.Outbox(), Inbox: store.Inbox(), Clock: clock, IDs: e.ids,
	})
	e.seedWorkedExample()
	return e
}

// meta mints a fresh CloudEvents envelope for a handler call.
func (e *env) meta(typ string) usecases.Meta {
	e.events++
	return usecases.Meta{ID: fmt.Sprintf("evt-%04d", e.events), Type: typ, Time: e.clock.Now()}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func hours(start, end string) usecases.HoursDTO { return usecases.HoursDTO{Start: start, End: end} }

func allWeek(start, end string) map[string]usecases.HoursDTO {
	m := map[string]usecases.HoursDTO{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
		m[d] = hours(start, end)
	}
	return m
}

func (e *env) node(id, typ, subtype, region string, profile *usecases.NodeProfileDeclared) {
	e.t.Helper()
	must(e.t, e.svc.OnNodeRegistered(ctx, e.meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: id, NodeType: typ, Subtype: subtype, Operator: "VENDOR", RegionID: region, Timezone: "America/Chicago",
	}))
	if profile != nil {
		profile.NodeID, profile.Timezone = id, "America/Chicago"
		must(e.t, e.svc.OnNodeProfileDeclared(ctx, e.meta("NodeOperationalProfileDeclared"), *profile))
	}
	must(e.t, e.svc.OnNodeStateChanged(ctx, e.meta("NodeActivated"), usecases.NodeStateChanged{NodeID: id}, "ACTIVE"))
}

func fcProfile(handling int, maxOrders int) *usecases.NodeProfileDeclared {
	return &usecases.NodeProfileDeclared{
		ProfileRevision: 1, OperatingHoursByDay: allWeek("06:00", "22:00"), HandlingTimeMinutes: handling,
		Cutoffs:       []usecases.CutoffDTO{{LocalTime: "11:00"}, {LocalTime: "18:00", MaxOrders: maxOrders}},
		ThroughputCap: usecases.ThroughputCapDTO{Value: 400, TimeUnit: "HOUR"},
	}
}

func (e *env) seedWorkedExample() {
	e.node("N-DAU1", "DELIVERY_STATION", "", "R-SC", nil)
	e.node("N-SAU1", "SORTATION_CENTER", "", "R-SC", &usecases.NodeProfileDeclared{
		ProfileRevision: 1, OperatingHoursByDay: allWeek("00:00", "23:59"), SortDwellMinutes: 120, ThroughputCap: usecases.ThroughputCapDTO{Value: 9999, TimeUnit: "HOUR"},
	})
	e.node("N-AUS3", "FULFILLMENT_CENTER", "SORTABLE", "R-SC", fcProfile(95, 2500))
	must(e.t, e.svc.OnRegionDefined(ctx, e.meta("RegionDefined"), usecases.RegionDefined{RegionID: "R-SC", Code: "SC", Name: "South Central"}))
	must(e.t, e.svc.OnServiceAreaAssigned(ctx, e.meta("ServiceAreaAssigned"), usecases.ServiceAreaAssigned{PostalPrefix: "787", DeliveryStationNodeID: "N-DAU1", RegionID: "R-SC", AssignmentVersion: 1}))
	must(e.t, e.svc.OnLaneOpened(ctx, e.meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-1", OriginNodeID: "N-AUS3", DestinationNodeID: "N-SAU1", NominalTransitMinutes: 75}))
	must(e.t, e.svc.OnLaneOpened(ctx, e.meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-2", OriginNodeID: "N-SAU1", DestinationNodeID: "N-DAU1", NominalTransitMinutes: 90}))
	e.stock("N-AUS3", "ci-1001", "lst-1", 37)
	e.departure("dep-AUS3-1800", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(22, 18, 0), at(22, 19, 15), 40)
	e.departure("dep-SAU1-DAU1-2200", "lane-2", "LINEHAUL", "N-SAU1", "N-DAU1", at(22, 22, 0), at(22, 23, 30), 140)
	e.wave("W-DAU1-1023-A", at(23, 6, 0), at(23, 9, 0), at(23, 21, 0), 320)
}

func (e *env) stock(node, item, listing string, qty int) {
	e.t.Helper()
	must(e.t, e.svc.OnCatalogItemChanged(ctx, e.meta("CatalogItemCreated"), usecases.CatalogItemChanged{CatalogItemID: item}, false))
	must(e.t, e.svc.OnListingCreated(ctx, e.meta("ListingCreated"), usecases.ListingCreated{ListingID: listing, CatalogItemID: item, NodeID: node}))
	must(e.t, e.svc.OnListingAvailabilityChanged(ctx, e.meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: listing, CatalogItemID: item, NodeID: node, AvailableQty: qty, AsOf: at(22, 14, 0),
	}))
}

func (e *env) departure(id, lane, kind, from, to string, cutoff, arrives time.Time, capacity int) {
	e.t.Helper()
	must(e.t, e.svc.OnDepartureScheduled(ctx, e.meta("DepartureScheduled"), usecases.DepartureScheduled{
		DepartureID: id, LaneID: lane, Kind: kind, OriginNodeID: from, DestinationNodeID: to, CutoffAt: cutoff, DepartsAt: cutoff.Add(15 * time.Minute),
		ArrivesAt: arrives, CapacityPackages: capacity,
	}))
}

func (e *env) wave(id string, induct, from, to time.Time, stops int) {
	e.t.Helper()
	must(e.t, e.svc.OnWaveScheduled(ctx, e.meta("DeliveryWaveScheduled"), usecases.WaveScheduled{
		WaveID: id, StationNodeID: "N-DAU1", InductCutoffAt: induct, WindowStart: from, WindowEnd: to, StopCapacity: stops,
	}))
}

// quoteLines quotes ci-1001 x2 for prefix 787.
func (e *env) quote(tiers ...shared.Tier) views.QuoteResult {
	e.t.Helper()
	res, err := e.svc.QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		DestinationPostalPrefix: "787", Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}}, Tiers: tiers,
	})
	must(e.t, err)
	return res
}

func (e *env) eventNames() []string {
	var out []string
	for _, ev := range e.store.Outbox().Events() {
		out = append(out, ev.EventName())
	}
	return out
}

func count(names []string, name string) int {
	n := 0
	for _, x := range names {
		if x == name {
			n++
		}
	}
	return n
}
