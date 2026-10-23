package search

import (
	"slices"
	"time"
)

// leg is a departure on the way to the delivery station.
type leg struct {
	dep  Departure
	prev string
}

// arrival is the result of the earliest-arrival search from a pickup.
type arrival struct {
	at   time.Time
	legs []string
}

func (s *searcher) dwell(nodeID string) time.Duration {
	if n, ok := s.in.Nodes[nodeID]; ok && n.Profile != nil {
		return time.Duration(n.Profile.SortDwellMinutes) * time.Minute
	}
	return 0
}

func (s *searcher) usable(d Departure, strict bool) bool {
	if !d.Scheduled || d.Cancelled || s.in.ExcludeDepartures[d.ID] {
		return false
	}
	if l, ok := s.lanes[d.LaneID]; ok && !l.Open {
		return false
	}
	return !strict || d.Remaining >= s.packages
}

// earliestArrival is the time-expanded earliest-arrival search from the
// pickup's destination to the delivery station: at every intermediate node it
// adds the sort dwell, then takes the first departure whose cutoff is not
// before that instant and that has room. Ties go to the path with fewer legs.
func (s *searcher) earliestArrival(pickup Departure, strict bool) (arrival, bool) {
	station := s.in.Station.ID
	if pickup.Dest == station {
		return arrival{at: pickup.ArrivesAt}, true
	}
	best := map[string]time.Time{pickup.Dest: pickup.ArrivesAt}
	via := map[string]leg{}
	done := map[string]bool{}
	for {
		u, ok := s.nextNode(best, done, station)
		if !ok {
			break
		}
		done[u] = true
		ready := best[u].Add(s.dwell(u))
		for _, d := range s.departuresFrom(u) {
			if d.Kind == KindPickup || d.CutoffAt.Before(ready) || !s.usable(d, strict) || done[d.Dest] {
				continue
			}
			if cur, seen := best[d.Dest]; !seen || d.ArrivesAt.Before(cur) || d.ArrivesAt.Equal(cur) && s.legCount(via, u)+1 < s.legCount(via, d.Dest) {
				best[d.Dest] = d.ArrivesAt
				via[d.Dest] = leg{dep: d, prev: u}
			}
		}
	}
	at, ok := best[station]
	if !ok {
		return arrival{}, false
	}
	return arrival{at: at, legs: s.legIDs(via, station, pickup.Dest)}, true
}

// nextNode picks the unsettled node with the earliest arrival, other than the
// delivery station (the search ends there).
func (s *searcher) nextNode(best map[string]time.Time, done map[string]bool, station string) (string, bool) {
	pick := ""
	for id, at := range best {
		if done[id] || id == station {
			continue
		}
		if pick == "" || at.Before(best[pick]) || at.Equal(best[pick]) && id < pick {
			pick = id
		}
	}
	return pick, pick != ""
}

func (s *searcher) departuresFrom(nodeID string) []Departure { return s.byOrigin[nodeID] }

func (s *searcher) legCount(via map[string]leg, node string) int {
	n := 0
	for {
		l, ok := via[node]
		if !ok {
			return n
		}
		n++
		node = l.prev
	}
}

func (s *searcher) legIDs(via map[string]leg, to, from string) []string {
	var ids []string
	for node := to; node != from; {
		l, ok := via[node]
		if !ok {
			break
		}
		ids = append(ids, l.dep.ID)
		node = l.prev
	}
	slices.Reverse(ids)
	return ids
}

// firstWave is the first wave at the station whose induct cutoff is not
// before the arrival, not cancelled and with stops left.
func (s *searcher) firstWave(at time.Time, strict bool) (Wave, bool) {
	var best Wave
	found := false
	for _, w := range s.in.Waves {
		if !w.Scheduled || w.Cancelled || s.in.ExcludeWaves[w.ID] || w.Station != s.in.Station.ID || w.InductCutoffAt.Before(at) {
			continue
		}
		if strict && w.Remaining < 1 {
			continue
		}
		if !found || w.InductCutoffAt.Before(best.InductCutoffAt) || w.InductCutoffAt.Equal(best.InductCutoffAt) && w.WindowEnd.Before(best.WindowEnd) {
			best, found = w, true
		}
	}
	return best, found
}
