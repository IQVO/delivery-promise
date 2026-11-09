package usecases

import (
	"context"
	"errors"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// noCalibrationMetrics is the CalibrationMetrics of a Deps whose Metrics does
// not implement it.
type noCalibrationMetrics struct{}

func (noCalibrationMetrics) CalibrationBufferApplied(context.Context, string, int) {}
func (noCalibrationMetrics) CalibrationSegmentBelowMinimum(context.Context, string, string, string) {
}

// calibrationMetrics returns the Deps' Metrics as CalibrationMetrics when it is
// one, and a recorder of nothing otherwise.
func (s *Service) calibrationMetrics() ports.CalibrationMetrics {
	if m, ok := s.Metrics.(ports.CalibrationMetrics); ok {
		return m
	}
	return noCalibrationMetrics{}
}

// calibrationBufferOf adapts the calibration read model to the search's
// closure (ADR 0020). It loads every segment ONCE, answers each from the
// current policy (the target quantile, the minimum, the cap) and counts the
// segments that have observations but fewer than the minimum. The closure is nil
// when no segment has reached the minimum (the search then computes its paths
// once for all tiers, as before), and when the read model cannot be read: a
// quote never fails for want of a buffer.
func (s *Service) calibrationBufferOf(ctx context.Context, pol *policy.Policy, now time.Time) func(shared.Tier, shared.Basis, search.Grade) search.Calibration {
	if s.Calibration == nil {
		return nil
	}
	segments, err := s.Calibration.Segments(ctx, pol.CalibrationWindowDays(), now)
	if err != nil {
		s.Logger.WarnContext(ctx, "calibration read model unreadable: quoting without a buffer", "error", err)
		return nil
	}
	answers := make(map[readmodel.CalibrationKey]search.Calibration, len(segments))
	metrics := s.calibrationMetrics()
	for key, lateBy := range segments {
		c := search.Calibrate(lateBy, pol)
		if !c.Confidence.Known() {
			metrics.CalibrationSegmentBelowMinimum(ctx, string(key.Tier), string(key.Basis), string(key.NodeGrade))
			continue
		}
		answers[key] = c
	}
	if len(answers) == 0 {
		return nil
	}
	return func(t shared.Tier, b shared.Basis, g search.Grade) search.Calibration {
		return answers[readmodel.CalibrationKey{Tier: t, Basis: b, NodeGrade: g}]
	}
}

// recordCalibration appends the judgement of p to its segment, inside the
// caller's unit of work. A promise kept is an observation of 0 minutes late; a
// promise delivered late, of its lateByMinutes. A package LOST or RETURNED says
// nothing about how late deliveries run (and no buffer makes a lost package
// arrive), so it is not recorded.
func (s *Service) recordCalibration(ctx context.Context, p *promise.Promise) error {
	j := p.Judgement()
	if s.Calibration == nil || j == nil {
		return nil
	}
	lateBy := 0
	switch {
	case j.Outcome == promise.OutcomeKept:
	case j.Outcome == promise.OutcomeBroken && j.Cause == promise.CauseLate:
		lateBy = j.LateByMinutes
	default:
		return nil
	}
	grade := ""
	sc, err := s.Network.GetScorecard(ctx, p.Witness().NodeID)
	switch {
	case err == nil:
		grade = sc.Grade
	case !errors.Is(err, ports.ErrNotFound):
		return err
	}
	t := p.Terms()
	key := readmodel.CalibrationKey{Tier: t.Tier, Basis: t.Basis, NodeGrade: search.SegmentGrade(search.Grade(grade))}
	return s.Calibration.RecordJudgement(ctx, key, readmodel.CalibrationObservation{LateByMinutes: lateBy, ObservedAt: j.JudgedAt})
}

// recordBuffers records the buffer folded into the best candidate of every
// (group, tier) a quote searched.
func (s *Service) recordBuffers(ctx context.Context, res search.Result) {
	metrics := s.calibrationMetrics()
	for _, g := range res.Groups {
		for t, cs := range g.Candidates {
			if len(cs) > 0 {
				metrics.CalibrationBufferApplied(ctx, string(t), cs[0].BufferMinutes)
			}
		}
	}
}
