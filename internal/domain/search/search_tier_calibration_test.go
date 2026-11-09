package search

import (
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// An ineligible node is filtered (ADR 0022) AND the calibration buffer applies
// (ADR 0020) in the same request: the survivor carries the buffer and the
// slower tiers keep both centres, each with its buffer.
func TestAnIneligibleNodeIsFilteredWhileTheCalibrationBufferApplies(t *testing.T) {
	in := buffered(secondCentre(fixture()), Calibration{BufferMinutes: 30, Confidence: shared.NewConfidence(0.96)})
	in.TierIneligible = ineligibleFor(nil, [2]string{"N-AUS3", "NEXT_DAY"})
	g := onlyGroup(t, Run(in))

	next := g.Candidates[shared.TierNextDay]
	if len(next) != 1 || next[0].NodeID != "N-AUS4" {
		t.Fatalf("only N-AUS4 may remain for NEXT_DAY: %+v", next)
	}
	if next[0].BufferMinutes != 30 || next[0].Confidence.Value() != 0.96 {
		t.Fatalf("the survivor must carry the buffer: %d/%v", next[0].BufferMinutes, next[0].Confidence)
	}
	std := g.Candidates[shared.TierStandard]
	if len(std) != 2 {
		t.Fatalf("STANDARD keeps both centres: %+v", std)
	}
	for _, c := range std {
		if c.BufferMinutes != 30 {
			t.Fatalf("%s lost its buffer on STANDARD: %d", c.NodeID, c.BufferMinutes)
		}
	}
}
