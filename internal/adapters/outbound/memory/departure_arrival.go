package memory

import (
	"context"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/policy"
)

// DepartureArrivals is the in-memory ports.DepartureArrivals (ADR 0021). It
// keeps the persistence contract of the Postgres adapter: the first arrival
// seen is kept and rows past the transit window are pruned on write.
type DepartureArrivals struct{ s *Store }

// RecordDepartureArrived stores the arrival unless one is already stored and
// prunes the rows older than the longest transit window before arrivedAt.
func (d *DepartureArrivals) RecordDepartureArrived(ctx context.Context, departureID string, arrivedAt time.Time) error {
	defer d.s.enter(ctx)()
	if _, seen := d.s.st.arrivals[departureID]; !seen {
		d.s.st.arrivals[departureID] = arrivedAt.UTC()
	}
	cutoff := arrivedAt.AddDate(0, 0, -policy.MaxTransitWindowDays)
	for id, at := range d.s.st.arrivals {
		if at.Before(cutoff) {
			delete(d.s.st.arrivals, id)
		}
	}
	return nil
}

// DepartureArrived reports whether an arrival row exists for the departure.
func (d *DepartureArrivals) DepartureArrived(ctx context.Context, departureID string) (bool, error) {
	defer d.s.enter(ctx)()
	_, ok := d.s.st.arrivals[departureID]
	return ok, nil
}
