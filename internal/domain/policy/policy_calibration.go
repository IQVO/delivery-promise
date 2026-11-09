package policy

import "github.com/claudioed/delivery-promise/internal/domain/shared"

// Calibration bounds and defaults (ADR 0020).
const (
	MinTargetOnTimePercent     = 50
	MaxTargetOnTimePercent     = 99
	DefaultTargetOnTimePercent = 95

	MinCalibrationObservationsFloor   = 10
	MaxCalibrationObservationsCeiling = 10000
	DefaultMinCalibrationObservations = 50

	MinCalibrationWindowDays     = 1
	MaxCalibrationWindowDays     = 365
	DefaultCalibrationWindowDays = 28

	MinCalibrationBufferMinutes     = 0
	MaxCalibrationBufferMinutesCeil = 2880
	DefaultMaxCalibrationBuffer     = 720
)

// calibration holds the four calibration parameters of a policy version. The
// target is ONE global value, not one per tier (ADR 0020).
type calibration struct {
	targetPercent int
	minObs        int
	windowDays    int
	maxBuffer     int
}

// calibrationOf builds the parameters of a policy from the optional input
// values: an omitted (0) target, minimum or window takes its default, and an
// omitted (nil) maximum buffer takes its default, because 0 is a valid buffer
// cap (it switches the buffer off).
func calibrationOf(target, minObs, windowDays int, maxBuffer *int) calibration {
	c := calibration{targetPercent: target, minObs: minObs, windowDays: windowDays, maxBuffer: DefaultMaxCalibrationBuffer}
	if c.targetPercent == 0 {
		c.targetPercent = DefaultTargetOnTimePercent
	}
	if c.minObs == 0 {
		c.minObs = DefaultMinCalibrationObservations
	}
	if c.windowDays == 0 {
		c.windowDays = DefaultCalibrationWindowDays
	}
	if maxBuffer != nil {
		c.maxBuffer = *maxBuffer
	}
	return c
}

// calibrationFrom reads the calibration parameters of an input.
func calibrationFrom(in Input) calibration {
	return calibrationOf(in.TargetOnTimeProbabilityPercent, in.MinCalibrationObservations, in.CalibrationWindowDays, in.MaxCalibrationBufferMinutes)
}

// calibrationFromSnapshot reads the calibration parameters of a snapshot.
func calibrationFromSnapshot(s Snapshot) calibration {
	return calibrationOf(s.TargetOnTimeProbabilityPercent, s.MinCalibrationObservations, s.CalibrationWindowDays, s.MaxCalibrationBufferMinutes)
}

// validate checks every parameter against its range.
func (c calibration) validate() error {
	if c.targetPercent < MinTargetOnTimePercent || c.targetPercent > MaxTargetOnTimePercent {
		return shared.Invalid("targetOnTimeProbabilityPercent must be between %d and %d", MinTargetOnTimePercent, MaxTargetOnTimePercent)
	}
	if c.minObs < MinCalibrationObservationsFloor || c.minObs > MaxCalibrationObservationsCeiling {
		return shared.Invalid("minCalibrationObservations must be between %d and %d", MinCalibrationObservationsFloor, MaxCalibrationObservationsCeiling)
	}
	if c.windowDays < MinCalibrationWindowDays || c.windowDays > MaxCalibrationWindowDays {
		return shared.Invalid("calibrationWindowDays must be between %d and %d", MinCalibrationWindowDays, MaxCalibrationWindowDays)
	}
	if c.maxBuffer < MinCalibrationBufferMinutes || c.maxBuffer > MaxCalibrationBufferMinutesCeil {
		return shared.Invalid("maxCalibrationBufferMinutes must be between %d and %d", MinCalibrationBufferMinutes, MaxCalibrationBufferMinutesCeil)
	}
	return nil
}

// TargetOnTimeProbabilityPercent returns the share of promises of a segment
// the buffer aims to deliver on time (ADR 0020).
func (p *Policy) TargetOnTimeProbabilityPercent() int { return p.cal.targetPercent }

// MinCalibrationObservations returns how many in-window observations a
// segment needs before its buffer applies (ADR 0020).
func (p *Policy) MinCalibrationObservations() int { return p.cal.minObs }

// CalibrationWindowDays returns how many days of judgements a segment keeps
// (ADR 0020).
func (p *Policy) CalibrationWindowDays() int { return p.cal.windowDays }

// MaxCalibrationBufferMinutes returns the cap of a segment's buffer (ADR 0020).
func (p *Policy) MaxCalibrationBufferMinutes() int { return p.cal.maxBuffer }
