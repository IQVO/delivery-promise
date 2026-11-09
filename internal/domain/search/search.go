package search

import (
	"cmp"
	"slices"
	"time"

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
	_, ok := s.ownerOf(n.ID, l)
	return ok
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
	paths := pathSet{s: s, nodes: eligible, byKey: map[pathKey][]Candidate{}}
	for _, t := range tiers {
		cs := s.forTier(paths.of(t, true), t, eligible, true)
		if len(cs) == 0 {
			g.Unavailable[t] = DeliverySLAUnavailable
			if len(s.forTier(paths.of(t, false), t, eligible, false)) > 0 {
				g.Unavailable[t] = NodeCapacityExhausted
			}
			continue
		}
		g.Candidates[t] = s.owned(cs, lines)
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

func (s *searcher) pathsOf(nodes []Node, strict bool, t shared.Tier) []Candidate {
	var out []Candidate
	for _, n := range nodes {
		out = append(out, s.scheduled(n, strict, t)...)
	}
	return out
}
