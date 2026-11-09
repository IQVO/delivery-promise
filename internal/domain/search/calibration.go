package search

import (
	"slices"

	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Calibration is the answer of the calibration closure for one segment
// (tier, basis, node grade): the buffer added to a candidate's final arrival
// and the confidence the quote then carries (ADR 0020). The zero value is "no
// buffer, confidence unknown": what a segment below the minimum observations
// answers, and what the search did before calibration existed.
type Calibration struct {
	// BufferMinutes is never negative.
	BufferMinutes int
	// Confidence is known only when the segment has the minimum observations.
	Confidence shared.Confidence
}

// CalibrationBuffer is the target quantile of the observed lateness: the
// smallest b such that at least targetPercent% of the observations have
// lateBy <= b. A kept promise counts as 0, a negative value as 0, and when the
// kept share already meets the target the buffer is 0. It is one-sided (never
// negative: a buffer never makes a promise earlier) and uncapped; Calibrate
// applies the policy's minimum and cap (ADR 0020).
func CalibrationBuffer(obs []int, targetPercent int) int {
	if len(obs) == 0 {
		return 0
	}
	sorted := slices.Clone(obs)
	slices.Sort(sorted)
	n := len(sorted)
	rank := (targetPercent*n + 99) / 100 // ceil(target * n / 100) without float error
	rank = min(max(rank, 1), n)
	return max(sorted[rank-1], 0)
}

// Calibrate answers a segment from its in-window observations under a policy:
// below MinCalibrationObservations the buffer is 0 and the confidence unknown;
// otherwise the buffer is the target quantile capped at
// MaxCalibrationBufferMinutes, and the confidence is the observed share of
// observations with lateBy <= buffer (ADR 0020).
func Calibrate(obs []int, pol *policy.Policy) Calibration {
	if len(obs) < pol.MinCalibrationObservations() {
		return Calibration{}
	}
	buffer := min(CalibrationBuffer(obs, pol.TargetOnTimeProbabilityPercent()), pol.MaxCalibrationBufferMinutes())
	within := 0
	for _, o := range obs {
		if o <= buffer {
			within++
		}
	}
	return Calibration{BufferMinutes: buffer, Confidence: shared.NewConfidence(float64(within) / float64(len(obs)))}
}

// SegmentGrade is the grade part of a calibration segment key: the node's
// scorecard grade, with the empty grade (no scorecard yet) spelled PROVISIONAL,
// as the search already ranks the two alike (ADR 0020).
func SegmentGrade(g Grade) Grade {
	if g == "" {
		return GradeProvisional
	}
	return g
}

// calibrate asks the input's closure for the segment of a candidate; no
// closure, or a negative answer, means no buffer.
func (s *searcher) calibrate(t shared.Tier, basis shared.Basis, nodeID string) Calibration {
	if s.in.CalibrationBuffer == nil {
		return Calibration{}
	}
	c := s.in.CalibrationBuffer(t, basis, SegmentGrade(s.in.Grades[nodeID]))
	c.BufferMinutes = max(c.BufferMinutes, 0)
	return c
}

// pathKey identifies one set of computed paths: without a calibration closure
// every tier shares the set of tier "".
type pathKey struct {
	tier   shared.Tier
	strict bool
}

// pathSet computes the witness paths of a group's eligible nodes lazily, once
// per (tier, strict) when the buffer depends on the tier and once per strict
// otherwise.
type pathSet struct {
	s     *searcher
	nodes []Node
	byKey map[pathKey][]Candidate
}

func (p *pathSet) of(t shared.Tier, strict bool) []Candidate {
	if p.s.in.CalibrationBuffer == nil {
		t = ""
	}
	k := pathKey{tier: t, strict: strict}
	cs, ok := p.byKey[k]
	if !ok {
		cs = p.s.pathsOf(p.nodes, strict, t)
		p.byKey[k] = cs
	}
	return cs
}
