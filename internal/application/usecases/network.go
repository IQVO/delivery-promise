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
	// existingPromise marks the re-promising of a promise already made: the
	// fast-tier eligibility filter applies to future quotes only (ADR 0022).
	existingPromise bool
	// consolidatedDay and consolidatedNotBefore are the CONSOLIDATED tier's
	// chosen local day, and the instant whose local day is its minimum when a
	// promise is re-promised (ADR 0024).
	consolidatedDay       string
	consolidatedNotBefore time.Time
}

// horizonFor is how far ahead of now the read models are loaded for a search:
// the CONSOLIDATED tier reaches MaxConsolidatedCalendarDays local days out.
func horizonFor(tiers []shared.Tier) time.Duration {
	if slices.Contains(tiers, shared.TierConsolidated) {
		return max(SearchHorizon, time.Duration(shared.MaxConsolidatedCalendarDays+1)*24*time.Hour)
	}
	return SearchHorizon
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
		Stock: func(string, string) []shared.OwnerStock { return nil }, MeasuredTransit: s.measuredTransitOf(ctx, pol, now),
		CalibrationBuffer: s.calibrationBufferOf(ctx, pol, now),
		ConsolidatedDay:   opt.consolidatedDay, ConsolidatedNotBefore: opt.consolidatedNotBefore,
	}
	var err error
	if !opt.existingPromise {
		if in.TierIneligible, err = s.tierIneligibleOf(ctx, tiers); err != nil {
			return in, err
		}
	}
	area, err := s.Network.GetArea(ctx, prefix)
	if errors.Is(err, ports.ErrNotFound) {
		return in, nil
	}
	if err != nil {
		return in, err
	}
	// A CARRIER_HANDOFF area promises on the carrier options instead of waves;
	// an area without a mode, or one whose options never arrived, stays on
	// waves exactly as before (ADR 0026).
	if area.DeliveryMode == readmodel.AreaCarrierHandoff && len(area.Carriers) > 0 {
		in.Carrier = &search.CarrierArea{Options: area.Carriers}
	}
	in.MeasuredCarrierTransit = s.measuredCarrierTransitOf(ctx, pol, now, prefix)
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

// measuredTransitOf adapts the measured-transit read model to the search's
// closure: the current policy's service-level percentile, minimum observations
// and window (ADR 0014). A service without the port never measures.
func (s *Service) measuredTransitOf(ctx context.Context, pol *policy.Policy, now time.Time) func(origin, dest string) (bool, int) {
	if s.TransitActuals == nil {
		return nil
	}
	percentile, minObs, window := pol.TransitServiceLevelPercent(), pol.MinTransitObservations(), pol.TransitWindowDays()
	return func(origin, dest string) (bool, int) {
		return s.TransitActuals.MeasuredTransit(ctx, origin, dest, percentile, minObs, window, now)
	}
}

// measuredCarrierTransitOf is measuredTransitOf for the final leg of a
// CARRIER_HANDOFF area: the same ring asked with the dest "area:"+prefix, so
// carrier observations never mix with linehaul ones (ADR 0026). A service
// without the port never measures.
func (s *Service) measuredCarrierTransitOf(ctx context.Context, pol *policy.Policy, now time.Time, prefix string) func(handoffNodeID string) (bool, int) {
	if s.TransitActuals == nil {
		return nil
	}
	percentile, minObs, window := pol.TransitServiceLevelPercent(), pol.MinTransitObservations(), pol.TransitWindowDays()
	dest := "area:" + prefix
	return func(handoffNodeID string) (bool, int) {
		return s.TransitActuals.MeasuredTransit(ctx, handoffNodeID, dest, percentile, minObs, window, now)
	}
}

func (s *Service) fill(ctx context.Context, in *search.Input, stationID string, lines []shared.Line) error {
	items := make([]string, len(lines))
	for i, l := range lines {
		items[i] = l.CatalogItemID
	}
	net, err := s.Network.Load(ctx, readmodel.Query{StationID: stationID, ItemIDs: items, From: in.Now, Until: in.Now.Add(horizonFor(in.Tiers))})
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
	confirmed, err := s.Promises.ConfirmedHolds(ctx, nodeIDs)
	if err != nil {
		return err
	}
	addUnaccounted(holds, confirmed, latestAsOf(net.Listings))
	in.Stock = availability(net, holds)
	for _, sc := range net.Scorecards {
		in.Grades[sc.NodeID] = search.Grade(sc.Grade)
	}
	in.Lanes, in.Departures, in.Waves = net.Lanes, net.Departures, net.Waves
	books, err := s.Books.List(ctx, repository.BookFilter{NodeIDs: nodeIDs, CutoffFrom: in.Now})
	if err != nil {
		return err
	}
	for _, b := range books {
		in.Books[string(b.ID())] = search.BookView{
			Open: b.State() == book.StateOpen, Room: b.Room(), HoldDeadlineAt: b.HoldDeadlineAt(),
			Share: b.Share(), Used: b.Provisional() + b.Confirmed(),
		}
	}
	return nil
}
