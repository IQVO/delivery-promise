package features_test

import (
	"fmt"
	"strconv"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// registerCalibrationSteps registers the steps of the calibration feature
// (ADR 0020).
func registerCalibrationSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the next day's delivery wave "([^"]*)" is scheduled$`, w.nextDayWave)
	sc.Step(`^the policy needs (\d+) judged promises before a segment is buffered$`, w.calibrationMinimum)
	sc.Step(`^(\d+) (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) promises are judged on time$`, func(n int, tier string) error {
		return w.judgePromises(n, tier, -2*time.Hour)
	})
	sc.Step(`^(\d+) (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) promises are judged late by (\d+) minutes$`, func(n int, tier string, minutes int) error {
		return w.judgePromises(n, tier, time.Duration(minutes)*time.Minute)
	})
	sc.Step(`^the (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) offer carries the confidence ([0-9.]+)$`, w.offerConfidence)
	sc.Step(`^the (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) offer carries no confidence$`, w.offerNoConfidence)
	sc.Step(`^the last command published a DeliveryOfferQuoted event with the confidence ([0-9.]+)$`, w.eventConfidence)
	sc.Step(`^the promise carries the confidence ([0-9.]+)$`, w.promiseConfidence)
}

func (w *world) nextDayWave(id string) error {
	return w.svc.OnWaveScheduled(w.ctx(), w.meta("DeliveryWaveScheduled"), usecasesWave(id))
}

func (w *world) calibrationMinimum(n int) error {
	return w.given("publish the calibration minimum", func() error {
		_, err := w.svc.PublishPromisePolicy(w.ctx(), policy.Input{
			OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100, MinCalibrationObservations: n,
		})
		return err
	})
}

// judgePromises runs n promises of the tier through the whole life: quote, make,
// confirm, lock, ship and deliver at the window's latest instant plus offset
// (negative: early). The judgement closes the loop the calibration reads.
func (w *world) judgePromises(n int, tier string, offset time.Duration) error {
	for i := range n {
		suffix := fmt.Sprintf("%s-%02d", tier, i)
		w.doQuote("787", "ci-1001", 2, tierOf(tier))
		if err := w.given("quote "+suffix, func() error { return w.lastErr }); err != nil {
			return err
		}
		if err := w.makeFrom(tier); err != nil {
			return err
		}
		if err := w.given("make "+suffix, func() error { return w.lastErr }); err != nil {
			return err
		}
		if err := w.confirm("ord-"+suffix, 1); err != nil {
			return err
		}
		if err := w.given("confirm "+suffix, func() error { return w.lastErr }); err != nil {
			return err
		}
		if err := w.lockAndShip("asg-"+suffix, "ord-"+suffix, "TRK-"+suffix); err != nil {
			return err
		}
		p, err := w.promise()
		if err != nil {
			return err
		}
		if err := w.delivered("TRK-"+suffix, p.Terms.DeliverByLatest.Add(offset).Format(time.RFC3339)); err != nil {
			return err
		}
		if err := w.given("deliver "+suffix, func() error { return w.lastErr }); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) offerConfidence(tier, want string) error {
	o, err := w.offer(tier)
	if err != nil {
		return err
	}
	wantF, err := strconv.ParseFloat(want, 64)
	if err != nil {
		return err
	}
	if o.Confidence == nil || *o.Confidence != wantF {
		return fmt.Errorf("%s offer confidence %v, want %v", tier, o.Confidence, wantF)
	}
	return nil
}

func (w *world) offerNoConfidence(tier string) error {
	o, err := w.offer(tier)
	if err != nil {
		return err
	}
	if o.Confidence != nil {
		return fmt.Errorf("%s offer carries the confidence %v, want none (unknown)", tier, *o.Confidence)
	}
	return nil
}

func (w *world) eventConfidence(want string) error {
	wantF, err := strconv.ParseFloat(want, 64)
	if err != nil {
		return err
	}
	for _, e := range w.store.Outbox().Events()[w.outboxLen:] {
		if q, ok := e.(offer.DeliveryOfferQuoted); ok {
			if !q.Confidence.Known() || q.Confidence.Value() != wantF {
				return fmt.Errorf("DeliveryOfferQuoted confidence %+v, want %v", q.Confidence, wantF)
			}
			return nil
		}
	}
	return fmt.Errorf("the last command published no DeliveryOfferQuoted (published %v)", w.published())
}

func (w *world) promiseConfidence(want string) error {
	p, err := w.promise()
	if err != nil {
		return err
	}
	wantF, err := strconv.ParseFloat(want, 64)
	if err != nil {
		return err
	}
	if p.Terms.Confidence == nil || *p.Terms.Confidence != wantF {
		return fmt.Errorf("promise confidence %v, want %v", p.Terms.Confidence, wantF)
	}
	return nil
}

func tierOf(s string) shared.Tier { return shared.Tier(s) }

// usecasesWave is the second delivery wave of the worked example: the day after
// wave A, 09:00 to 21:00.
func usecasesWave(id string) usecases.WaveScheduled {
	return usecases.WaveScheduled{
		WaveID: id, StationNodeID: "N-DAU1", InductCutoffAt: at(23, 20, 0), WindowStart: at(24, 9, 0), WindowEnd: at(24, 21, 0), StopCapacity: 320,
	}
}
