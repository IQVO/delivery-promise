package search

import (
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/calendar"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// The worked example of the promise model: Thursday 2026-10-22 14:05 in Austin
// (CDT, UTC-5), a shopper in postal prefix 787 asks for ci-1001 x2.
var cdt = time.FixedZone("CDT", -5*3600)

func at(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, cdt).UTC() }

func allDays(start, end int) map[time.Weekday]calendar.DayHours {
	m := map[time.Weekday]calendar.DayHours{}
	for d := time.Sunday; d <= time.Saturday; d++ {
		m[d] = calendar.DayHours{Start: start, End: end}
	}
	return m
}

func fcProfile(handling int, cutoffs ...calendar.Cutoff) *Profile {
	return &Profile{
		Schedule:        calendar.Schedule{Loc: cdt, Hours: allDays(6*60, 22*60)},
		HandlingMinutes: handling, Cutoffs: cutoffs, Cap: book.Throughput{Value: 400, PerHour: true},
	}
}

func fixture() Input {
	station := Node{ID: "N-DAU1", Type: "DELIVERY_STATION", RegionID: "R-SC", Active: true, Loc: cdt}
	sort := Node{ID: "N-SAU1", Type: "SORTATION_CENTER", RegionID: "R-SC", Active: true, Loc: cdt,
		Profile: &Profile{Schedule: calendar.Schedule{Loc: cdt, Hours: allDays(0, 24*60-1)}, SortDwellMinutes: 120}}
	aus3 := Node{ID: "N-AUS3", Type: TypeFulfillmentCenter, Subtype: "SORTABLE", RegionID: "R-SC", Active: true, Loc: cdt,
		Profile: fcProfile(95, calendar.Cutoff{Minute: 11 * 60}, calendar.Cutoff{Minute: 18 * 60, MaxOrders: 2500})}
	return Input{
		Now: at(22, 14, 5), Policy: policy.Default(), Station: &station, AreaRegionID: "R-SC",
		Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
		Nodes: map[string]Node{"N-DAU1": station, "N-SAU1": sort, "N-AUS3": aus3},
		Available: func(node, item string) int {
			if node == "N-AUS3" && item == "ci-1001" {
				return 37
			}
			return 0
		},
		Lanes: []Lane{
			{ID: "lane-1", Origin: "N-AUS3", Dest: "N-SAU1", NominalMinutes: 75, Open: true},
			{ID: "lane-2", Origin: "N-SAU1", Dest: "N-DAU1", NominalMinutes: 90, Open: true},
		},
		Departures: []Departure{
			{ID: "dep-AUS3-1800", LaneID: "lane-1", Kind: KindPickup, Origin: "N-AUS3", Dest: "N-SAU1", CutoffAt: at(22, 18, 0), ArrivesAt: at(22, 19, 15), Remaining: 40, Scheduled: true},
			{ID: "dep-SAU1-DAU1-2200", LaneID: "lane-2", Kind: KindLinehaul, Origin: "N-SAU1", Dest: "N-DAU1", CutoffAt: at(22, 22, 0), ArrivesAt: at(22, 23, 30), Remaining: 140, Scheduled: true},
		},
		Waves:  []Wave{{ID: "W-DAU1-1023-A", Station: "N-DAU1", InductCutoffAt: at(23, 6, 0), WindowStart: at(23, 9, 0), WindowEnd: at(23, 21, 0), Remaining: 320, Scheduled: true}},
		Grades: map[string]Grade{}, Books: map[string]BookView{},
	}
}

func (in Input) withNode(n Node) Input {
	nodes := map[string]Node{}
	for k, v := range in.Nodes {
		nodes[k] = v
	}
	nodes[n.ID] = n
	in.Nodes = nodes
	return in
}

func (in Input) withDeparture(d Departure) Input {
	in.Departures = append(append([]Departure{}, in.Departures...), d)
	return in
}

func (in Input) withWave(w Wave) Input {
	in.Waves = append(append([]Wave{}, in.Waves...), w)
	return in
}

func (in Input) stock(node, item string, qty int) Input {
	prev := in.Available
	in.Available = func(n, i string) int {
		if n == node && i == item {
			return qty
		}
		return prev(n, i)
	}
	return in
}
