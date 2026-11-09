package shared

import (
	"bytes"
	"encoding/json"
	"math"
)

// confidenceScale is the resolution of a Confidence: three decimals.
const confidenceScale = 1000

// Confidence is the calibrated probability that a quote is kept on time: the
// observed share of the quote's segment whose lateness was within the buffer
// the quote carries (ADR 0020). It is a number in [0,1] rounded to three
// decimals, or UNKNOWN: a segment with too few observations has no confidence,
// which is not the same as a confidence of zero. The zero value is unknown.
type Confidence struct {
	thousandths int
	known       bool
}

// NewConfidence returns the known confidence of a share in [0,1], rounded to
// three decimals; a share outside [0,1] (or NaN) is clamped into it.
func NewConfidence(share float64) Confidence {
	switch {
	case math.IsNaN(share) || share < 0:
		share = 0
	case share > 1:
		share = 1
	}
	return Confidence{thousandths: int(math.Round(share * confidenceScale)), known: true}
}

// Known reports whether the confidence is known.
func (c Confidence) Known() bool { return c.known }

// Value returns the confidence in [0,1]; 0 when unknown (check Known first).
func (c Confidence) Value() float64 { return float64(c.thousandths) / confidenceScale }

// Ptr returns the optional wire form: nil when unknown.
func (c Confidence) Ptr() *float64 {
	if !c.known {
		return nil
	}
	v := c.Value()
	return &v
}

// ConfidenceOf reads the optional wire form: nil is unknown.
func ConfidenceOf(p *float64) Confidence {
	if p == nil {
		return Confidence{}
	}
	return NewConfidence(*p)
}

// MarshalJSON writes the number, or null when unknown.
func (c Confidence) MarshalJSON() ([]byte, error) {
	if !c.known {
		return []byte("null"), nil
	}
	return json.Marshal(c.Value())
}

// UnmarshalJSON reads a number or null (unknown); it is tolerant of a missing
// field, which leaves the zero value, unknown.
func (c *Confidence) UnmarshalJSON(b []byte) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		*c = Confidence{}
		return nil
	}
	var f float64
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	*c = NewConfidence(f)
	return nil
}
