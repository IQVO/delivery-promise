package features_test

import (
	"fmt"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

func registerConsolidatedSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^delivery waves are scheduled on the days (\d+) to (\d+)$`, w.wavesOnDays)
	sc.Step(`^I quote (\d+) of "([^"]*)" for postal prefix "([^"]*)" for the delivery day "([^"]*)"$`, w.quoteDay)
	sc.Step(`^the CONSOLIDATED offer promises delivery between "([^"]*)" and "([^"]*)"$`, func(from, to string) error { return w.offerWindow("CONSOLIDATED", from, to) })
	sc.Step(`^the CONSOLIDATED offer is (consolidation eligible|not consolidation eligible)$`, w.offerEligible)
	sc.Step(`^I make a promise from the CONSOLIDATED offer$`, func() error { return w.makeFrom("CONSOLIDATED") })
	sc.Step(`^the promise is CONSOLIDATED and consolidation eligible$`, w.promiseEligible)
}

func (w *world) wavesOnDays(from, to int) error {
	for d := from; d <= to; d++ {
		id := fmt.Sprintf("W-DAU1-10%02d", d)
		if err := w.given("schedule wave "+id, func() error {
			return w.svc.OnWaveScheduled(w.ctx(), w.meta("DeliveryWaveScheduled"), usecases.WaveScheduled{
				WaveID: id, StationNodeID: "N-DAU1", InductCutoffAt: at(d, 6, 0), WindowStart: at(d, 9, 0), WindowEnd: at(d, 21, 0), StopCapacity: 320,
			})
		}); err != nil {
			return err
		}
	}
	return nil
}

func (w *world) quoteDay(qty int, item, prefix, day string) error {
	w.run(func() error {
		res, err := w.svc.QuoteDeliveryOffers(w.ctx(), usecases.QuoteCommand{
			DestinationPostalPrefix: prefix, Lines: lines(item, qty), ConsolidatedDeliveryDay: day,
		})
		if err == nil {
			w.lastQuote = res
		}
		return err
	})
	return nil
}

func (w *world) offerEligible(phrase string) error {
	o, err := w.offer("CONSOLIDATED")
	if err != nil {
		return err
	}
	want := phrase == "consolidation eligible"
	if o.ConsolidationEligible == nil || *o.ConsolidationEligible != want {
		return fmt.Errorf("consolidationEligible = %v, want %v", o.ConsolidationEligible, want)
	}
	return nil
}

func (w *world) promiseEligible() error {
	p, err := w.promise()
	if err != nil {
		return err
	}
	if p.Terms.Tier != "CONSOLIDATED" || p.Terms.ConsolidationEligible == nil || !*p.Terms.ConsolidationEligible {
		return fmt.Errorf("terms %+v, want a CONSOLIDATED promise that is consolidation eligible", p.Terms)
	}
	if want := at(27, 21, 0); !p.Terms.DeliverByLatest.Equal(want) {
		return fmt.Errorf("deliverByLatest %s, want %s", p.Terms.DeliverByLatest.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	return nil
}
