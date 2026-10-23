package search

import (
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// transit returns the shortest nominal transit from a node to the delivery
// station over OPEN lanes, adding the sort dwell of every intermediate node.
func (s *searcher) transit(from string) (time.Duration, bool) {
	station := s.in.Station.ID
	best := map[string]time.Duration{from: 0}
	done := map[string]bool{}
	for {
		u, d, ok := s.nearest(best, done)
		if !ok {
			return 0, false
		}
		if u == station {
			return d, true
		}
		done[u] = true
		step := d
		if u != from {
			step += s.dwell(u)
		}
		for _, l := range s.in.Lanes {
			if !l.Open || l.Origin != u || done[l.Dest] {
				continue
			}
			via := step + time.Duration(l.NominalMinutes)*time.Minute
			if cur, seen := best[l.Dest]; !seen || via < cur {
				best[l.Dest] = via
			}
		}
	}
}

func (s *searcher) nearest(best map[string]time.Duration, done map[string]bool) (string, time.Duration, bool) {
	pick, found := "", false
	var at time.Duration
	for id, d := range best {
		if done[id] {
			continue
		}
		if !found || d < at || d == at && id < pick {
			pick, at, found = id, d, true
		}
	}
	return pick, at, found
}

// nominalWindow is the delivery window of a nominal path: the destination
// station's next local calendar day after the arrival, 09:00 to 21:00.
func (s *searcher) nominalWindow(arrive time.Time) shared.Window {
	loc := s.stationSched.Loc
	y, m, d := arrive.In(loc).Date()
	return shared.Window{
		Earliest: time.Date(y, m, d+1, NominalWindowStart/60, NominalWindowStart%60, 0, 0, loc).UTC(),
		Latest:   time.Date(y, m, d+1, NominalWindowEnd/60, NominalWindowEnd%60, 0, 0, loc).UTC(),
	}
}

// nominal builds the NOMINAL candidates of one node (STANDARD only): the
// node's next profile cutoffs plus the nominal lane transit (ADR 0002, A1).
func (s *searcher) nominal(n Node, strict bool) []Candidate {
	p := n.Profile
	handling, skip := Handicap(s.in.Grades[n.ID], p.HandlingMinutes)
	ready, ok := p.Schedule.AddBusinessMinutes(s.in.Now, handling)
	if !ok {
		return nil
	}
	tr, ok := s.transit(n.ID)
	if !ok {
		return nil
	}
	cutoffs := p.Schedule.NextCutoffs(p.Cutoffs, ready, skip+MaxPickups)
	if skip >= len(cutoffs) {
		return nil
	}
	var out []Candidate
	for _, c := range cutoffs[skip:] {
		pickupID := NominalPickupID(c.At)
		id, op, okBook := s.bookFor(n, pickupID, c.At, strict)
		expires, meet, okExp := s.expiry(p, c.At, handling)
		if !okBook || !okExp {
			continue
		}
		w := s.nominalWindow(c.At.Add(tr))
		if !c.At.Before(w.Earliest) {
			continue
		}
		out = append(out, Candidate{
			NodeID: n.ID, ShipBy: c.At, Window: w, Basis: shared.BasisNominal, Witness: shared.Witness{NodeID: n.ID}, BookID: string(id),
			ExpiresAt: expires, LatestMeetableAt: meet, Grade: s.in.Grades[n.ID], InRegion: n.RegionID == s.in.AreaRegionID, Opening: op,
		})
	}
	return out
}
