package memory

import (
	"context"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
)

// TransitActuals is the in-memory ports.TransitActuals (ADR 0014). It keeps
// the persistence contract of the Postgres adapter: the same ring cap, the
// same window pruning and the same open-row lifecycle.
type TransitActuals struct{ s *Store }

// RecordLoadDeparted opens the pairing row of a load and prunes the open rows
// older than the longest transit window.
func (t *TransitActuals) RecordLoadDeparted(ctx context.Context, loadID, originNodeID string, departedAt time.Time) error {
	defer t.s.enter(ctx)()
	t.s.st.transitOpen[loadID] = transitOpenRow{Origin: originNodeID, DepartedAt: departedAt}
	cutoff := departedAt.AddDate(0, 0, -policy.MaxTransitWindowDays)
	for id, row := range t.s.st.transitOpen {
		if row.DepartedAt.Before(cutoff) {
			delete(t.s.st.transitOpen, id)
		}
	}
	return nil
}

// RecordLoadArrived pairs the load's open row into one observation of its
// (origin, dest) pair and deletes the open row; a load with no open row
// changes nothing.
func (t *TransitActuals) RecordLoadArrived(ctx context.Context, loadID, destNodeID string, arrivedAt time.Time) error {
	defer t.s.enter(ctx)()
	open, ok := t.s.st.transitOpen[loadID]
	if !ok {
		return nil
	}
	delete(t.s.st.transitOpen, loadID)
	k := transitPair{origin: open.Origin, dest: destNodeID}
	ring := append([]readmodel.TransitObservation(nil), t.s.st.transitRings[k]...)
	ring = readmodel.AppendTransitObservation(ring,
		readmodel.TransitObservation{Minutes: int(arrivedAt.Sub(open.DepartedAt).Minutes()), ObservedAt: arrivedAt.UTC()},
		policy.MaxTransitWindowDays, arrivedAt)
	t.s.st.transitRings[k] = ring
	return nil
}

// MeasuredTransit answers the nearest-rank percentile transit of the pair's
// in-window observations; ok is false below minObservations.
func (t *TransitActuals) MeasuredTransit(ctx context.Context, origin, dest string, percentile, minObservations, windowDays int, now time.Time) (bool, int) {
	defer t.s.enter(ctx)()
	ring := t.s.st.transitRings[transitPair{origin: origin, dest: dest}]
	return readmodel.TransitPercentile(ring, percentile, minObservations, windowDays, now)
}
