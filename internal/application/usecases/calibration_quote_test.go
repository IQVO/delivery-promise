package usecases_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// seedSegment records kept promises and then late ones into a segment, all
// judged now.
func (e *env) seedSegment(key readmodel.CalibrationKey, kept, late, lateBy int) {
	e.t.Helper()
	for i := range kept + late {
		minutes := 0
		if i >= kept {
			minutes = lateBy
		}
		must(e.t, e.store.Calibration().RecordJudgement(ctx, key, readmodel.CalibrationObservation{LateByMinutes: minutes, ObservedAt: e.clock.Now()}))
	}
}

// withSecondWave adds the day after's delivery wave to the worked example.
func (e *env) withSecondWave() {
	e.wave("W-DAU1-1024-B", at(23, 20, 0), at(24, 9, 0), at(24, 21, 0), 320)
}

func twoDayScheduled() readmodel.CalibrationKey {
	return readmodel.CalibrationKey{Tier: shared.TierTwoDay, Basis: shared.BasisScheduled, NodeGrade: search.GradeProvisional}
}

// deps rebuilds the env's Deps, so a test can change one port.
func (e *env) deps() usecases.Deps {
	return usecases.Deps{
		UoW: e.store.UnitOfWork(), Offers: e.store.Offers(), Promises: e.store.Promises(), Books: e.store.Books(), Policies: e.store.Policies(),
		Network: e.store.Network(), Shipments: e.store.Shipments(), Outbox: e.store.Outbox(), Inbox: e.store.Inbox(), Clock: e.clock, IDs: e.ids,
	}
}

func TestAQuoteFromASegmentWithEnoughHistoryIsBufferedAndConfident(t *testing.T) {
	e := newEnv(t)
	e.withSecondWave()
	e.seedSegment(twoDayScheduled(), 55, 5, 400) // n = 60 >= 50; 5 of 60 late: the 95% quantile is 400
	o := offerOf(t, e.quote(shared.TierTwoDay), "TWO_DAY")
	if !o.Window.Latest.Equal(at(24, 21, 0)) {
		t.Fatalf("window = %+v, want the 24th: 23:30 + 400 minutes misses the 06:00 induct cutoff", o.Window)
	}
	if o.Confidence == nil || *o.Confidence != 1 {
		t.Fatalf("confidence = %v, want 1 (every observation is within the 400-minute buffer)", o.Confidence)
	}
	quoted := false
	for _, ev := range e.store.Outbox().Events() {
		if q, ok := ev.(offer.DeliveryOfferQuoted); ok {
			quoted = true
			if !q.Confidence.Known() || q.Confidence.Value() != 1 {
				t.Fatalf("DeliveryOfferQuoted confidence = %+v", q.Confidence)
			}
		}
	}
	if !quoted {
		t.Fatal("no DeliveryOfferQuoted")
	}
}

func TestAQuoteBelowTheMinimumIsExactlyTheF2Quote(t *testing.T) {
	e := newEnv(t)
	e.withSecondWave()
	e.seedSegment(twoDayScheduled(), 0, 49, 400) // one short of 50
	o := offerOf(t, e.quote(shared.TierTwoDay), "TWO_DAY")
	if !o.Window.Latest.Equal(at(23, 21, 0)) || o.Confidence != nil {
		t.Fatalf("window %+v confidence %v: below the minimum nothing changes and nothing is claimed", o.Window, o.Confidence)
	}
}

func TestThePolicyDecidesTheMinimumTheTargetAndTheCap(t *testing.T) {
	e := newEnv(t)
	e.withSecondWave()
	_, err := e.svc.PublishPromisePolicy(ctx, policyInput(10, 90, 120))
	must(t, err)
	e.seedSegment(twoDayScheduled(), 8, 2, 400) // n = 10; 80% kept, the 90% quantile is 400, capped at 120
	o := offerOf(t, e.quote(shared.TierTwoDay), "TWO_DAY")
	// 23:30 + 120 minutes = 01:30, still before wave A's 06:00 induct cutoff
	if !o.Window.Latest.Equal(at(23, 21, 0)) {
		t.Fatalf("window = %+v: the cap must hold the buffer at 120 minutes", o.Window)
	}
	if o.Confidence == nil || *o.Confidence != 0.8 {
		t.Fatalf("confidence = %v, want 0.8: eight of ten are within the capped buffer", o.Confidence)
	}
}

func TestAnEstimateCarriesTheConfidenceOfItsOption(t *testing.T) {
	e := newEnv(t)
	e.seedSegment(readmodel.CalibrationKey{Tier: shared.TierNextDay, Basis: shared.BasisScheduled, NodeGrade: search.GradeProvisional}, 60, 0, 0)
	est := e.estimate(lines(), shared.TierNextDay, shared.TierTwoDay)
	next, two := optionOf(t, est.Estimates[0], "NEXT_DAY"), optionOf(t, est.Estimates[0], "TWO_DAY")
	if next.Confidence == nil || *next.Confidence != 1 || two.Confidence != nil {
		t.Fatalf("NEXT_DAY confidence %v, TWO_DAY %v: only the calibrated segment speaks", next.Confidence, two.Confidence)
	}
}

func TestThePromiseAndItsEventCarryTheOfferConfidenceAndARevisionTakesTheNewOne(t *testing.T) {
	e := newEnv(t)
	if o := offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY"); o.Confidence != nil {
		t.Fatalf("no history yet, confidence = %v", o.Confidence)
	}
	e.seedSegment(nextDayScheduled(search.GradeProvisional), 60, 0, 0)
	made := offerOf(t, e.quote(shared.TierNextDay), "NEXT_DAY") // calibrated now
	p := e.make(made.OfferID).Promise
	if p.Terms.Confidence == nil || *p.Terms.Confidence != 1 {
		t.Fatalf("promise terms confidence = %v", p.Terms.Confidence)
	}
	for _, ev := range e.store.Outbox().Events() {
		if m, ok := ev.(promise.PromiseMade); ok && (!m.Terms.Confidence.Known() || m.Terms.Confidence.Value() != 1) {
			t.Fatalf("PromiseMade confidence = %+v", m.Terms.Confidence)
		}
	}
	_, err := e.svc.ConfirmPromise(ctx, p.PromiseID, "ord-1", 1)
	must(t, err)
	e.departure("dep-AUS3-1730", "lane-1", "PICKUP", "N-AUS3", "N-SAU1", at(22, 17, 30), at(22, 18, 45), 40)
	must(t, e.svc.OnDepartureCancelled(ctx, e.meta("DepartureCancelled"), usecases.DepartureCancelled{DepartureID: "dep-AUS3-1800"}))
	got, err := e.svc.GetPromise(ctx, p.PromiseID)
	must(t, err)
	if len(got.Revisions) != 1 || got.Terms.Confidence == nil || *got.Terms.Confidence != 1 {
		t.Fatalf("revised terms confidence = %v, revisions %d", got.Terms.Confidence, len(got.Revisions))
	}
}

// unreadable is a PromiseCalibrationView whose reads fail.
type unreadable struct{ ports.PromiseCalibrationView }

func (unreadable) Segments(context.Context, int, time.Time) (map[readmodel.CalibrationKey][]int, error) {
	return nil, errors.New("calibration store down")
}

func TestAnUnreadableCalibrationViewNeverFailsAQuote(t *testing.T) {
	e := newEnv(t)
	e.withSecondWave()
	e.seedSegment(twoDayScheduled(), 55, 5, 400)
	d := e.deps()
	d.Calibration = unreadable{e.store.Calibration()}
	res, err := usecases.New(d).QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		DestinationPostalPrefix: "787", Lines: lines(), Tiers: []shared.Tier{shared.TierTwoDay},
	})
	must(t, err)
	if o := offerOf(t, res, "TWO_DAY"); !o.Window.Latest.Equal(at(23, 21, 0)) || o.Confidence != nil {
		t.Fatalf("an unreadable view must quote as before calibration: %+v", o)
	}
}

func TestAServiceWithoutTheViewQuotesAsBefore(t *testing.T) {
	e := newEnv(t)
	e.seedSegment(nextDayScheduled(search.GradeProvisional), 60, 0, 0)
	res, err := usecases.New(e.deps()).QuoteDeliveryOffers(ctx, usecases.QuoteCommand{
		DestinationPostalPrefix: "787", Lines: lines(), Tiers: []shared.Tier{shared.TierNextDay},
	})
	must(t, err)
	if o := offerOf(t, res, "NEXT_DAY"); o.Confidence != nil {
		t.Fatalf("without the view nothing is claimed: %v", o.Confidence)
	}
}
