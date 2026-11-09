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
// measured says the path used at least one departure whose arrival the
// measured-transit read model pushed later than the timetable (ADR 0014).
type arrival struct {
	at       time.Time
	legs     []string
	measured bool
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

// measuredAt returns a departure's arrival at its destination, pushed later by
// the measured transit of its (origin, dest) pair when that exceeds the
// scheduled transit arrivesAt - departsAt: a one-sided adjustment, measured
// transit never promises earlier than the timetable (ADR 0014).
func (s *searcher) measuredAt(d Departure) (time.Time, bool) {
	return ExpectedArrival(d, s.in.MeasuredTransit)
}

// earliestArrival is the time-expanded earliest-arrival search from the
// pickup's destination to the delivery station: at every intermediate node it
// adds the sort dwell, then takes the first departure whose cutoff is not
// before that instant and that has room. Ties go to the path with fewer legs.
// Every departure, the pickup included, arrives at its measured transit when
// that is later than its scheduled one (ADR 0014).
func (s *searcher) earliestArrival(pickup Departure, strict bool) (arrival, bool) {
	station := s.in.Station.ID
	start, startMeasured := s.measuredAt(pickup)
	if pickup.Dest == station {
		return arrival{at: start, measured: startMeasured}, true
	}
	best := map[string]time.Time{pickup.Dest: start}
	measured := map[string]bool{pickup.Dest: startMeasured}
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
			at, legMeasured := s.measuredAt(d)
			if cur, seen := best[d.Dest]; !seen || at.Before(cur) || at.Equal(cur) && s.legCount(via, u)+1 < s.legCount(via, d.Dest) {
				best[d.Dest] = at
				via[d.Dest] = leg{dep: d, prev: u}
				measured[d.Dest] = legMeasured || measured[u]
			}
		}
	}
	at, ok := best[station]
	if !ok {
		return arrival{}, false
	}
	return arrival{at: at, legs: s.legIDs(via, station, pickup.Dest), measured: measured[station]}, true
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
