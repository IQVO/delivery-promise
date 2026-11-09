package search

import (
	"cmp"
	"slices"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

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
		if s.ineligible(c.NodeID, t) {
			continue
		}
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

// FastTier says whether eligibility governs the tier: only SAME_DAY and
// NEXT_DAY follow the node-performance gate (ADR 0022).
func FastTier(t shared.Tier) bool { return t == shared.TierSameDay || t == shared.TierNextDay }

// ineligible says the node is removed from the tier's candidates: the tier is a
// fast one and the input reports the node as ineligible for it.
func (s *searcher) ineligible(nodeID string, t shared.Tier) bool {
	return s.in.TierIneligible != nil && FastTier(t) && s.in.TierIneligible(nodeID, t)
}
