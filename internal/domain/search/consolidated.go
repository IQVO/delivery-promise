package search

import (
	"cmp"
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// The constants of the CONSOLIDATED tier (ADR 0024). A policy field for them is
// a deferred follow-up.
const (
	// DefaultConsolidatedOffsetDays is how many local days after the fastest
	// feasible window the chosen day falls when the request names none.
	DefaultConsolidatedOffsetDays = 2
	// MinConsolidationHoldDays is how many local days after the fastest
	// feasible window a chosen day must fall for the option to be
	// consolidationEligible.
	MinConsolidationHoldDays = 1
	// consolidatedDayLayout is the layout of Input.ConsolidatedDay.
	consolidatedDayLayout = "2006-01-02"
)

// consolidated fills the CONSOLIDATED tier of a group: the candidates of the
// chosen local day, or the constraint that blocked the tier.
func (s *searcher) consolidated(g *Group, paths *pathSet, eligible []Node) {
	t := shared.TierConsolidated
	cs := s.consolidatedSet(paths, eligible, true)
	if len(cs) == 0 {
		g.Unavailable[t] = DeliverySLAUnavailable
		if len(s.consolidatedSet(paths, eligible, false)) > 0 {
			g.Unavailable[t] = NodeCapacityExhausted
		}
		return
	}
	g.Candidates[t] = s.owned(cs, g.Lines)
}

// consolidatedSet builds the ranked CONSOLIDATED candidates from the group's own
// STANDARD paths: the fastest STANDARD window fixes the earliest local day, the
// chosen day is at or after it, and every candidate ends on a wave of that day.
// With a minimum day it keeps the candidates of every day from there to the
// horizon, day by day.
func (s *searcher) consolidatedSet(paths *pathSet, eligible []Node, strict bool) []Candidate {
	std := s.forTier(paths.of(shared.TierStandard, strict), shared.TierStandard, eligible, strict)
	if len(std) == 0 {
		return nil
	}
	fastest := s.dayOffset(std[0].Window.Latest)
	first := s.chosenDay(fastest)
	last := first
	if !s.in.ConsolidatedNotBefore.IsZero() {
		last = shared.MaxConsolidatedCalendarDays
	}
	var out []Candidate
	for k := first; k <= last && k <= shared.MaxConsolidatedCalendarDays; k++ {
		out = append(out, s.consolidatedOn(k, fastest, eligible, strict)...)
	}
	return out
}

// dayOffset is the number of local days from today to the local date of t.
func (s *searcher) dayOffset(t time.Time) int { return s.stationSched.DaysBetween(s.in.Now, t) }

// chosenDay is the first local day, as an offset from today, a CONSOLIDATED
// candidate may end on: the requested day, else the default offset after the
// fastest day, never before the fastest day.
func (s *searcher) chosenDay(fastest int) int {
	k := fastest + DefaultConsolidatedOffsetDays
	if d, err := time.ParseInLocation(consolidatedDayLayout, s.in.ConsolidatedDay, s.stationSched.Loc); err == nil {
		k = s.dayOffset(d)
	}
	if !s.in.ConsolidatedNotBefore.IsZero() {
		k = s.dayOffset(s.in.ConsolidatedNotBefore)
	}
	return max(k, fastest)
}

// consolidatedOn ranks the candidates that end on local day k (an offset from
// today): scheduled paths first, the NOMINAL fallback when there is none.
func (s *searcher) consolidatedOn(k, fastest int, eligible []Node, strict bool) []Candidate {
	t := shared.TierConsolidated
	onDay := func(w Wave) bool { return s.dayOffset(w.WindowEnd) == k }
	wave := func(at time.Time) (Wave, bool) { return s.waveWhere(at, strict, onDay) }
	var cs []Candidate
	for _, n := range eligible {
		cs = append(cs, s.scheduled(n, strict, t, wave)...)
	}
	cs = s.fit(cs, t)
	if len(cs) == 0 {
		var nom []Candidate
		for _, n := range eligible {
			nom = append(nom, s.nominal(n, strict, t, s.nominalOn(k))...)
		}
		cs = s.fit(nom, t)
	}
	slices.SortFunc(cs, rankHold)
	cs = cs[:min(len(cs), MaxCandidates)]
	hold := k >= fastest+MinConsolidationHoldDays
	for i := range cs {
		cs[i].ConsolidationEligible = &hold
	}
	return cs
}

// nominalOn is the NOMINAL window of local day k, 09:00 to 21:00; a nominal
// arrival that is not before that day cannot make it.
func (s *searcher) nominalOn(k int) nominalWindowFn {
	return func(arrive time.Time) (shared.Window, bool) {
		if s.dayOffset(arrive) >= k {
			return shared.Window{}, false
		}
		loc := s.stationSched.Loc
		y, m, d := s.in.Now.In(loc).Date()
		return shared.Window{
			Earliest: time.Date(y, m, d+k, NominalWindowStart/60, NominalWindowStart%60, 0, 0, loc).UTC(),
			Latest:   time.Date(y, m, d+k, NominalWindowEnd/60, NominalWindowEnd%60, 0, 0, loc).UTC(),
			TimeZone: s.timeZone(),
		}, true
	}
}

// rankHold orders CONSOLIDATED candidates like rank (earliest window end, in
// region), then the LATER pickup first, the longest hold, then fewer legs,
// better grade and ids (ADR 0024).
func rankHold(a, b Candidate) int {
	if c := a.Window.Latest.Compare(b.Window.Latest); c != 0 {
		return c
	}
	if a.InRegion != b.InRegion {
		if a.InRegion {
			return -1
		}
		return 1
	}
	return cmp.Or(b.ShipBy.Compare(a.ShipBy), a.LegCount-b.LegCount, a.Grade.Rank()-b.Grade.Rank(), cmp.Compare(a.NodeID, b.NodeID))
}
