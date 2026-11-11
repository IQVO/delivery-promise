package search

import (
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// bookFor returns the book id of a node pickup and, when the book does not
// exist yet, the opening to create; ok is false when strict and the book cannot
// take a hold.
func (s *searcher) bookFor(n Node, pickupID string, cutoff time.Time, strict bool) (book.ID, book.Opening, bool) {
	id := book.MakeID(n.ID, pickupID)
	if bv, exists := s.in.Books[string(id)]; exists {
		if strict && (!bv.Open || bv.Room < 1 || !s.in.Now.Before(bv.HoldDeadlineAt)) {
			return id, book.Opening{}, false
		}
		return id, book.Opening{}, true
	}
	op, ok := OpeningFor(n, pickupID, cutoff, s.in.Policy)
	if !ok || strict && !s.in.Now.Before(op.HoldDeadlineAt) {
		return id, op, false
	}
	return id, op, true
}

// expiry returns the offer expiry for a pickup cutoff and whether the
// candidate can still be met: now < cutoff minus the effective handling.
func (s *searcher) expiry(p *Profile, cutoff time.Time, handling int) (expires, meet time.Time, ok bool) {
	meet, found := p.Schedule.SubBusinessMinutes(cutoff, handling)
	if !found || !meet.After(s.in.Now) {
		return time.Time{}, time.Time{}, false
	}
	expires = s.in.Now.Add(s.in.Policy.OfferTTL())
	if meet.Before(expires) {
		expires = meet
	}
	return expires, meet, true
}

// waveFn picks the delivery wave for a final arrival: the earliest wave of the
// station for most tiers, the earliest one on the chosen day for CONSOLIDATED.
type waveFn func(at time.Time) (Wave, bool)

// scheduled builds the SCHEDULED candidates of one node, each ending on the
// wave that wave picks.
func (s *searcher) scheduled(n Node, strict bool, t shared.Tier, wave waveFn) []Candidate {
	p := n.Profile
	handling, skip := Handicap(s.in.Grades[n.ID], p.HandlingMinutes)
	ready, ok := p.Schedule.AddBusinessMinutes(s.in.Now, handling)
	if !ok {
		return nil
	}
	var pickups []Departure
	for _, d := range s.byOrigin[n.ID] {
		if d.Kind != KindPickup || d.CutoffAt.Before(ready) || !s.usable(d, strict) {
			continue
		}
		if _, _, okBook := s.bookFor(n, d.ID, d.CutoffAt, strict); okBook {
			pickups = append(pickups, d)
		}
	}
	if skip >= len(pickups) {
		return nil
	}
	pickups = pickups[skip:]
	if len(pickups) > MaxPickups {
		pickups = pickups[:MaxPickups]
	}
	var out []Candidate
	for _, d := range pickups {
		if c, okC := s.candidateFrom(n, d, handling, strict, t, wave); okC {
			out = append(out, c)
		}
	}
	return out
}

func (s *searcher) candidateFrom(n Node, d Departure, handling int, strict bool, t shared.Tier, wave waveFn) (Candidate, bool) {
	expires, meet, ok := s.expiry(n.Profile, d.CutoffAt, handling)
	if !ok {
		return Candidate{}, false
	}
	arr, found := s.earliestArrival(d, strict)
	if !found {
		return Candidate{}, false
	}
	basis := shared.BasisScheduled
	if arr.measured { // at least one leg ran later than the timetable (ADR 0014)
		basis = shared.BasisMeasured
	}
	// The segment's buffer is added to the final arrival BEFORE the wave snap, so
	// the window keeps its width and the offer lands in a later one (ADR 0020).
	cal := s.calibrate(t, basis, n.ID)
	w, found := wave(arr.at.Add(time.Duration(cal.BufferMinutes) * time.Minute))
	if !found || !d.CutoffAt.Before(w.WindowStart) {
		return Candidate{}, false
	}
	id, op, _ := s.bookFor(n, d.ID, d.CutoffAt, false)
	return Candidate{
		NodeID: n.ID, ShipBy: d.CutoffAt, Window: shared.Window{Earliest: w.WindowStart, Latest: w.WindowEnd, TimeZone: s.timeZone()}, Basis: basis,
		BufferMinutes: cal.BufferMinutes, Confidence: cal.Confidence,
		Witness: shared.Witness{NodeID: n.ID, PickupDepartureID: d.ID, Legs: arr.legs, DeliveryWaveID: w.ID}, BookID: string(id),
		ExpiresAt: expires, LatestMeetableAt: meet, Grade: s.in.Grades[n.ID], InRegion: n.RegionID == s.in.AreaRegionID,
		LegCount: len(arr.legs), Direct: d.Dest == s.in.Station.ID && (n.Subtype == SubtypeSubSameDay || n.Subtype == SubtypeMicro), Opening: op,
		CapacityBand: s.bandOf(id, op),
	}, true
}

// bandOf reads the capacity band of the book a candidate charges: the existing
// book's share and holds, or, for a book still to be opened, its opening share
// with nothing used. Empty when the book has no finite capacity (ADR 0023).
func (s *searcher) bandOf(id book.ID, op book.Opening) shared.CapacityBand {
	used, share := 0, op.Share
	if bv, exists := s.in.Books[string(id)]; exists {
		used, share = bv.Used, bv.Share
	}
	band, _ := shared.CapacityBandFor(used, share)
	return band
}
