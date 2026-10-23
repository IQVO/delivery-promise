package usecases

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
)

// ScorecardWindow is the only scorecard window the promise reads (E18).
const ScorecardWindow = "30d"

// consume runs fn in a unit of work that first records the CloudEvents id in
// the inbox: a message already processed changes nothing (idempotent consumer).
// fn returns the promises that were flagged AT_RISK and still need a revision;
// they are re-promised after the commit.
func (s *Service) consume(ctx context.Context, m Meta, fn func(ctx context.Context) ([]promise.ID, error)) error {
	var pending []promise.ID
	err := s.tx(ctx, func(ctx context.Context) error {
		pending = nil
		first, err := s.Inbox.Begin(ctx, m.ID, m.Type, s.Clock.Now())
		if err != nil || !first {
			return err
		}
		pending, err = fn(ctx)
		return err
	})
	if err != nil {
		return err
	}
	s.ReviseAll(ctx, pending)
	return nil
}

// project adapts a plain projection (no risk) to consume.
func (s *Service) project(ctx context.Context, m Meta, fn func(ctx context.Context) error) error {
	return s.consume(ctx, m, func(ctx context.Context) ([]promise.ID, error) { return nil, fn(ctx) })
}

func notFound(err error) bool { return errors.Is(err, ports.ErrNotFound) }

// ---- node-topology -----------------------------------------------------------------------------------------

// OnNodeRegistered projects NodeRegistered into NodeProfileView.
func (s *Service) OnNodeRegistered(ctx context.Context, m Meta, e NodeRegistered) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		n, err := s.Network.GetNode(ctx, e.NodeID)
		if err != nil && !notFound(err) {
			return err
		}
		if notFound(err) {
			n = readmodel.Node{ID: e.NodeID, State: readmodel.NodeProposed}
		}
		n.Type, n.Subtype, n.Operator, n.RegionID, n.Timezone = e.NodeType, e.Subtype, e.Operator, e.RegionID, e.Timezone
		return s.Network.PutNode(ctx, n)
	})
}

// OnNodeProfileDeclared projects NodeOperationalProfileDeclared; a revision not
// above the stored one is ignored.
func (s *Service) OnNodeProfileDeclared(ctx context.Context, m Meta, e NodeProfileDeclared) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		n, err := s.Network.GetNode(ctx, e.NodeID)
		if err != nil && !notFound(err) {
			return err
		}
		if notFound(err) {
			n = readmodel.Node{ID: e.NodeID, State: readmodel.NodeProposed, Timezone: e.Timezone}
		}
		if n.Profile != nil && e.ProfileRevision <= n.Profile.Revision {
			return nil
		}
		hours := make(map[string]readmodel.Hours, len(e.OperatingHoursByDay))
		for day, h := range e.OperatingHoursByDay {
			hours[strings.ToLower(day)] = readmodel.Hours{Start: h.Start, End: h.End}
		}
		cutoffs := make([]readmodel.Cutoff, len(e.Cutoffs))
		for i, c := range e.Cutoffs {
			cutoffs[i] = readmodel.Cutoff{LocalTime: c.LocalTime, Days: slices.Clone(c.Days), MaxOrders: c.MaxOrders}
		}
		n.Profile = &readmodel.Profile{
			Revision: e.ProfileRevision, Timezone: e.Timezone, OperatingHours: hours, Holidays: slices.Clone(e.Holidays),
			HandlingMinutes: e.HandlingTimeMinutes, Cutoffs: cutoffs, CapValue: e.ThroughputCap.Value,
			CapPerHour: e.ThroughputCap.TimeUnit == "HOUR", SortDwellMinutes: e.SortDwellMinutes,
		}
		if e.Timezone != "" {
			n.Timezone = e.Timezone
		}
		return s.Network.PutNode(ctx, n)
	})
}

// OnNodeStateChanged projects NodeActivated, NodeSuspended and NodeRetired; state
// is one of readmodel.NodeActive, NodeSuspended or NodeRetired.
func (s *Service) OnNodeStateChanged(ctx context.Context, m Meta, e NodeStateChanged, state string) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		n, err := s.Network.GetNode(ctx, e.NodeID)
		if err != nil && !notFound(err) {
			return err
		}
		if notFound(err) {
			n = readmodel.Node{ID: e.NodeID}
		}
		n.State = state
		return s.Network.PutNode(ctx, n)
	})
}

// OnLaneOpened projects LaneOpened into LaneView.
func (s *Service) OnLaneOpened(ctx context.Context, m Meta, e LaneOpened) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		return s.Network.PutLane(ctx, search.Lane{ID: e.LaneID, Origin: e.OriginNodeID, Dest: e.DestinationNodeID, NominalMinutes: e.NominalTransitMinutes, Open: true})
	})
}

// OnLaneClosed projects LaneClosed.
func (s *Service) OnLaneClosed(ctx context.Context, m Meta, e LaneClosed) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		l, err := s.Network.GetLane(ctx, e.LaneID)
		if err != nil && !notFound(err) {
			return err
		}
		if notFound(err) {
			l = search.Lane{ID: e.LaneID}
		}
		l.Open = false
		return s.Network.PutLane(ctx, l)
	})
}

// OnServiceAreaAssigned projects ServiceAreaAssigned; an assignmentVersion not
// above the stored one is ignored.
func (s *Service) OnServiceAreaAssigned(ctx context.Context, m Meta, e ServiceAreaAssigned) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		cur, err := s.Network.GetArea(ctx, e.PostalPrefix)
		if err != nil && !notFound(err) {
			return err
		}
		if err == nil && e.AssignmentVersion <= cur.Version {
			return nil
		}
		return s.Network.PutArea(ctx, readmodel.Area{PostalPrefix: e.PostalPrefix, StationID: e.DeliveryStationNodeID, RegionID: e.RegionID, Version: e.AssignmentVersion})
	})
}

// OnRegionDefined projects RegionDefined.
func (s *Service) OnRegionDefined(ctx context.Context, m Meta, e RegionDefined) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		return s.Network.PutRegion(ctx, readmodel.Region{ID: e.RegionID, Code: e.Code, Name: e.Name})
	})
}

// ---- offer-availability ------------------------------------------------------------------------------------

// OnCatalogItemChanged projects CatalogItemCreated (discontinued false) and
// CatalogItemDiscontinued (true).
func (s *Service) OnCatalogItemChanged(ctx context.Context, m Meta, e CatalogItemChanged, discontinued bool) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		return s.Network.PutItem(ctx, readmodel.Item{ID: e.CatalogItemID, Discontinued: discontinued})
	})
}

// OnListingCreated projects ListingCreated.
func (s *Service) OnListingCreated(ctx context.Context, m Meta, e ListingCreated) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		if _, err := s.Network.GetListing(ctx, e.ListingID); err == nil {
			return nil
		} else if !notFound(err) {
			return err
		}
		return s.Network.PutListing(ctx, readmodel.Listing{ID: e.ListingID, ItemID: e.CatalogItemID, NodeID: e.NodeID})
	})
}

// OnListingAvailabilityChanged projects ListingAvailabilityChanged; an asOf
// older than the stored one is ignored.
func (s *Service) OnListingAvailabilityChanged(ctx context.Context, m Meta, e ListingAvailabilityChanged) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		l, err := s.Network.GetListing(ctx, e.ListingID)
		if err != nil && !notFound(err) {
			return err
		}
		if err == nil && e.AsOf.Before(l.AsOf) {
			return nil
		}
		l.ID, l.ItemID, l.NodeID, l.AvailableQty, l.AsOf = e.ListingID, e.CatalogItemID, e.NodeID, e.AvailableQty, e.AsOf.UTC()
		return s.Network.PutListing(ctx, l)
	})
}

// OnListingToggled projects ListingSuppressed (suppressed true) and
// ListingRestored (false).
func (s *Service) OnListingToggled(ctx context.Context, m Meta, e ListingToggled, suppressed bool) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		l, err := s.Network.GetListing(ctx, e.ListingID)
		if err != nil && !notFound(err) {
			return err
		}
		if notFound(err) {
			l = readmodel.Listing{ID: e.ListingID, NodeID: e.NodeID}
		}
		l.Suppressed = suppressed
		return s.Network.PutListing(ctx, l)
	})
}

// ---- node-performance --------------------------------------------------------------------------------------

// OnScorecardUpdated projects NodeScorecardUpdated; only the 30d window counts
// (E18).
func (s *Service) OnScorecardUpdated(ctx context.Context, m Meta, e ScorecardUpdated) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		if e.Window != ScorecardWindow {
			return nil
		}
		return s.Network.PutScorecard(ctx, readmodel.Scorecard{NodeID: e.NodeID, Grade: e.Grade, Observations: e.Observations, ComputedAt: e.ComputedAt.UTC()})
	})
}

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

// ---- order-sourcing ----------------------------------------------------------------------------------------

// OnFulfillmentLocked records assignmentId -> (promiseId, orderId, groupNo) and
// the locked node (E1), then judges in case the package facts arrived first.
func (s *Service) OnFulfillmentLocked(ctx context.Context, m Meta, e FulfillmentLocked) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		a, err := s.Shipments.GetAssignment(ctx, e.AssignmentID)
		if err != nil && !notFound(err) {
			return err
		}
		a.ID, a.PromiseID, a.OrderID, a.GroupNo, a.NodeID, a.Locked, a.LockedAt = e.AssignmentID, e.PromiseID, e.OrderID, e.GroupNo, e.NodeID, true, s.Clock.Now()
		if err := s.Shipments.PutAssignment(ctx, a); err != nil {
			return err
		}
		return s.judge(ctx, e.AssignmentID)
	})
}

// OnFulfillmentShipped records the package set of the group, then judges.
func (s *Service) OnFulfillmentShipped(ctx context.Context, m Meta, e FulfillmentShipped) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		if err := s.Shipments.PutShipment(ctx, e.AssignmentID, e.TrackingNumbers); err != nil {
			return err
		}
		return s.judge(ctx, e.AssignmentID)
	})
}

// OnFulfillmentUnsourceable remembers the excluded nodes and flags the promise
// AT_RISK (RepromiseOnRisk).
func (s *Service) OnFulfillmentUnsourceable(ctx context.Context, m Meta, e FulfillmentUnsourceable) error {
	return s.consume(ctx, m, func(ctx context.Context) ([]promise.ID, error) {
		a, err := s.Shipments.GetAssignment(ctx, e.AssignmentID)
		if err != nil && !notFound(err) {
			return nil, err
		}
		a.ID, a.PromiseID, a.OrderID, a.GroupNo = e.AssignmentID, e.PromiseID, e.OrderID, e.GroupNo
		a.ExcludedNodes = union(a.ExcludedNodes, e.ExcludedNodes)
		if err := s.Shipments.PutAssignment(ctx, a); err != nil {
			return nil, err
		}
		return s.flagAtRisk(ctx, []promise.ID{promise.ID(e.PromiseID)}, ReasonNodeUnsourceable)
	})
}

// ---- shipping-and-tracking ---------------------------------------------------------------------------------

// OnPackageDelivered records the delivery and judges the group when it was the
// last package.
func (s *Service) OnPackageDelivered(ctx context.Context, m Meta, e PackageDelivered) error {
	return s.recordOutcome(ctx, m, readmodel.PackageOutcome{TrackingNumber: e.TrackingNumber, AssignmentID: e.AssignmentID, Kind: readmodel.OutcomeDelivered, At: e.DeliveredAt.UTC()})
}

// OnPackageLost records the loss; the group is judged BROKEN (cause LOST).
func (s *Service) OnPackageLost(ctx context.Context, m Meta, e PackageLost) error {
	return s.recordOutcome(ctx, m, readmodel.PackageOutcome{TrackingNumber: e.TrackingNumber, Kind: readmodel.OutcomeLost, At: e.DeclaredAt.UTC()})
}

// OnPackageReturned records the return; the group is judged BROKEN (cause RETURNED).
func (s *Service) OnPackageReturned(ctx context.Context, m Meta, e PackageReturned) error {
	return s.recordOutcome(ctx, m, readmodel.PackageOutcome{TrackingNumber: e.TrackingNumber, Kind: readmodel.OutcomeReturned, At: e.ReturnedAt.UTC()})
}

func (s *Service) recordOutcome(ctx context.Context, m Meta, o readmodel.PackageOutcome) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		if o.AssignmentID == "" {
			id, err := s.Shipments.AssignmentOf(ctx, o.TrackingNumber)
			if err != nil && !notFound(err) {
				return err
			}
			o.AssignmentID = id
		}
		if err := s.Shipments.PutOutcome(ctx, o); err != nil {
			return err
		}
		if o.AssignmentID == "" {
			return nil
		}
		return s.judge(ctx, o.AssignmentID)
	})
}

// OnPackageException treats a tracking exception as a risk (RepromiseOnRisk).
func (s *Service) OnPackageException(ctx context.Context, m Meta, e PackageException) error {
	return s.assignmentRisk(ctx, m, e.AssignmentID, ReasonPackageException, nil, nil)
}

// ---- customer-ordering -------------------------------------------------------------------------------------

// OnCustomerOrderPlaced is the reconciling ConfirmPromise (E7): the synchronous
// confirm normally ran already and this is a no-op; a promise still PROVISIONAL
// inside its hold is confirmed. A dead hold is logged, never retried.
func (s *Service) OnCustomerOrderPlaced(ctx context.Context, m Meta, e CustomerOrderPlaced) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		for _, g := range e.Groups {
			p, err := s.Promises.Get(ctx, promise.ID(g.PromiseID))
			if notFound(err) {
				s.Logger.WarnContext(ctx, "order placed with an unknown promise", "orderId", e.OrderID, "promiseId", g.PromiseID)
				continue
			}
			if err != nil {
				return err
			}
			err = s.confirm(ctx, p, e.OrderID, g.GroupNo)
			if errors.Is(err, promise.ErrPromiseHoldExpired) || errors.Is(err, promise.ErrPromiseClosed) {
				s.Logger.WarnContext(ctx, "order placed on a dead promise", "orderId", e.OrderID, "promiseId", g.PromiseID, "error", err)
				continue
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// OnCustomerOrderCancelled cancels the promises of the named groups, or of every
// group when none is named (CancelPromise, G21, A24).
func (s *Service) OnCustomerOrderCancelled(ctx context.Context, m Meta, e CustomerOrderCancelled) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		ps, err := s.Promises.ListByOrder(ctx, e.OrderID)
		if err != nil {
			return err
		}
		reason := e.Reason
		if reason == "" {
			reason = "ORDER_CANCELLED"
		}
		for _, p := range ps {
			if len(e.GroupNos) > 0 && !containsInt(e.GroupNos, p.GroupNo()) {
				continue
			}
			if err := s.cancelOne(ctx, p.ID(), reason); err != nil {
				return err
			}
		}
		return nil
	})
}
