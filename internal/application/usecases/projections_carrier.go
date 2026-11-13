package usecases

import (
	"context"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
)

// OnPackageHandedToCarrier opens the carrier pairing row of the
// measured-transit read model, keyed by tracking number (ADR 0026): the
// analogue of OnLoadDeparted's load row, paired by the delivery into one
// observation of (handoffNodeID, "area:"+prefix) under the same percentile
// knobs (ADR 0014). It is a projection only: the carrier handoff carries no
// promise risk by itself - the legs to the handoff node are watched as any
// departure legs, and the carrier leg has nothing to watch (the ADR 0026 gap).
func (s *Service) OnPackageHandedToCarrier(ctx context.Context, m Meta, e PackageHandedToCarrier) error {
	return s.project(ctx, m, func(ctx context.Context) error {
		if s.TransitActuals == nil {
			return nil
		}
		return s.TransitActuals.RecordCarrierHandoff(ctx, e.TrackingNumber, e.HandoffNodeID, e.DestinationPostalPrefix, e.HandedAt.UTC())
	})
}

// OnPackageReadyForCollection records the collection-readiness as the kept
// outcome of a pickup-point promise and judges the group when it was the last
// package (ADR 0027).
func (s *Service) OnPackageReadyForCollection(ctx context.Context, m Meta, e PackageReadyForCollection) error {
	return s.recordOutcome(ctx, m, readmodel.PackageOutcome{TrackingNumber: e.TrackingNumber, AssignmentID: e.AssignmentID, Kind: readmodel.OutcomeDelivered, At: e.ReadyAt.UTC()}, func(ctx context.Context) error {
		return nil
	})
}

// OnPackageDelivered records the delivery, judges the group when it was the
// last package (F9), and pairs the carrier's open row into one measured
// carrier observation when the package rode a carrier (ADR 0026): the same
// event is the arrival half of both the linehaul and the carrier ring.
func (s *Service) OnPackageDelivered(ctx context.Context, m Meta, e PackageDelivered) error {
	return s.recordOutcome(ctx, m, readmodel.PackageOutcome{TrackingNumber: e.TrackingNumber, AssignmentID: e.AssignmentID, Kind: readmodel.OutcomeDelivered, At: e.DeliveredAt.UTC()}, func(ctx context.Context) error {
		if s.TransitActuals == nil { // an env without the measured-transit ring pairs nothing (ADR 0026)
			return nil
		}
		return s.TransitActuals.RecordCarrierDelivered(ctx, e.TrackingNumber, e.DeliveredAt.UTC())
	})
}
