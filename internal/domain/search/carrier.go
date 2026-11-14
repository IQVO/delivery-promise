package search

import (
	"cmp"
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// MaxCarrierOptions bounds the carrier options a CARRIER_HANDOFF area may
// declare (ADR 0026); node-topology enforces the same 1..4 shape.
const MaxCarrierOptions = 4

// CarrierOption is one declared carrier option of a CARRIER_HANDOFF service
// area: the carrier, the node it collects at, its declared cost per package
// and its declared transit in minutes (ADR 0026).
type CarrierOption struct {
	CarrierID           string
	HandoffNodeID       string
	CostPerPackageCents int64
	TransitMinutes      int
}

// CarrierArea is the carrier-handoff declaration of the destination service
// area: 1..4 options; the search promises on the option ChooseCarrier picks.
type CarrierArea struct {
	Options []CarrierOption
}

// ChooseCarrier is the deterministic per-package carrier rule of a
// CARRIER_HANDOFF area (ADR 0026): lowest costPerPackageCents, tie: the option
// with the LATER transit window, tie: the lexicographically smallest
// carrierId. shipping-and-tracking mirrors this rule verbatim at handoff, so
// the promise and the physical handoff name the same carrier whenever both
// read the same area assignment version. ok is false for an empty list.
func ChooseCarrier(options []CarrierOption) (CarrierOption, bool) {
	if len(options) == 0 || len(options) > MaxCarrierOptions {
		return CarrierOption{}, false
	}
	ordered := slices.Clone(options)
	slices.SortFunc(ordered, func(a, b CarrierOption) int {
		if c := cmp.Compare(a.CostPerPackageCents, b.CostPerPackageCents); c != 0 {
			return c
		}
		if c := cmp.Compare(b.TransitMinutes, a.TransitMinutes); c != 0 { // later transit wins the tie
			return c
		}
		return cmp.Compare(a.CarrierID, b.CarrierID)
	})
	return ordered[0], true
}

// carrierTransit is the effective final-leg transit of the chosen option: the
// declared transit, pushed later by the measured carrier transit of the
// handoff node when that exceeds it - the same one-sided rule as the linehaul
// legs (ADR 0014); measured says it was pushed. A nil closure never measures.
func (s *searcher) carrierTransit(opt CarrierOption) (time.Duration, bool) {
	effective := time.Duration(opt.TransitMinutes) * time.Minute
	if s.in.MeasuredCarrierTransit == nil {
		return effective, false
	}
	if ok, minutes := s.in.MeasuredCarrierTransit(opt.HandoffNodeID); ok {
		if measured := time.Duration(minutes) * time.Minute; measured > effective {
			return measured, true
		}
	}
	return effective, false
}

// carrierCandidate finishes a CARRIER_HANDOFF candidate (ADR 0026): the
// arrival search ended at the chosen option's handoff node, the final leg is
// the carrier's effective transit, and the window is that arrival plus the
// transit, wide CarrierWindowHours - no delivery wave exists to snap to. The
// witness names the carrier and its handoff node instead of a wave.
func (s *searcher) carrierCandidate(n Node, d Departure, arr arrival, handling int, t shared.Tier) (Candidate, bool) {
	opt, ok := ChooseCarrier(s.in.Carrier.Options)
	if !ok {
		return Candidate{}, false
	}
	expires, meet, okExp := s.expiry(n.Profile, d.CutoffAt, handling)
	if !okExp {
		return Candidate{}, false
	}
	transit, transitMeasured := s.carrierTransit(opt)
	basis := shared.BasisScheduled
	if arr.measured || transitMeasured { // a leg or the carrier ran later than declared (ADR 0014)
		basis = shared.BasisMeasured
	}
	// The segment's buffer is added to the arrival before the window opens, so
	// the window keeps its width (ADR 0020).
	cal := s.calibrate(t, basis, n.ID)
	earliest := arr.at.Add(time.Duration(cal.BufferMinutes)*time.Minute + transit)
	id, op, _ := s.bookFor(n, d.ID, d.CutoffAt, false)
	return Candidate{
		NodeID: n.ID, ShipBy: d.CutoffAt, Window: shared.Window{Earliest: earliest, Latest: earliest.Add(s.in.Policy.CarrierWindowHours()), TimeZone: s.timeZone()}, Basis: basis,
		BufferMinutes: cal.BufferMinutes, Confidence: cal.Confidence,
		Witness: shared.Witness{NodeID: n.ID, PickupDepartureID: d.ID, Legs: arr.legs, CarrierID: opt.CarrierID, HandoffNodeID: opt.HandoffNodeID}, BookID: string(id),
		ExpiresAt: expires, LatestMeetableAt: meet, Grade: s.in.Grades[n.ID], InRegion: n.RegionID == s.in.AreaRegionID,
		LegCount: len(arr.legs), Direct: d.Dest == s.finalTarget() && (n.Subtype == SubtypeSubSameDay || n.Subtype == SubtypeMicro), Opening: op,
		CapacityBand: s.bandOf(id, op),
	}, true
}
