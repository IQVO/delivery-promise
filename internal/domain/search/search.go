package search

import (
	"cmp"
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/calendar"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Search bounds.
const (
	// MaxPickups is how many pickup cutoffs per node a search looks at.
	MaxPickups = 6
	// MaxCandidates is how many ranked candidates per (group, tier) are kept.
	MaxCandidates = 5
	// NominalWindowStart and NominalWindowEnd bound the delivery window of a
	// NOMINAL path (ADR 0002, A1), in the destination station's local time.
	NominalWindowStart = 9 * 60
	NominalWindowEnd   = 21 * 60
)

type searcher struct {
	in           Input
	lanes        map[string]Lane
	byOrigin     map[string][]Departure
	packages     int
	stationSched calendar.Schedule
}

// Run executes the search over the snapshot.
func Run(in Input) Result {
	s := newSearcher(in)
	res := Result{WholeQuote: map[shared.Tier]Constraint{}}
	tiers := s.requestedTiers()
	if s.in.Station == nil {
		for _, t := range tiers {
			res.WholeQuote[t] = DestinationNotServed
		}
		return res
	}
	tiers = s.afterDeadlines(tiers, res.WholeQuote)
	if len(tiers) == 0 {
		return res
	}
	nodes := s.candidateNodes()
	groups, ok := s.groupLines(nodes)
	if !ok {
		for _, t := range tiers {
			res.WholeQuote[t] = ItemQuantityNotAvailable
		}
		return res
	}
	for i, lines := range groups {
		res.Groups = append(res.Groups, s.searchGroup(i+1, lines, nodes, tiers))
	}
	return res
}

func newSearcher(in Input) *searcher {
	if in.Policy == nil {
		in.Policy = policy.Default()
	}
	s := &searcher{in: in, lanes: map[string]Lane{}, byOrigin: map[string][]Departure{}, packages: in.Policy.PackagesPerOrder()}
	for _, l := range in.Lanes {
		s.lanes[l.ID] = l
	}
	for _, d := range in.Departures {
		s.byOrigin[d.Origin] = append(s.byOrigin[d.Origin], d)
	}
	for _, ds := range s.byOrigin {
		slices.SortFunc(ds, func(a, b Departure) int { return cmp.Or(a.CutoffAt.Compare(b.CutoffAt), cmp.Compare(a.ID, b.ID)) })
	}
	if in.Station != nil {
		loc := in.Station.Loc
		if loc == nil {
			loc = time.UTC
		}
		s.stationSched = calendar.Schedule{Loc: loc}
	}
	return s
}

// timeZone is the IANA name of the delivery station's zone: the local calendar
// every window of this search belongs to (ADR 0011).
func (s *searcher) timeZone() string { return s.stationSched.Loc.String() }

func (s *searcher) requestedTiers() []shared.Tier {
	if len(s.in.Tiers) == 0 {
		return shared.AllTiers()
	}
	out := slices.Clone(s.in.Tiers)
	slices.SortFunc(out, func(a, b shared.Tier) int { return a.Rank() - b.Rank() })
	return slices.Compact(out)
}

// afterDeadlines records PAST_ORDER_DEADLINE for the tiers whose order
// deadline in the destination region has passed and returns the others.
func (s *searcher) afterDeadlines(tiers []shared.Tier, whole map[shared.Tier]Constraint) []shared.Tier {
	local := s.in.Now.In(s.stationSched.Loc)
	minute := local.Hour()*60 + local.Minute()
	var open []shared.Tier
	for _, t := range tiers {
		if s.in.Policy.PastDeadline(t, s.in.AreaRegionID, minute) {
			whole[t] = PastOrderDeadline
			continue
		}
		open = append(open, t)
	}
	return open
}

// candidateNodes returns the ACTIVE fulfillment centres with an operational
// profile that the input does not exclude, by node id.
func (s *searcher) candidateNodes() []Node {
	var out []Node
	for _, n := range s.in.Nodes {
		if n.Type != TypeFulfillmentCenter || !n.Active || n.Profile == nil || s.in.ExcludeNodes[n.ID] {
			continue
		}
		if s.in.OnlyNode != "" && n.ID != s.in.OnlyNode {
			continue
		}
		out = append(out, n)
	}
	slices.SortFunc(out, func(a, b Node) int { return cmp.Compare(a.ID, b.ID) })
	return out
}

func (s *searcher) covers(n Node, l shared.Line) bool {
	return s.in.Available(n.ID, l.CatalogItemID) >= l.Quantity
}

func (s *searcher) groupLines(nodes []Node) ([][]shared.Line, bool) {
	covers := make(map[string]map[string]bool, len(nodes))
	for _, n := range nodes {
		covers[n.ID] = map[string]bool{}
		for _, l := range s.in.Lines {
			if s.covers(n, l) {
				covers[n.ID][l.CatalogItemID] = true
			}
		}
	}
	if s.in.SingleGroup {
		for _, n := range nodes {
			if countCovered(s.in.Lines, covers[n.ID]) == len(s.in.Lines) {
				return [][]shared.Line{slices.Clone(s.in.Lines)}, true
			}
		}
		return nil, false
	}
	return GroupLines(s.in.Lines, covers)
}

func (s *searcher) searchGroup(no int, lines []shared.Line, nodes []Node, tiers []shared.Tier) Group {
	g := Group{No: no, Lines: lines, Candidates: map[shared.Tier][]Candidate{}, Unavailable: map[shared.Tier]Constraint{}}
	var eligible []Node
	for _, n := range nodes {
		if s.coversAll(n, lines) {
			eligible = append(eligible, n)
		}
	}
	strict := s.pathsOf(eligible, true)
	var loose []Candidate
	looseDone := false
	for _, t := range tiers {
		cs := s.forTier(strict, t, eligible, true)
		if len(cs) == 0 {
			if !looseDone {
				loose, looseDone = s.pathsOf(eligible, false), true
			}
			g.Unavailable[t] = DeliverySLAUnavailable
			if len(s.forTier(loose, t, eligible, false)) > 0 {
				g.Unavailable[t] = NodeCapacityExhausted
			}
			continue
		}
		g.Candidates[t] = cs
	}
	return g
}

func (s *searcher) coversAll(n Node, lines []shared.Line) bool {
	for _, l := range lines {
		if !s.covers(n, l) {
			return false
		}
	}
	return true
}

func (s *searcher) pathsOf(nodes []Node, strict bool) []Candidate {
	var out []Candidate
	for _, n := range nodes {
		out = append(out, s.scheduled(n, strict)...)
	}
	return out
}

// forTier ranks the candidates that fit the tier; for STANDARD without any
// scheduled candidate it falls back to the nominal paths (basis NOMINAL).
func (s *searcher) forTier(scheduled []Candidate, t shared.Tier, eligible []Node, strict bool) []Candidate {
	cs := s.fit(scheduled, t)
	if len(cs) == 0 && t.AllowsNominal() {
		var nom []Candidate
		for _, n := range eligible {
			nom = append(nom, s.nominal(n, strict)...)
		}
		cs = s.fit(nom, t)
	}
	slices.SortFunc(cs, rank)
	if len(cs) > MaxCandidates {
		cs = cs[:MaxCandidates]
	}
	return cs
}

func (s *searcher) fit(cs []Candidate, t shared.Tier) []Candidate {
	var out []Candidate
	for _, c := range cs {
		if s.stationSched.DaysBetween(s.in.Now, c.Window.Latest) > t.MaxCalendarDays() {
			continue
		}
		if t == shared.TierSameDay && !c.Direct {
			continue
		}
		c.Tier = t
		out = append(out, c)
	}
	return out
}

// rank orders candidates: earliest window end, then in-region, fewer legs,
// better grade, then ids for determinism (ADR 0002, A9).
func rank(a, b Candidate) int {
	if c := a.Window.Latest.Compare(b.Window.Latest); c != 0 {
		return c
	}
	if a.InRegion != b.InRegion {
		if a.InRegion {
			return -1
		}
		return 1
	}
	return cmp.Or(a.LegCount-b.LegCount, a.Grade.Rank()-b.Grade.Rank(), cmp.Compare(a.NodeID, b.NodeID), a.ShipBy.Compare(b.ShipBy))
}

// bookFor returns the book id of a node pickup and, when the book does not
// exist yet, the opening to create; ok is false when strict and the book cannot
// take a hold.
func (s *searcher) bookFor(n Node, pickupID string, cutoff time.Time, strict bool) (book.ID, book.Opening, bool) {
	id := book.MakeID(n.ID, pickupID)
	if bv, exists := s.in.Books[string(id)]; exists {
		if strict && (!bv.Open || bv.Room < 1 || !s.in.Now.Before(bv.HoldDeadlineAt)) {
			return id, book.Opening{}, false
		}
		return id, book.Opening{}, true
	}
	op, ok := OpeningFor(n, pickupID, cutoff, s.in.Policy)
	if !ok || strict && !s.in.Now.Before(op.HoldDeadlineAt) {
		return id, op, false
	}
	return id, op, true
}

// expiry returns the offer expiry for a pickup cutoff and whether the
// candidate can still be met: now < cutoff minus the effective handling.
func (s *searcher) expiry(p *Profile, cutoff time.Time, handling int) (expires, meet time.Time, ok bool) {
	meet, found := p.Schedule.SubBusinessMinutes(cutoff, handling)
	if !found || !meet.After(s.in.Now) {
		return time.Time{}, time.Time{}, false
	}
	expires = s.in.Now.Add(s.in.Policy.OfferTTL())
	if meet.Before(expires) {
		expires = meet
	}
	return expires, meet, true
}

// scheduled builds the SCHEDULED candidates of one node.
func (s *searcher) scheduled(n Node, strict bool) []Candidate {
	p := n.Profile
	handling, skip := Handicap(s.in.Grades[n.ID], p.HandlingMinutes)
	ready, ok := p.Schedule.AddBusinessMinutes(s.in.Now, handling)
	if !ok {
		return nil
	}
	var pickups []Departure
	for _, d := range s.byOrigin[n.ID] {
		if d.Kind != KindPickup || d.CutoffAt.Before(ready) || !s.usable(d, strict) {
			continue
		}
		if _, _, okBook := s.bookFor(n, d.ID, d.CutoffAt, strict); okBook {
			pickups = append(pickups, d)
		}
	}
	if skip >= len(pickups) {
		return nil
	}
	pickups = pickups[skip:]
	if len(pickups) > MaxPickups {
		pickups = pickups[:MaxPickups]
	}
	var out []Candidate
	for _, d := range pickups {
		if c, okC := s.candidateFrom(n, d, handling, strict); okC {
			out = append(out, c)
		}
	}
	return out
}

func (s *searcher) candidateFrom(n Node, d Departure, handling int, strict bool) (Candidate, bool) {
	expires, meet, ok := s.expiry(n.Profile, d.CutoffAt, handling)
	if !ok {
		return Candidate{}, false
	}
	arr, found := s.earliestArrival(d, strict)
	if !found {
		return Candidate{}, false
	}
	w, found := s.firstWave(arr.at, strict)
	if !found || !d.CutoffAt.Before(w.WindowStart) {
		return Candidate{}, false
	}
	id, op, _ := s.bookFor(n, d.ID, d.CutoffAt, false)
	return Candidate{
		NodeID: n.ID, ShipBy: d.CutoffAt, Window: shared.Window{Earliest: w.WindowStart, Latest: w.WindowEnd, TimeZone: s.timeZone()}, Basis: shared.BasisScheduled,
		Witness: shared.Witness{NodeID: n.ID, PickupDepartureID: d.ID, Legs: arr.legs, DeliveryWaveID: w.ID}, BookID: string(id),
		ExpiresAt: expires, LatestMeetableAt: meet, Grade: s.in.Grades[n.ID], InRegion: n.RegionID == s.in.AreaRegionID,
		LegCount: len(arr.legs), Direct: d.Dest == s.in.Station.ID && (n.Subtype == SubtypeSubSameDay || n.Subtype == SubtypeMicro), Opening: op,
	}, true
}
