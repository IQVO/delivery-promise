package readmodel

import (
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// MaxCalibrationObservations caps the ring of one calibration segment: the
// latest observations are kept, the rest are dropped (ADR 0020).
const MaxCalibrationObservations = 500

// CalibrationKey is the segment of PromiseCalibrationView: the tier and basis
// of a judged promise and the grade of its locked node, as the scorecard read
// model holds it, the empty grade spelled PROVISIONAL (search.SegmentGrade).
type CalibrationKey struct {
	Tier      shared.Tier
	Basis     shared.Basis
	NodeGrade search.Grade
}

// CalibrationObservation is one judged promise of a segment: how many minutes
// late it was delivered, 0 for a promise kept, stamped with the instant it was
// judged (ADR 0020).
type CalibrationObservation struct {
	LateByMinutes int       `json:"lateBy"`
	ObservedAt    time.Time `json:"observedAt"`
}

// AppendCalibrationObservation appends one observation to a segment's ring: it
// prunes the observations older than windowDays around now and keeps the
// latest MaxCalibrationObservations. The ring is stored newest-last.
func AppendCalibrationObservation(ring []CalibrationObservation, obs CalibrationObservation, windowDays int, now time.Time) []CalibrationObservation {
	ring = append(slices.Clone(ring), obs)
	ring = PruneCalibrationWindow(ring, windowDays, now)
	if over := len(ring) - MaxCalibrationObservations; over > 0 {
		ring = ring[over:]
	}
	return ring
}

// PruneCalibrationWindow drops the observations observed before now minus
// windowDays days.
func PruneCalibrationWindow(ring []CalibrationObservation, windowDays int, now time.Time) []CalibrationObservation {
	cutoff := now.AddDate(0, 0, -windowDays)
	return slices.DeleteFunc(ring, func(o CalibrationObservation) bool { return o.ObservedAt.Before(cutoff) })
}

// CalibrationLateBy returns the lateBy minutes of the in-window observations
// of a ring, oldest first; the ring itself is not changed.
func CalibrationLateBy(ring []CalibrationObservation, windowDays int, now time.Time) []int {
	in := PruneCalibrationWindow(slices.Clone(ring), windowDays, now)
	out := make([]int, len(in))
	for i, o := range in {
		out[i] = o.LateByMinutes
	}
	return out
}
