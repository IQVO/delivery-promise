package usecases

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
)

// judge is JudgeOnDelivery for one assignment, inside the caller's unit of
// work. A group is judged when its LAST shipped package is delivered, or at the
// first package lost or returned (gaps G1 and G6). It does nothing while the
// facts are incomplete (the lock, the shipment or a delivery not seen yet): the
// next event that completes them calls it again (ADR 0002, A25).
func (s *Service) judge(ctx context.Context, assignmentID string) error {
	a, err := s.Shipments.GetAssignment(ctx, assignmentID)
	if errors.Is(err, ports.ErrNotFound) || err == nil && !a.Locked {
		return nil
	}
	if err != nil {
		return err
	}
	trackings, err := s.Shipments.Shipment(ctx, assignmentID)
	if err != nil || len(trackings) == 0 {
		return err
	}
	outcomes, err := s.Shipments.Outcomes(ctx, trackings)
	if err != nil {
		return err
	}
	verdict, ok := verdictOf(trackings, outcomes)
	if !ok {
		return nil
	}
	p, err := s.Promises.Get(ctx, promise.ID(a.PromiseID))
	if errors.Is(err, ports.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	events, err := p.Judge(verdict, s.Clock.Now())
	if err != nil {
		return ignoreStale(err)
	}
	return s.saveAll(ctx, p, nil, events)
}

// verdictOf decides the judgement from the package outcomes: the earliest lost
// or returned package breaks the promise; otherwise it needs every shipped
// package delivered, and the delivery is the latest one.
func verdictOf(trackings []string, outcomes []readmodel.PackageOutcome) (promise.Verdict, bool) {
	var first *readmodel.PackageOutcome
	var delivered []time.Time
	for i := range outcomes {
		o := outcomes[i]
		switch o.Kind {
		case readmodel.OutcomeDelivered:
			delivered = append(delivered, o.At)
		default:
			if first == nil || o.At.Before(first.At) {
				first = &outcomes[i]
			}
		}
	}
	if first != nil {
		cause := promise.CauseLost
		if first.Kind == readmodel.OutcomeReturned {
			cause = promise.CauseReturned
		}
		return promise.Verdict{Cause: cause, At: first.At}, true
	}
	if len(delivered) < len(trackings) {
		return promise.Verdict{}, false
	}
	return promise.Verdict{DeliveredAt: slices.MaxFunc(delivered, func(a, b time.Time) int { return a.Compare(b) })}, true
}
