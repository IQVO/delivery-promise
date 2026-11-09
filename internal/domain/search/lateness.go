package search

import "time"

// LateRiskGrace is how long past its expected arrival a departure may run
// before the late-risk sweep calls it late (ADR 0021).
const LateRiskGrace = 15 * time.Minute

// MeasuredTransitFunc answers the measured transit in minutes of an (origin,
// dest) pair at the policy's service level; ok is false while the pair has too
// few observations. It is Input.MeasuredTransit.
type MeasuredTransitFunc = func(origin, dest string) (ok bool, minutes int)

// ExpectedArrival is a departure's arrival at its destination pushed later by
// the measured transit of its (origin, dest) pair when that exceeds the
// scheduled transit arrivesAt - departsAt: a one-sided adjustment, measured
// transit never promises earlier than the timetable (ADR 0014). The search
// (measuredAt) and the late-risk sweep (ADR 0021) both call it, so they never
// disagree about when a load is expected. adjusted says the arrival was pushed
// later than the timetable; a nil measured never adjusts.
func ExpectedArrival(d Departure, measured MeasuredTransitFunc) (at time.Time, adjusted bool) {
	if measured == nil {
		return d.ArrivesAt, false
	}
	if ok, minutes := measured(d.Origin, d.Dest); ok {
		if delta := float64(minutes) - d.ArrivesAt.Sub(d.DepartsAt).Minutes(); delta > 0 {
			return d.ArrivesAt.Add(time.Duration(delta) * time.Minute), true
		}
	}
	return d.ArrivesAt, false
}

// PathLeg is one departure of a promise's witness path and whether its arrival
// was seen (a DepartureArrival row exists).
type PathLeg struct {
	Departure Departure
	Arrived   bool
}

// PathRisk is the verdict of AssessPath on a witness path.
type PathRisk struct {
	// Late says a departure with no arrival is past its expected arrival plus
	// LateRiskGrace.
	Late bool
	// LateDepartureID and LateExpected name the first such departure (in path
	// order) and when it was expected.
	LateDepartureID string
	LateExpected    time.Time
	// Projected is the latest expected arrival among the remaining (not yet
	// arrived) departures from the late one on, shifted by the observed
	// lateness of the late departure: when the path reaches the delivery
	// station if the delay propagates unchanged.
	Projected time.Time
	// Dwell is the sort dwell of the intermediate nodes still ahead of the
	// load after the late departure.
	Dwell time.Duration
}

// AssessPath judges a witness path (the pickup then the legs, in order) at
// now. A departure is late when it has no arrival, and now is after its
// expected arrival plus LateRiskGrace. dwell answers a node's sort dwell (nil
// for none). Pure: no clock, no repository.
func AssessPath(legs []PathLeg, measured MeasuredTransitFunc, dwell func(nodeID string) time.Duration, now time.Time) PathRisk {
	first := -1
	var firstExpected time.Time
	for i, l := range legs {
		if l.Arrived {
			continue
		}
		if at, _ := ExpectedArrival(l.Departure, measured); now.After(at.Add(LateRiskGrace)) {
			first, firstExpected = i, at
			break
		}
	}
	if first < 0 {
		return PathRisk{}
	}
	risk := PathRisk{Late: true, LateDepartureID: legs[first].Departure.ID, LateExpected: firstExpected, Projected: firstExpected}
	lateness := now.Sub(firstExpected)
	for i := first; i < len(legs); i++ {
		if legs[i].Arrived {
			continue
		}
		if at, _ := ExpectedArrival(legs[i].Departure, measured); at.After(risk.Projected) {
			risk.Projected = at
		}
		if i < len(legs)-1 && dwell != nil { // the last leg reaches the delivery station: no sort there
			risk.Dwell += dwell(legs[i].Departure.Dest)
		}
	}
	risk.Projected = risk.Projected.Add(lateness)
	return risk
}

// Reachable says the promise's window is still reachable although the path is
// late: the projected arrival, the sort dwell still ahead and the last-mile
// allowance of the witness wave (the wave's window end less its induct
// cutoff, which is what the search lets an arrival at the station cost) end no
// later than the window end. A path that is not late is reachable. A wave that
// is not scheduled or was cancelled never is (ADR 0021).
func (r PathRisk) Reachable(wave Wave, windowEnd time.Time) bool {
	if !r.Late {
		return true
	}
	if !wave.Scheduled || wave.Cancelled {
		return false
	}
	lastMile := max(0, wave.WindowEnd.Sub(wave.InductCutoffAt))
	return !r.Projected.Add(r.Dwell).Add(lastMile).After(windowEnd)
}
