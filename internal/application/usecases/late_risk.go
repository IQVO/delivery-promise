package usecases

import (
	"context"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// FlagPredictedLate is the late-risk sweep pass (ADR 0021). It pages through
// the CONFIRMED promises, limit per page, and flags AT_RISK with the reason
// PREDICTED_LATE every promise whose witness path has a departure past its
// expected arrival (the timetable plus the F2 measured-transit adjustment) and
// a grace of search.LateRiskGrace, with no DepartureArrival row. It returns how
// many promises it flagged. A failure on one promise is logged and the pass
// goes on; the error returned is the one that stopped the paging.
func (s *Service) FlagPredictedLate(ctx context.Context, limit int) (int, error) {
	if s.DepartureArrivals == nil || limit < 1 {
		return 0, nil
	}
	v, err := s.newPathView(ctx)
	if err != nil {
		return 0, err
	}
	flagged := 0
	var after promise.ID
	for ctx.Err() == nil {
		ids, err := s.Promises.ListConfirmed(ctx, after, limit)
		if err != nil {
			return flagged, err
		}
		for _, id := range ids {
			ok, err := s.flagIfPredictedLate(ctx, v, id)
			if err != nil {
				s.Logger.WarnContext(ctx, "late-risk check failed; the next sweep retries", "promiseId", id, "error", err)
			}
			if ok {
				flagged++
			}
		}
		if len(ids) < limit {
			break
		}
		after = ids[len(ids)-1]
	}
	return flagged, ctx.Err()
}

// flagIfPredictedLate flags one CONFIRMED promise whose path is late. The flag
// is its own unit of work and re-reads the promise, so a promise that left
// CONFIRMED meanwhile (another risk event) is not flagged twice.
func (s *Service) flagIfPredictedLate(ctx context.Context, v *pathView, id promise.ID) (bool, error) {
	p, err := s.Promises.Get(ctx, id)
	if notFound(err) || err == nil && p.State() != promise.StateConfirmed {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	risk, err := v.assess(ctx, p.Witness())
	if err != nil || !risk.Late {
		return false, err
	}
	flagged := false
	err = s.tx(ctx, func(ctx context.Context) error {
		flagged = false
		cur, err := s.Promises.Get(ctx, id)
		if err != nil || cur.State() != promise.StateConfirmed {
			return ignoreNotFound(err)
		}
		if _, err := s.flagAtRisk(ctx, []promise.ID{id}, ReasonPredictedLate); err != nil {
			return err
		}
		flagged = true
		return nil
	})
	if flagged {
		s.LateRisk.PredictedLateFlagged(ctx)
		s.Logger.InfoContext(ctx, "promise flagged predicted late", "promiseId", id, "departureId", risk.LateDepartureID,
			"expectedArrival", risk.LateExpected.UTC().Format(time.RFC3339), "projectedStationArrival", risk.Projected.UTC().Format(time.RFC3339))
	}
	return flagged, err
}

func ignoreNotFound(err error) error {
	if notFound(err) {
		return nil
	}
	return err
}

// holdsPredictedLate says RepriceAtRisk must leave an AT_RISK promise alone: it
// was flagged PREDICTED_LATE and its current window is still reachable
// (ADR 0021). Any other reason, a promise that is no longer AT_RISK and a
// window that is not reachable go on to the normal re-promise.
func (s *Service) holdsPredictedLate(ctx context.Context, view func() (*pathView, error), id promise.ID) bool {
	p, err := s.Promises.Get(ctx, id)
	if err != nil || p.State() != promise.StateAtRisk || p.AtRiskReason() != ReasonPredictedLate {
		return false
	}
	v, err := view()
	if err != nil {
		s.Logger.WarnContext(ctx, "reachability check failed; the promise is re-promised", "promiseId", id, "error", err)
		return false
	}
	reachable, err := v.reachable(ctx, p)
	if err != nil {
		s.Logger.WarnContext(ctx, "reachability check failed; the promise is re-promised", "promiseId", id, "error", err)
		return false
	}
	return reachable
}

// lazyPathView builds the pathView on first use, so a pass that meets no
// PREDICTED_LATE promise reads no policy.
func (s *Service) lazyPathView(ctx context.Context) func() (*pathView, error) {
	var v *pathView
	var err error
	return func() (*pathView, error) {
		if v == nil && err == nil {
			v, err = s.newPathView(ctx)
		}
		return v, err
	}
}

// pathView reads the local read models a late-risk verdict needs, memoising
// each lookup for the length of one sweep pass.
type pathView struct {
	s        *Service
	now      time.Time
	measured search.MeasuredTransitFunc
	deps     map[string]*search.Departure
	arrived  map[string]bool
	dwells   map[string]time.Duration
}

// newPathView reads the current policy for the measured-transit service level
// and fixes now for the whole pass.
func (s *Service) newPathView(ctx context.Context) (*pathView, error) {
	pol, err := s.currentPolicy(ctx)
	if err != nil {
		return nil, err
	}
	now := s.Clock.Now().UTC()
	return &pathView{
		s: s, now: now, measured: memoTransit(s.measuredTransitOf(ctx, pol, now)),
		deps: map[string]*search.Departure{}, arrived: map[string]bool{}, dwells: map[string]time.Duration{},
	}, nil
}

// departure returns the scheduled, not cancelled departure, or nil.
func (v *pathView) departure(ctx context.Context, id string) (*search.Departure, error) {
	if d, seen := v.deps[id]; seen {
		return d, nil
	}
	d, err := v.s.Network.GetDeparture(ctx, id)
	if err != nil && !notFound(err) {
		return nil, err
	}
	var out *search.Departure
	if err == nil && d.Scheduled && !d.Cancelled {
		out = &d
	}
	v.deps[id] = out
	return out, nil
}

func (v *pathView) hasArrived(ctx context.Context, id string) (bool, error) {
	if got, seen := v.arrived[id]; seen {
		return got, nil
	}
	got := false
	if v.s.DepartureArrivals != nil {
		var err error
		if got, err = v.s.DepartureArrivals.DepartureArrived(ctx, id); err != nil {
			return false, err
		}
	}
	v.arrived[id] = got
	return got, nil
}

// dwell is a node's sort dwell; a node without a profile has none.
func (v *pathView) dwell(ctx context.Context, nodeID string) time.Duration {
	if d, seen := v.dwells[nodeID]; seen {
		return d
	}
	var d time.Duration
	if n, err := v.s.Network.GetNode(ctx, nodeID); err == nil && n.Profile != nil {
		d = time.Duration(n.Profile.SortDwellMinutes) * time.Minute
	}
	v.dwells[nodeID] = d
	return d
}

// legs lists the witness path's usable departures (the pickup, then the legs
// in order) with whether their arrival was seen. A NOMINAL witness has none.
func (v *pathView) legs(ctx context.Context, w shared.Witness) ([]search.PathLeg, error) {
	var out []search.PathLeg
	for _, id := range append([]string{w.PickupDepartureID}, w.Legs...) {
		if id == "" {
			continue
		}
		d, err := v.departure(ctx, id)
		if err != nil {
			return nil, err
		}
		if d == nil {
			continue
		}
		arrived, err := v.hasArrived(ctx, id)
		if err != nil {
			return nil, err
		}
		out = append(out, search.PathLeg{Departure: *d, Arrived: arrived})
	}
	return out, nil
}

// assess judges the witness path at the pass's now.
func (v *pathView) assess(ctx context.Context, w shared.Witness) (search.PathRisk, error) {
	legs, err := v.legs(ctx, w)
	if err != nil {
		return search.PathRisk{}, err
	}
	return search.AssessPath(legs, v.measured, func(nodeID string) time.Duration { return v.dwell(ctx, nodeID) }, v.now), nil
}

// reachable says the promise's window can still be met although its path is
// late. A wave that is unknown makes it not reachable.
func (v *pathView) reachable(ctx context.Context, p *promise.Promise) (bool, error) {
	w := p.Witness()
	risk, err := v.assess(ctx, w)
	if err != nil || !risk.Late {
		return err == nil, err
	}
	wave, err := v.s.Network.GetWave(ctx, w.DeliveryWaveID)
	if err != nil && !notFound(err) {
		return false, err
	}
	return risk.Reachable(wave, p.Terms().Window.Latest), nil
}
