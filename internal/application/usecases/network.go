package usecases

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/calendar"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// SearchHorizon is how far ahead of now departures are loaded for a search.
const SearchHorizon = 8 * 24 * time.Hour

type searchOptions struct {
	singleGroup       bool
	onlyNode          string
	excludeNodes      map[string]bool
	excludeDepartures map[string]bool
	excludeWaves      map[string]bool
}

// ToSearchNode converts a NodeProfileView row to the search's node. A row whose
// zone or profile cannot be read has no Loc or Profile and is never a candidate.
func ToSearchNode(n readmodel.Node) search.Node {
	out := search.Node{ID: n.ID, Type: n.Type, Subtype: n.Subtype, RegionID: n.RegionID, Active: n.State == readmodel.NodeActive}
	loc, err := time.LoadLocation(n.Timezone)
	if err != nil {
		return out
	}
	out.Loc = loc
	if n.Profile != nil {
		if p, ok := toProfile(*n.Profile, loc); ok {
			out.Profile = p
		}
	}
	return out
}

func toProfile(p readmodel.Profile, loc *time.Location) (*search.Profile, bool) {
	hours := map[time.Weekday]calendar.DayHours{}
	for name, h := range p.OperatingHours {
		day, ok := calendar.ParseWeekday(name)
		start, e1 := calendar.ParseClock(h.Start)
		end, e2 := calendar.ParseClock(h.End)
		if !ok || e1 != nil || e2 != nil {
			return nil, false
		}
		hours[day] = calendar.DayHours{Start: start, End: end}
	}
	cutoffs, ok := toCutoffs(p.Cutoffs)
	if !ok {
		return nil, false
	}
	holidays := make(map[string]bool, len(p.Holidays))
	for _, h := range p.Holidays {
		holidays[h] = true
	}
	return &search.Profile{
		Schedule: calendar.Schedule{Loc: loc, Hours: hours, Holidays: holidays}, HandlingMinutes: p.HandlingMinutes, Cutoffs: cutoffs,
		Cap: book.Throughput{Value: p.CapValue, PerHour: p.CapPerHour}, SortDwellMinutes: p.SortDwellMinutes,
	}, true
}

func toCutoffs(in []readmodel.Cutoff) ([]calendar.Cutoff, bool) {
	out := make([]calendar.Cutoff, 0, len(in))
	for _, c := range in {
		minute, err := calendar.ParseClock(c.LocalTime)
		if err != nil {
			return nil, false
		}
		var days []time.Weekday
		for _, d := range c.Days {
			day, ok := calendar.ParseWeekday(d)
			if !ok {
				return nil, false
			}
			days = append(days, day)
		}
		out = append(out, calendar.Cutoff{Minute: minute, Days: days, MaxOrders: c.MaxOrders})
	}
	return out, true
}

// searchInput loads the read models, the books and the provisional holds a
// search needs and assembles its input.
func (s *Service) searchInput(ctx context.Context, now time.Time, pol *policy.Policy, prefix string, lines []shared.Line, tiers []shared.Tier, opt searchOptions) (search.Input, error) {
	in := search.Input{
		Now: now, Policy: pol, Lines: lines, Tiers: tiers, SingleGroup: opt.singleGroup, OnlyNode: opt.onlyNode,
		ExcludeNodes: opt.excludeNodes, ExcludeDepartures: opt.excludeDepartures, ExcludeWaves: opt.excludeWaves,
		Nodes: map[string]search.Node{}, Grades: map[string]search.Grade{}, Books: map[string]search.BookView{},
		Available: func(string, string) int { return 0 },
	}
	area, err := s.Network.GetArea(ctx, prefix)
	if errors.Is(err, ports.ErrNotFound) {
		return in, nil
	}
	if err != nil {
		return in, err
	}
	stationRow, err := s.Network.GetNode(ctx, area.StationID)
	if errors.Is(err, ports.ErrNotFound) {
		return in, nil
	}
	if err != nil {
		return in, err
	}
	if st := ToSearchNode(stationRow); st.Loc != nil {
		in.Station, in.AreaRegionID = &st, area.RegionID
		in.Nodes[st.ID] = st
	} else {
		return in, nil
	}
	return in, s.fill(ctx, &in, area.StationID, lines)
}

func (s *Service) fill(ctx context.Context, in *search.Input, stationID string, lines []shared.Line) error {
	items := make([]string, len(lines))
	for i, l := range lines {
		items[i] = l.CatalogItemID
	}
	net, err := s.Network.Load(ctx, readmodel.Query{StationID: stationID, ItemIDs: items, From: in.Now, Until: in.Now.Add(SearchHorizon)})
	if err != nil {
		return err
	}
	nodeIDs := make([]string, 0, len(net.Nodes))
	for _, n := range net.Nodes {
		in.Nodes[n.ID] = ToSearchNode(n)
		nodeIDs = append(nodeIDs, n.ID)
	}
	slices.Sort(nodeIDs)
	holds, err := s.Promises.HeldQuantities(ctx, nodeIDs)
	if err != nil {
		return err
	}
	in.Available = availability(net, holds)
	for _, sc := range net.Scorecards {
		in.Grades[sc.NodeID] = search.Grade(sc.Grade)
	}
	in.Lanes, in.Departures, in.Waves = net.Lanes, net.Departures, net.Waves
	books, err := s.Books.List(ctx, repository.BookFilter{NodeIDs: nodeIDs, CutoffFrom: in.Now})
	if err != nil {
		return err
	}
	for _, b := range books {
		in.Books[string(b.ID())] = search.BookView{Open: b.State() == book.StateOpen, Room: b.Room(), HoldDeadlineAt: b.HoldDeadlineAt()}
	}
	return nil
}

// availability is the units of an item shippable from a node: the listing's
// availableQty, 0 when the listing is suppressed or the item discontinued,
// minus the units provisional promises hold (ADR 0002, A3).
func availability(net readmodel.Network, holds map[repository.HoldKey]int) func(node, item string) int {
	discontinued := map[string]bool{}
	for _, it := range net.Items {
		discontinued[it.ID] = it.Discontinued
	}
	qty := map[repository.HoldKey]int{}
	for _, l := range net.Listings {
		if !l.Suppressed && !discontinued[l.ItemID] {
			qty[repository.HoldKey{NodeID: l.NodeID, ItemID: l.ItemID}] += l.AvailableQty
		}
	}
	return func(node, item string) int {
		k := repository.HoldKey{NodeID: node, ItemID: item}
		return max(0, qty[k]-holds[k])
	}
}

// nodeAvailability returns the net units of the lines' items at one node, for
// the ErrGroupSpansNodes check of MakePromise.
func (s *Service) nodeAvailability(ctx context.Context, nodeID string, lines []shared.Line) (map[string]int, error) {
	items := make([]string, len(lines))
	for i, l := range lines {
		items[i] = l.CatalogItemID
	}
	listings, err := s.Network.ListingsForNode(ctx, nodeID, items)
	if err != nil {
		return nil, err
	}
	holds, err := s.Promises.HeldQuantities(ctx, []string{nodeID})
	if err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, l := range listings {
		if l.Suppressed {
			continue
		}
		if it, err := s.Network.GetItem(ctx, l.ItemID); err == nil && it.Discontinued {
			continue
		} else if err != nil && !errors.Is(err, ports.ErrNotFound) {
			return nil, err
		}
		out[l.ItemID] += l.AvailableQty
	}
	for k, h := range holds {
		if k.NodeID == nodeID {
			out[k.ItemID] = max(0, out[k.ItemID]-h)
		}
	}
	return out, nil
}
