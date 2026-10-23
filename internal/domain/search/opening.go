package search

import (
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// NominalPickupID is the pickup id of the book of a NOMINAL path, which has no
// scheduled departure (ADR 0002, A1).
func NominalPickupID(cutoffAt time.Time) string {
	return "nominal-" + cutoffAt.UTC().Format("20060102T1504Z")
}

// BookIDFor returns the bookId a witness holds capacity on: nodeId plus the
// pickup departure, or the nominal pickup of cutoffAt for a NOMINAL witness.
func BookIDFor(w shared.Witness, cutoffAt time.Time) book.ID {
	if w.PickupDepartureID == "" {
		return book.MakeID(w.NodeID, NominalPickupID(cutoffAt))
	}
	return book.MakeID(w.NodeID, w.PickupDepartureID)
}

// OpeningFor computes the book to open lazily for a node pickup (gap G9): the
// share is min(slot maxOrders, throughput cap x window) x the policy share, and
// holds stop at cutoffAt minus the declared handling time in business time.
func OpeningFor(n Node, pickupID string, cutoffAt time.Time, pol *policy.Policy) (book.Opening, bool) {
	p := n.Profile
	if p == nil {
		return book.Opening{}, false
	}
	hold, ok := p.Schedule.SubBusinessMinutes(cutoffAt, p.HandlingMinutes)
	if !ok {
		hold = cutoffAt
	}
	return book.Opening{
		NodeID: n.ID, PickupDepartureID: pickupID, CutoffAt: cutoffAt, HoldDeadlineAt: hold,
		Share: shareFor(p, cutoffAt, pol),
	}, true
}

func shareFor(p *Profile, cutoffAt time.Time, pol *policy.Policy) int {
	maxOrders := 0
	for _, in := range p.Schedule.CutoffsOn(p.Cutoffs, cutoffAt) {
		if in.At.Equal(cutoffAt) {
			maxOrders = in.MaxOrders
		}
	}
	start, ok := p.Schedule.PreviousCutoff(p.Cutoffs, cutoffAt)
	if !ok {
		start = cutoffAt.Add(-24 * time.Hour)
	}
	window := p.Schedule.OperatingMinutesBetween(start, cutoffAt)
	throughput := book.ThroughputOrders(p.Cap, window, p.Schedule.DayMinutes(cutoffAt))
	return book.Share(maxOrders, throughput, pol.SharePercent())
}
