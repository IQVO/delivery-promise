package readmodel

import (
	"math"
	"slices"
	"time"
)

// MaxTransitObservations caps the ring of one (origin, dest) pair: the latest
// observations are kept, the rest are dropped (ADR 0014).
const MaxTransitObservations = 200

// TransitObservation is one measured transit of a (origin, dest) pair: how
// many minutes a load actually took, stamped with the instant it arrived
// (ADR 0014).
type TransitObservation struct {
	Minutes    int       `json:"minutes"`
	ObservedAt time.Time `json:"observedAt"`
}

// AppendTransitObservation appends one observation to a pair's ring: it prunes
// the observations older than windowDays around now and keeps the latest
// MaxTransitObservations. The ring is stored newest-last.
func AppendTransitObservation(ring []TransitObservation, obs TransitObservation, windowDays int, now time.Time) []TransitObservation {
	ring = append(ring, obs)
	ring = PruneTransitWindow(ring, windowDays, now)
	if over := len(ring) - MaxTransitObservations; over > 0 {
		ring = ring[over:]
	}
	return ring
}

// PruneTransitWindow drops the observations observed before now minus
// windowDays days.
func PruneTransitWindow(ring []TransitObservation, windowDays int, now time.Time) []TransitObservation {
	cutoff := now.AddDate(0, 0, -windowDays)
	return slices.DeleteFunc(ring, func(o TransitObservation) bool { return o.ObservedAt.Before(cutoff) })
}

// TransitPercentile answers the measured transit of a pair: the nearest-rank
// percentile percentile over the minutes of the in-window observations, ok
// false while there are fewer than minObservations of them (ADR 0014).
func TransitPercentile(ring []TransitObservation, percentile, minObservations, windowDays int, now time.Time) (bool, int) {
	in := PruneTransitWindow(slices.Clone(ring), windowDays, now)
	if len(in) < minObservations {
		return false, 0
	}
	minutes := make([]int, len(in))
	for i, o := range in {
		minutes[i] = o.Minutes
	}
	slices.Sort(minutes)
	rank := int(math.Ceil(float64(percentile) * float64(len(minutes)) / 100))
	if rank < 1 {
		rank = 1
	}
	if rank > len(minutes) {
		rank = len(minutes)
	}
	return true, minutes[rank-1]
}
