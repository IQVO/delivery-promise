package memory

import (
	"context"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
)

// PromiseCalibrationView is the in-memory ports.PromiseCalibrationView (ADR
// 0020). It keeps the persistence contract of the Postgres adapter: the same
// ring cap, the same window pruning, and the same atomicity with the unit of
// work that records the judgement.
type PromiseCalibrationView struct{ s *Store }

// RecordJudgement appends one observation to the segment's ring.
func (c *PromiseCalibrationView) RecordJudgement(ctx context.Context, key readmodel.CalibrationKey, obs readmodel.CalibrationObservation) error {
	defer c.s.enter(ctx)()
	obs.ObservedAt = obs.ObservedAt.UTC()
	c.s.st.calibration[key] = readmodel.AppendCalibrationObservation(c.s.st.calibration[key], obs, policy.MaxCalibrationWindowDays, obs.ObservedAt)
	return nil
}

// Segments returns the in-window lateBy minutes of every segment that has any.
func (c *PromiseCalibrationView) Segments(ctx context.Context, windowDays int, now time.Time) (map[readmodel.CalibrationKey][]int, error) {
	defer c.s.enter(ctx)()
	out := map[readmodel.CalibrationKey][]int{}
	for k, ring := range c.s.st.calibration {
		if lateBy := readmodel.CalibrationLateBy(ring, windowDays, now); len(lateBy) > 0 {
			out[k] = lateBy
		}
	}
	return out, nil
}
