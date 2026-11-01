package usecases

import (
	"context"
	"slices"

	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
)

// ---- linehaul-transportation -------------------------------------------------------------------------------

// OnDepartureScheduled projects DepartureScheduled. A row that a capacity event
// created earlier keeps its remaining packages.
func (s *Service) OnDepartureScheduled(ctx context.Context, m Meta, e DepartureScheduled) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		d, err := s.Network.GetDeparture(ctx, e.DepartureID)
		if err != nil && !notFound(err) {
			return err
		}
		remaining := e.CapacityPackages
		if err == nil {
			remaining = d.Remaining
		}
		return s.Network.PutDeparture(ctx, search.Departure{
			ID: e.DepartureID, LaneID: e.LaneID, Kind: e.Kind, Origin: e.OriginNodeID, Dest: e.DestinationNodeID, CutoffAt: e.CutoffAt.UTC(),
			DepartsAt: e.DepartsAt.UTC(), ArrivesAt: e.ArrivesAt.UTC(), Remaining: remaining, Cancelled: d.Cancelled, Scheduled: true,
		})
	})
}

// OnDepartureCapacityChanged projects DepartureCapacityChanged.
func (s *Service) OnDepartureCapacityChanged(ctx context.Context, m Meta, e DepartureCapacityChanged) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		d, err := s.Network.GetDeparture(ctx, e.DepartureID)
		if err != nil && !notFound(err) {
			return err
		}
		d.ID, d.Remaining = e.DepartureID, e.RemainingPackages
		return s.Network.PutDeparture(ctx, d)
	})
}

// OnDepartureCancelled projects DepartureCancelled and flags every CONFIRMED
// promise whose witness path uses the departure AT_RISK (RepromiseOnRisk).
func (s *Service) OnDepartureCancelled(ctx context.Context, m Meta, e DepartureCancelled) error {
	return s.consume(ctx, m, func(ctx context.Context) ([]promise.ID, error) {
		d, err := s.Network.GetDeparture(ctx, e.DepartureID)
		if err != nil && !notFound(err) {
			return nil, err
		}
		d.ID, d.Cancelled = e.DepartureID, true
		if err := s.Network.PutDeparture(ctx, d); err != nil {
			return nil, err
		}
		ids, err := s.Promises.ListActiveByPath(ctx, repository.PromiseRiskFilter{DepartureID: e.DepartureID})
		if err != nil {
			return nil, err
		}
		return s.flagAtRisk(ctx, ids, ReasonDepartureCanceled)
	})
}

// OnBookingMoved treats a rebooking as a risk to the assignment's promise and
// skips the departure it left (E13).
func (s *Service) OnBookingMoved(ctx context.Context, m Meta, e BookingMoved) error {
	return s.assignmentRisk(ctx, m, e.AssignmentID, ReasonBookingMoved, nil, []string{e.FromDepartureID})
}

// OnBookingFailed treats a failed booking as a risk to the assignment's promise
// and skips that departure (E13).
func (s *Service) OnBookingFailed(ctx context.Context, m Meta, e BookingFailed) error {
	return s.assignmentRisk(ctx, m, e.AssignmentID, ReasonBookingFailed, nil, []string{e.DepartureID})
}

// ---- last-mile-delivery ------------------------------------------------------------------------------------

// OnWaveScheduled projects DeliveryWaveScheduled.
func (s *Service) OnWaveScheduled(ctx context.Context, m Meta, e WaveScheduled) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		w, err := s.Network.GetWave(ctx, e.WaveID)
		if err != nil && !notFound(err) {
			return err
		}
		remaining := e.StopCapacity
		if err == nil {
			remaining = w.Remaining
		}
		return s.Network.PutWave(ctx, search.Wave{
			ID: e.WaveID, Station: e.StationNodeID, InductCutoffAt: e.InductCutoffAt.UTC(), WindowStart: e.WindowStart.UTC(),
			WindowEnd: e.WindowEnd.UTC(), Remaining: remaining, Cancelled: w.Cancelled, Scheduled: true,
		})
	})
}

// OnWaveCapacityChanged projects DeliveryWaveCapacityChanged.
func (s *Service) OnWaveCapacityChanged(ctx context.Context, m Meta, e WaveCapacityChanged) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		w, err := s.Network.GetWave(ctx, e.WaveID)
		if err != nil && !notFound(err) {
			return err
		}
		w.ID, w.Remaining = e.WaveID, e.RemainingStops
		return s.Network.PutWave(ctx, w)
	})
}

// OnWaveCancelled projects DeliveryWaveCancelled and flags the promises whose
// witness wave it is (E5).
func (s *Service) OnWaveCancelled(ctx context.Context, m Meta, e WaveCancelled) error {
	return s.consume(ctx, m, func(ctx context.Context) ([]promise.ID, error) {
		w, err := s.Network.GetWave(ctx, e.WaveID)
		if err != nil && !notFound(err) {
			return nil, err
		}
		w.ID, w.Cancelled = e.WaveID, true
		if err := s.Network.PutWave(ctx, w); err != nil {
			return nil, err
		}
		ids, err := s.Promises.ListActiveByPath(ctx, repository.PromiseRiskFilter{WaveID: e.WaveID})
		if err != nil {
			return nil, err
		}
		return s.flagAtRisk(ctx, ids, ReasonWaveCanceled)
	})
}

// OnInductionDeferred treats a deferred induction as a risk (E15).
func (s *Service) OnInductionDeferred(ctx context.Context, m Meta, e InductionDeferred) error {
	return s.assignmentRiskVia(ctx, m, e.AssignmentID, e.TrackingNumber, ReasonInductionDeferred, nil, nil)
}

// assignmentRisk flags the promise of an assignment AT_RISK, remembering the
// nodes and departures re-promising must skip.
func (s *Service) assignmentRisk(ctx context.Context, m Meta, assignmentID, reason string, nodes, departures []string) error {
	return s.assignmentRiskVia(ctx, m, assignmentID, "", reason, nodes, departures)
}

// assignmentRiskVia is assignmentRisk for an event that may carry only a
// tracking number (PackageInductionDeferred omits assignmentId when no label
// data arrived): the assignment is then found through FulfillmentShipped.
func (s *Service) assignmentRiskVia(ctx context.Context, m Meta, assignmentID, trackingNumber, reason string, nodes, departures []string) error {
	return s.consume(ctx, m, func(ctx context.Context) ([]promise.ID, error) {
		if assignmentID == "" && trackingNumber != "" {
			id, err := s.Shipments.AssignmentOf(ctx, trackingNumber)
			if err != nil && !notFound(err) {
				return nil, err
			}
			assignmentID = id
		}
		a, err := s.Shipments.GetAssignment(ctx, assignmentID)
		if notFound(err) {
			s.Logger.WarnContext(ctx, "risk for an assignment whose lock was not seen", "assignmentId", assignmentID, "reason", reason)
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		a.ExcludedNodes, a.ExcludedDepartures = union(a.ExcludedNodes, nodes), union(a.ExcludedDepartures, departures)
		if err := s.Shipments.PutAssignment(ctx, a); err != nil {
			return nil, err
		}
		return s.flagAtRisk(ctx, []promise.ID{promise.ID(a.PromiseID)}, reason)
	})
}

func union(a, b []string) []string {
	out := slices.Clone(a)
	for _, x := range b {
		if !slices.Contains(out, x) {
			out = append(out, x)
		}
	}
	return out
}
