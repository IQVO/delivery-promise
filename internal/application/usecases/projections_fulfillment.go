package usecases

import (
	"context"
	"errors"
	"slices"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
)

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
		if err := s.rehomeOnLock(ctx, e); err != nil {
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

// recordOutcome stores the package fact, then judges EVERY assignment that
// shares the tracking number (a merged package belongs to several, ADR 0025),
// each on its own promise window.
func (s *Service) recordOutcome(ctx context.Context, m Meta, o readmodel.PackageOutcome) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		ids, err := s.sharingAssignments(ctx, o.AssignmentID, o.TrackingNumber)
		if err != nil {
			return err
		}
		if o.AssignmentID == "" && len(ids) > 0 {
			o.AssignmentID = ids[0]
		}
		if err := s.Shipments.PutOutcome(ctx, o); err != nil {
			return err
		}
		for _, id := range ids {
			if err := s.judge(ctx, id); err != nil {
				return err
			}
		}
		return nil
	})
}

// sharingAssignments is the union of the assignment an event names (may be
// empty) and every assignment the tracking number was shipped under, sorted
// and without duplicates. An unknown tracking number adds nothing.
func (s *Service) sharingAssignments(ctx context.Context, assignmentID, trackingNumber string) ([]string, error) {
	var ids []string
	if assignmentID != "" {
		ids = append(ids, assignmentID)
	}
	if trackingNumber != "" {
		owners, err := s.Shipments.AssignmentsOf(ctx, trackingNumber)
		if err != nil && !notFound(err) {
			return nil, err
		}
		ids = union(ids, owners)
	}
	slices.Sort(ids)
	return ids, nil
}

// OnPackageException treats a tracking exception as a risk (RepromiseOnRisk).
func (s *Service) OnPackageException(ctx context.Context, m Meta, e PackageException) error {
	return s.assignmentRiskVia(ctx, m, e.AssignmentID, e.TrackingNumber, ReasonPackageException, nil, nil)
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
