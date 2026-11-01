package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/search"
)

var networkCases = []named{
	{"every view row round-trips, a miss is ErrNotFound and a Put replaces", networkRoundTrips},
	{"a node profile round-trips", networkProfile},
	{"ListingsForNode returns the node's listings of the items", networkListings},
	{"Load returns the slice of the network a search needs", networkLoad},
}

func networkRoundTrips(t *testing.T, f Fixture) {
	n := f.Network
	_, err := n.GetNode(ctx, "nope")
	wantErr(t, err, ports.ErrNotFound)
	_, err = n.GetRegion(ctx, "nope")
	wantErr(t, err, ports.ErrNotFound)
	_, err = n.GetArea(ctx, "000")
	wantErr(t, err, ports.ErrNotFound)
	_, err = n.GetLane(ctx, "nope")
	wantErr(t, err, ports.ErrNotFound)
	_, err = n.GetItem(ctx, "nope")
	wantErr(t, err, ports.ErrNotFound)
	_, err = n.GetListing(ctx, "nope")
	wantErr(t, err, ports.ErrNotFound)
	_, err = n.GetDeparture(ctx, "nope")
	wantErr(t, err, ports.ErrNotFound)
	_, err = n.GetWave(ctx, "nope")
	wantErr(t, err, ports.ErrNotFound)
	_, err = n.GetScorecard(ctx, "nope")
	wantErr(t, err, ports.ErrNotFound)

	noErr(t, n.PutRegion(ctx, readmodel.Region{ID: "R-SC", Code: "SC", Name: "South Central"}))
	r, err := n.GetRegion(ctx, "R-SC")
	noErr(t, err)
	eq(t, "region name", r.Name, "South Central")
	noErr(t, n.PutArea(ctx, readmodel.Area{PostalPrefix: "787", StationID: "N-DAU1", RegionID: "R-SC", Version: 1}))
	noErr(t, n.PutArea(ctx, readmodel.Area{PostalPrefix: "787", StationID: "N-DAU2", RegionID: "R-SC", Version: 2}))
	a, err := n.GetArea(ctx, "787")
	noErr(t, err)
	eq(t, "area replaced", a.StationID, "N-DAU2")
	eq(t, "area version", a.Version, 2)
	networkRoundTripsMore(t, f)
}

func networkRoundTripsMore(t *testing.T, f Fixture) {
	n := f.Network
	noErr(t, n.PutLane(ctx, search.Lane{ID: "lane-1", Origin: "a", Dest: "b", NominalMinutes: 75, Open: true}))
	l, err := n.GetLane(ctx, "lane-1")
	noErr(t, err)
	eq(t, "lane", l, search.Lane{ID: "lane-1", Origin: "a", Dest: "b", NominalMinutes: 75, Open: true})
	noErr(t, n.PutItem(ctx, readmodel.Item{ID: "ci-1", Discontinued: true}))
	it, err := n.GetItem(ctx, "ci-1")
	noErr(t, err)
	eq(t, "discontinued", it.Discontinued, true)
	noErr(t, n.PutListing(ctx, readmodel.Listing{ID: "lst-1", ItemID: "ci-1", NodeID: "n1", AvailableQty: 7, AsOf: T0, Suppressed: true}))
	ls, err := n.GetListing(ctx, "lst-1")
	noErr(t, err)
	eq(t, "qty", ls.AvailableQty, 7)
	eq(t, "suppressed", ls.Suppressed, true)
	eqTime(t, "asOf", ls.AsOf, T0)
	noErr(t, n.PutScorecard(ctx, readmodel.Scorecard{NodeID: "n1", Grade: "B", Observations: 60, ComputedAt: T0}))
	sc, err := n.GetScorecard(ctx, "n1")
	noErr(t, err)
	eq(t, "grade", sc.Grade, "B")
	eq(t, "observations", sc.Observations, 60)
	dep := search.Departure{
		ID: "dep-1", LaneID: "lane-1", Kind: search.KindPickup, Origin: "a", Dest: "b", CutoffAt: T0, DepartsAt: T0.Add(time.Minute),
		ArrivesAt: T0.Add(time.Hour), Remaining: 9, Cancelled: true, Scheduled: true,
	}
	noErr(t, n.PutDeparture(ctx, dep))
	gd, err := n.GetDeparture(ctx, "dep-1")
	noErr(t, err)
	eq(t, "departure", gd, dep)
	wave := search.Wave{ID: "w-1", Station: "st", InductCutoffAt: T0, WindowStart: T0.Add(time.Hour), WindowEnd: T0.Add(2 * time.Hour), Remaining: 3, Scheduled: true}
	noErr(t, n.PutWave(ctx, wave))
	gw, err := n.GetWave(ctx, "w-1")
	noErr(t, err)
	eq(t, "wave", gw, wave)
}

func profile() *readmodel.Profile {
	return &readmodel.Profile{
		Revision: 3, Timezone: "America/Chicago", OperatingHours: map[string]readmodel.Hours{"monday": {Start: "06:00", End: "22:00"}, "tuesday": {Start: "06:00", End: "22:00"}},
		Holidays: []string{"2026-12-25"}, HandlingMinutes: 95, Cutoffs: []readmodel.Cutoff{{LocalTime: "18:00", Days: []string{"monday"}, MaxOrders: 2500}, {LocalTime: "11:00"}},
		CapValue: 400, CapPerHour: true, SortDwellMinutes: 0,
	}
}

func networkProfile(t *testing.T, f Fixture) {
	n := readmodel.Node{ID: "N-AUS3", Type: "FULFILLMENT_CENTER", Subtype: "SORTABLE", Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago", State: readmodel.NodeActive, Profile: profile()}
	noErr(t, f.Network.PutNode(ctx, n))
	got, err := f.Network.GetNode(ctx, "N-AUS3")
	noErr(t, err)
	eq(t, "type", got.Type, "FULFILLMENT_CENTER")
	eq(t, "state", got.State, readmodel.NodeActive)
	eq(t, "tz", got.Timezone, "America/Chicago")
	p := got.Profile
	if p == nil {
		t.Fatal("the profile was lost")
	}
	eq(t, "revision", p.Revision, 3)
	eq(t, "handling", p.HandlingMinutes, 95)
	eq(t, "hours", p.OperatingHours["tuesday"], readmodel.Hours{Start: "06:00", End: "22:00"})
	eq(t, "holidays", len(p.Holidays), 1)
	eq(t, "cutoffs", len(p.Cutoffs), 2)
	eq(t, "cutoff 0 days", len(p.Cutoffs[0].Days), 1)
	eq(t, "cutoff 0 max", p.Cutoffs[0].MaxOrders, 2500)
	eq(t, "cap", p.CapValue, 400)
	eq(t, "cap per hour", p.CapPerHour, true)
	stub := readmodel.Node{ID: "N-STUB", State: readmodel.NodeProposed}
	noErr(t, f.Network.PutNode(ctx, stub))
	gs, _ := f.Network.GetNode(ctx, "N-STUB")
	if gs.Profile != nil {
		t.Fatal("a node without a profile has none")
	}
}

func networkListings(t *testing.T, f Fixture) {
	put := func(id, item, node string, qty int) {
		noErr(t, f.Network.PutListing(ctx, readmodel.Listing{ID: id, ItemID: item, NodeID: node, AvailableQty: qty, AsOf: T0}))
	}
	put("lst-1", "ci-1", "n1", 5)
	put("lst-2", "ci-2", "n1", 6)
	put("lst-3", "ci-1", "n2", 7)
	put("lst-4", "ci-3", "n1", 8)
	got, err := f.Network.ListingsForNode(ctx, "n1", []string{"ci-1", "ci-2"})
	noErr(t, err)
	eq(t, "listings", len(got), 2)
	eq(t, "ordered by id", got[0].ID, "lst-1")
	got, _ = f.Network.ListingsForNode(ctx, "n9", []string{"ci-1"})
	eq(t, "none", len(got), 0)
	got, _ = f.Network.ListingsForNode(ctx, "n1", nil)
	eq(t, "no items", len(got), 0)
}

func networkLoad(t *testing.T, f Fixture) {
	seed := func(err error) { noErr(t, err) }
	for _, id := range []string{"st", "n1", "n2", "hub", "far", "idle"} {
		seed(f.Network.PutNode(ctx, readmodel.Node{ID: id, State: readmodel.NodeActive}))
	}
	seed(f.Network.PutListing(ctx, readmodel.Listing{ID: "lst-1", ItemID: "ci-1", NodeID: "n1", AvailableQty: 5}))
	seed(f.Network.PutListing(ctx, readmodel.Listing{ID: "lst-2", ItemID: "ci-1", NodeID: "n2", AvailableQty: 5}))
	seed(f.Network.PutListing(ctx, readmodel.Listing{ID: "lst-3", ItemID: "ci-9", NodeID: "idle", AvailableQty: 5}))
	seed(f.Network.PutItem(ctx, readmodel.Item{ID: "ci-1"}))
	seed(f.Network.PutItem(ctx, readmodel.Item{ID: "ci-9"}))
	seed(f.Network.PutScorecard(ctx, readmodel.Scorecard{NodeID: "n1", Grade: "C"}))
	seed(f.Network.PutScorecard(ctx, readmodel.Scorecard{NodeID: "idle", Grade: "A"}))
	seed(f.Network.PutLane(ctx, search.Lane{ID: "lane-1", Origin: "n1", Dest: "hub", NominalMinutes: 60, Open: true}))
	dep := func(id, o, d string, cutoff time.Time, scheduled bool) search.Departure {
		return search.Departure{ID: id, Kind: search.KindLinehaul, Origin: o, Dest: d, CutoffAt: cutoff, ArrivesAt: cutoff.Add(time.Hour), Remaining: 4, Scheduled: scheduled}
	}
	seed(f.Network.PutDeparture(ctx, dep("dep-in", "n1", "hub", T0.Add(time.Hour), true)))
	seed(f.Network.PutDeparture(ctx, dep("dep-hub", "hub", "st", T0.Add(2*time.Hour), true)))
	seed(f.Network.PutDeparture(ctx, dep("dep-past", "far", "st", T0.Add(-time.Hour), true)))
	seed(f.Network.PutDeparture(ctx, dep("dep-late", "far", "st", T0.Add(48*time.Hour), true)))
	seed(f.Network.PutDeparture(ctx, dep("dep-stub", "far", "st", T0.Add(time.Hour), false)))
	seed(f.Network.PutWave(ctx, search.Wave{ID: "w-ok", Station: "st", InductCutoffAt: T0.Add(3 * time.Hour), WindowStart: T0.Add(4 * time.Hour), WindowEnd: T0.Add(5 * time.Hour), Remaining: 2, Scheduled: true}))
	seed(f.Network.PutWave(ctx, search.Wave{ID: "w-old", Station: "st", WindowEnd: T0.Add(-time.Hour), Scheduled: true}))
	seed(f.Network.PutWave(ctx, search.Wave{ID: "w-else", Station: "other", WindowEnd: T0.Add(5 * time.Hour), Scheduled: true}))
	seed(f.Network.PutWave(ctx, search.Wave{ID: "w-stub", Station: "st", WindowEnd: T0.Add(5 * time.Hour)}))

	net, err := f.Network.Load(ctx, readmodel.Query{StationID: "st", ItemIDs: []string{"ci-1"}, From: T0, Until: T0.Add(24 * time.Hour)})
	noErr(t, err)
	eq(t, "nodes: station, n1, n2 and the hub of the departures", ids(net.Nodes, func(n readmodel.Node) string { return n.ID }), "hub,n1,n2,st")
	eq(t, "listings", ids(net.Listings, func(l readmodel.Listing) string { return l.ID }), "lst-1,lst-2")
	eq(t, "items", ids(net.Items, func(i readmodel.Item) string { return i.ID }), "ci-1")
	eq(t, "scorecards of the loaded nodes", ids(net.Scorecards, func(s readmodel.Scorecard) string { return s.NodeID }), "n1")
	eq(t, "lanes", ids(net.Lanes, func(l search.Lane) string { return l.ID }), "lane-1")
	eq(t, "departures in the window", ids(net.Departures, func(d search.Departure) string { return d.ID }), "dep-hub,dep-in")
	eq(t, "waves at the station", ids(net.Waves, func(w search.Wave) string { return w.ID }), "w-ok")
}

func ids[T any](rows []T, id func(T) string) string {
	out := ""
	for i, r := range rows {
		if i > 0 {
			out += ","
		}
		out += id(r)
	}
	return out
}

var shipmentCases = []named{
	{"an assignment round-trips with its exclusions and is found by promise", shipmentAssignment},
	{"shipments map tracking numbers to assignments", shipmentTrackings},
	{"the first outcome of a package wins", shipmentOutcomes},
}

func shipmentAssignment(t *testing.T, f Fixture) {
	_, err := f.Shipments.GetAssignment(ctx, "asg-1")
	wantErr(t, err, ports.ErrNotFound)
	_, err = f.Shipments.FindAssignmentByPromise(ctx, "prm-1")
	wantErr(t, err, ports.ErrNotFound)
	a := readmodel.Assignment{
		ID: "asg-1", PromiseID: "prm-1", OrderID: "ord-1", GroupNo: 2, NodeID: "N-AUS3", Locked: true, LockedAt: T0,
		ExcludedNodes: []string{"N-X"}, ExcludedDepartures: []string{"dep-1", "dep-2"},
	}
	noErr(t, f.Shipments.PutAssignment(ctx, a))
	got, err := f.Shipments.GetAssignment(ctx, "asg-1")
	noErr(t, err)
	eq(t, "promise", got.PromiseID, "prm-1")
	eq(t, "group", got.GroupNo, 2)
	eq(t, "locked", got.Locked, true)
	eq(t, "node", got.NodeID, "N-AUS3")
	eqTime(t, "lockedAt", got.LockedAt, T0)
	eq(t, "excluded nodes", len(got.ExcludedNodes), 1)
	eq(t, "excluded departures", len(got.ExcludedDepartures), 2)
	by, err := f.Shipments.FindAssignmentByPromise(ctx, "prm-1")
	noErr(t, err)
	eq(t, "by promise", by.ID, "asg-1")
	a.Locked, a.ExcludedNodes = false, nil
	noErr(t, f.Shipments.PutAssignment(ctx, a))
	got, _ = f.Shipments.GetAssignment(ctx, "asg-1")
	eq(t, "replaced", got.Locked, false)
	eq(t, "replaced exclusions", len(got.ExcludedNodes), 0)
}

func shipmentTrackings(t *testing.T, f Fixture) {
	got, err := f.Shipments.Shipment(ctx, "asg-1")
	noErr(t, err)
	eq(t, "no shipment yet", len(got), 0)
	_, err = f.Shipments.AssignmentOf(ctx, "TRK-1")
	wantErr(t, err, ports.ErrNotFound)
	noErr(t, f.Shipments.PutShipment(ctx, "asg-1", []string{"TRK-2", "TRK-1"}))
	got, _ = f.Shipments.Shipment(ctx, "asg-1")
	eq(t, "tracking numbers", ids(got, func(s string) string { return s }), "TRK-1,TRK-2")
	owner, err := f.Shipments.AssignmentOf(ctx, "TRK-2")
	noErr(t, err)
	eq(t, "owner", owner, "asg-1")
	noErr(t, f.Shipments.PutShipment(ctx, "asg-1", []string{"TRK-1"}))
	got, _ = f.Shipments.Shipment(ctx, "asg-1")
	eq(t, "a repeated shipment is replaced", len(got), 1)
}

func shipmentOutcomes(t *testing.T, f Fixture) {
	noErr(t, f.Shipments.PutOutcome(ctx, readmodel.PackageOutcome{TrackingNumber: "TRK-1", AssignmentID: "asg-1", Kind: readmodel.OutcomeDelivered, At: T0}))
	noErr(t, f.Shipments.PutOutcome(ctx, readmodel.PackageOutcome{TrackingNumber: "TRK-1", Kind: readmodel.OutcomeLost, At: T0.Add(time.Hour)}))
	noErr(t, f.Shipments.PutOutcome(ctx, readmodel.PackageOutcome{TrackingNumber: "TRK-2", Kind: readmodel.OutcomeReturned, At: T0.Add(time.Hour)}))
	got, err := f.Shipments.Outcomes(ctx, []string{"TRK-1", "TRK-2", "TRK-3"})
	noErr(t, err)
	eq(t, "outcomes", len(got), 2)
	eq(t, "first wins", got[0].Kind, readmodel.OutcomeDelivered)
	eq(t, "assignment kept", got[0].AssignmentID, "asg-1")
	eq(t, "second package", got[1].Kind, readmodel.OutcomeReturned)
	eqTime(t, "at", got[1].At, T0.Add(time.Hour))
	none, _ := f.Shipments.Outcomes(ctx, nil)
	eq(t, "no trackings", len(none), 0)
}
