package features_test

import (
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

func registerNetworkSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the worked-example network is projected$`, w.seedWorkedExample)
	sc.Step(`^the clock advances (\d+) minutes$`, w.advance)
	sc.Step(`^the node "([^"]*)" declares a cutoff slot of (\d+) orders?$`, w.declareSlot)
	sc.Step(`^the departure "([^"]*)" is cancelled$`, w.cancelDeparture)
	sc.Step(`^an earlier pickup "([^"]*)" is scheduled$`, w.earlierPickup)
	sc.Step(`^the node "([^"]*)" is (suspended|activated)$`, w.nodeState)
	sc.Step(`^the network changes: (.+)$`, w.changeNetwork)
}

func allWeek(start, end string) map[string]usecases.HoursDTO {
	m := map[string]usecases.HoursDTO{}
	for _, d := range []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"} {
		m[d] = usecases.HoursDTO{Start: start, End: end}
	}
	return m
}

func fcProfile(revision, handling, maxOrders int) usecases.NodeProfileDeclared {
	return usecases.NodeProfileDeclared{
		NodeID: "N-AUS3", Timezone: "America/Chicago", ProfileRevision: revision,
		OperatingHoursByDay: allWeek("06:00", "22:00"), HandlingTimeMinutes: handling,
		Cutoffs:       []usecases.CutoffDTO{{LocalTime: "11:00"}, {LocalTime: "18:00", MaxOrders: maxOrders}},
		ThroughputCap: usecases.ThroughputCapDTO{Value: 400, TimeUnit: "HOUR"},
	}
}

func (w *world) node(id, typ, subtype string, profile *usecases.NodeProfileDeclared) error {
	ctx := w.ctx()
	if err := w.svc.OnNodeRegistered(ctx, w.meta("NodeRegistered"), usecases.NodeRegistered{
		NodeID: id, NodeType: typ, Subtype: subtype, Operator: "VENDOR", RegionID: "R-SC", Timezone: "America/Chicago",
	}); err != nil {
		return err
	}
	if profile != nil {
		profile.NodeID, profile.Timezone = id, "America/Chicago"
		if err := w.svc.OnNodeProfileDeclared(ctx, w.meta("NodeOperationalProfileDeclared"), *profile); err != nil {
			return err
		}
	}
	return w.svc.OnNodeStateChanged(ctx, w.meta("NodeActivated"), usecases.NodeStateChanged{NodeID: id}, readmodel.NodeActive)
}

// seedWorkedExample projects the model's worked example through the real
// consumer use cases: three nodes, two lanes, one pickup and one linehaul
// departure, one delivery wave and 37 units of ci-1001 at the fulfilment node.
func (w *world) seedWorkedExample() error {
	sort := usecases.NodeProfileDeclared{
		ProfileRevision: 1, OperatingHoursByDay: allWeek("00:00", "23:59"), SortDwellMinutes: 120,
		ThroughputCap: usecases.ThroughputCapDTO{Value: 9999, TimeUnit: "HOUR"},
	}
	fc := fcProfile(1, 95, 2500)
	steps := []func() error{
		func() error { return w.node("N-DAU1", "DELIVERY_STATION", "", nil) },
		func() error { return w.node("N-SAU1", "SORTATION_CENTER", "", &sort) },
		func() error { return w.node("N-AUS3", "FULFILLMENT_CENTER", "SORTABLE", &fc) },
		w.projectGeography,
		w.projectSchedules,
		func() error { return w.stock("N-AUS3", "ci-1001", "lst-1", 37) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("seeding the worked example: %w", err)
		}
	}
	return nil
}

func (w *world) projectGeography() error {
	ctx := w.ctx()
	if err := w.svc.OnRegionDefined(ctx, w.meta("RegionDefined"), usecases.RegionDefined{RegionID: "R-SC", Code: "SC", Name: "South Central"}); err != nil {
		return err
	}
	if err := w.svc.OnServiceAreaAssigned(ctx, w.meta("ServiceAreaAssigned"), usecases.ServiceAreaAssigned{
		PostalPrefix: "787", DeliveryStationNodeID: "N-DAU1", RegionID: "R-SC", AssignmentVersion: 1,
	}); err != nil {
		return err
	}
	if err := w.svc.OnLaneOpened(ctx, w.meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-1", OriginNodeID: "N-AUS3", DestinationNodeID: "N-SAU1", NominalTransitMinutes: 75}); err != nil {
		return err
	}
	return w.svc.OnLaneOpened(ctx, w.meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-2", OriginNodeID: "N-SAU1", DestinationNodeID: "N-DAU1", NominalTransitMinutes: 90})
}

func (w *world) projectSchedules() error {
	if err := w.departure("dep-AUS3-1800", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(22, 18, 0), at(22, 19, 15), 40); err != nil {
		return err
	}
	if err := w.departure("dep-SAU1-DAU1-2200", "lane-2", "LINEHAUL", "N-SAU1", "N-DAU1", at(22, 22, 0), at(22, 23, 30), 140); err != nil {
		return err
	}
	return w.svc.OnWaveScheduled(w.ctx(), w.meta("DeliveryWaveScheduled"), usecases.WaveScheduled{
		WaveID: "W-DAU1-1023-A", StationNodeID: "N-DAU1", InductCutoffAt: at(23, 6, 0), WindowStart: at(23, 9, 0), WindowEnd: at(23, 21, 0), StopCapacity: 320,
	})
}

func (w *world) stock(node, item, listing string, qty int) error {
	ctx := w.ctx()
	if err := w.svc.OnCatalogItemChanged(ctx, w.meta("CatalogItemCreated"), usecases.CatalogItemChanged{CatalogItemID: item}, false); err != nil {
		return err
	}
	if err := w.svc.OnListingCreated(ctx, w.meta("ListingCreated"), usecases.ListingCreated{ListingID: listing, CatalogItemID: item, NodeID: node}); err != nil {
		return err
	}
	return w.svc.OnListingAvailabilityChanged(ctx, w.meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: listing, CatalogItemID: item, NodeID: node, AvailableQty: qty, AsOf: at(22, 14, 0),
	})
}

func (w *world) departure(id, lane, kind, from, to string, cutoff, arrives time.Time, capacity int) error {
	return w.svc.OnDepartureScheduled(w.ctx(), w.meta("DepartureScheduled"), usecases.DepartureScheduled{
		DepartureID: id, LaneID: lane, Kind: kind, OriginNodeID: from, DestinationNodeID: to, CutoffAt: cutoff,
		DepartsAt: cutoff.Add(15 * time.Minute), ArrivesAt: arrives, CapacityPackages: capacity,
	})
}

func (w *world) advance(minutes int) error {
	w.clock.Advance(time.Duration(minutes) * time.Minute)
	return nil
}

func (w *world) declareSlot(node string, maxOrders int) error {
	p := fcProfile(2, 95, maxOrders)
	p.NodeID = node
	return w.svc.OnNodeProfileDeclared(w.ctx(), w.meta("NodeOperationalProfileDeclared"), p)
}

// cancelDeparture is the trigger of the re-promising scenarios, so it counts as
// the "last command" whose published events the Then steps inspect.
func (w *world) cancelDeparture(id string) error {
	w.run(func() error {
		return w.svc.OnDepartureCancelled(w.ctx(), w.meta("DepartureCancelled"), usecases.DepartureCancelled{DepartureID: id})
	})
	return w.lastErr
}

func (w *world) earlierPickup(id string) error {
	return w.departure(id, "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(22, 17, 30), at(22, 18, 45), 40)
}

func (w *world) nodeState(node, change string) error {
	state := readmodel.NodeSuspended
	if change == "activated" {
		state = readmodel.NodeActive
	}
	return w.svc.OnNodeStateChanged(w.ctx(), w.meta("Node"+change), usecases.NodeStateChanged{NodeID: node}, state)
}

// changeNetwork applies one named change of a producer's fact, so one outline
// can prove that every kind of change retires the witness path.
func (w *world) changeNetwork(change string) error {
	ctx := w.ctx()
	switch change {
	case "the linehaul departure is cancelled":
		return w.cancelDeparture("dep-SAU1-DAU1-2200")
	case "the pickup departure is full":
		return w.svc.OnDepartureCapacityChanged(ctx, w.meta("DepartureCapacityChanged"), usecases.DepartureCapacityChanged{DepartureID: "dep-AUS3-1800", RemainingPackages: 0})
	case "the delivery wave is full":
		return w.svc.OnWaveCapacityChanged(ctx, w.meta("DeliveryWaveCapacityChanged"), usecases.WaveCapacityChanged{WaveID: "W-DAU1-1023-A", RemainingStops: 0})
	case "the delivery wave is cancelled":
		return w.svc.OnWaveCancelled(ctx, w.meta("DeliveryWaveCancelled"), usecases.WaveCancelled{WaveID: "W-DAU1-1023-A"})
	case "the fulfilment node is suspended":
		return w.nodeState("N-AUS3", "suspended")
	}
	return fmt.Errorf("unknown network change %q", change)
}
