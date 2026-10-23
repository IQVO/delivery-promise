package features_test

import (
	"fmt"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func registerQuoteSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^I quote (\d+) of "([^"]*)" for postal prefix "([^"]*)"$`, w.quoteAll)
	sc.Step(`^I quote (\d+) of "([^"]*)" for postal prefix "([^"]*)" on the tier (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD)$`, w.quoteTier)
	sc.Step(`^the quote offers exactly the tiers "([^"]*)"$`, w.quoteOffers)
	sc.Step(`^the (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) offer is (SCHEDULED|NOMINAL) from the node "([^"]*)" via the pickup "([^"]*)" and the wave "([^"]*)"$`, w.offerWitness)
	sc.Step(`^the (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) offer promises delivery between "([^"]*)" and "([^"]*)"$`, w.offerWindow)
	sc.Step(`^the (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) offer is (QUOTED|CONSUMED|EXPIRED)$`, w.offerState)
	sc.Step(`^the tier (NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD) is unavailable with "([^"]*)"$`, w.tierUnavailable)
	sc.Step(`^every tier is unavailable with "([^"]*)"$`, w.everyTierUnavailable)
	sc.Step(`^the order deadline for NEXT_DAY in the region "([^"]*)" is "(\d\d):(\d\d)" local time$`, w.publishDeadline)
	sc.Step(`^the item "([^"]*)" is discontinued$`, w.discontinue)
	sc.Step(`^the quote carries the policy version "([^"]*)"$`, w.quotePolicy)
}

func (w *world) doQuote(prefix, item string, qty int, tiers ...shared.Tier) {
	w.run(func() error {
		res, err := w.svc.QuoteDeliveryOffers(w.ctx(), usecases.QuoteCommand{DestinationPostalPrefix: prefix, Lines: lines(item, qty), Tiers: tiers})
		if err == nil {
			w.lastQuote = res
		}
		return err
	})
}

func (w *world) quoteAll(qty int, item, prefix string) error {
	w.doQuote(prefix, item, qty)
	return nil
}

func (w *world) quoteTier(qty int, item, prefix, tier string) error {
	w.doQuote(prefix, item, qty, shared.Tier(tier))
	return nil
}

func (w *world) quoteOffers(want string) error {
	var got []string
	for _, o := range w.lastQuote.Offers {
		got = append(got, o.Tier)
	}
	if strings.Join(got, ",") != want {
		return fmt.Errorf("offered tiers %v, want %s", got, want)
	}
	return nil
}

func (w *world) offerWitness(tier, basis, node, pickup, wave string) error {
	o, err := w.offer(tier)
	if err != nil {
		return err
	}
	got := fmt.Sprintf("%s/%s/%s/%s", o.Basis, o.WitnessPath.NodeID, o.WitnessPath.PickupDepartureID, o.WitnessPath.DeliveryWaveID)
	if want := fmt.Sprintf("%s/%s/%s/%s", basis, node, pickup, wave); got != want {
		return fmt.Errorf("%s offer witness %s, want %s", tier, got, want)
	}
	return nil
}

func (w *world) offerWindow(tier, from, to string) error {
	o, err := w.offer(tier)
	if err != nil {
		return err
	}
	earliest, err := time.Parse(time.RFC3339, from)
	if err != nil {
		return err
	}
	latest, err := time.Parse(time.RFC3339, to)
	if err != nil {
		return err
	}
	if !o.Window.Earliest.Equal(earliest) || !o.Window.Latest.Equal(latest) {
		return fmt.Errorf("%s window %s..%s, want %s..%s", tier, o.Window.Earliest, o.Window.Latest, earliest, latest)
	}
	return nil
}

func (w *world) offerState(tier, want string) error {
	o, err := w.offer(tier)
	if err != nil {
		return err
	}
	stored, err := w.svc.GetOffer(w.ctx(), o.OfferID)
	if err != nil {
		return err
	}
	if stored.State != want {
		return fmt.Errorf("%s offer is %s, want %s", tier, stored.State, want)
	}
	return nil
}

func (w *world) tierUnavailable(tier, constraint string) error {
	for _, u := range w.lastQuote.Unavailable {
		if u.Tier == tier && u.Constraint == constraint {
			return nil
		}
	}
	return fmt.Errorf("no %s/%s in unavailable %+v", tier, constraint, w.lastQuote.Unavailable)
}

func (w *world) everyTierUnavailable(constraint string) error {
	if len(w.lastQuote.Offers) != 0 || len(w.lastQuote.Unavailable) != 4 {
		return fmt.Errorf("want 0 offers and 4 unavailable tiers, got %d and %d", len(w.lastQuote.Offers), len(w.lastQuote.Unavailable))
	}
	for _, u := range w.lastQuote.Unavailable {
		if u.Constraint != constraint {
			return fmt.Errorf("tier %s unavailable with %s, want %s", u.Tier, u.Constraint, constraint)
		}
	}
	return nil
}

func (w *world) publishDeadline(region string, h, m int) error {
	return w.given("publish the order deadline", func() error {
		_, err := w.svc.PublishPromisePolicy(w.ctx(), policy.Input{
			OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100,
			Deadlines: []policy.Deadline{{Tier: shared.TierNextDay, RegionID: region, Minute: h*60 + m}},
		})
		return err
	})
}

func (w *world) discontinue(item string) error {
	return w.svc.OnCatalogItemChanged(w.ctx(), w.meta("CatalogItemDiscontinued"), usecases.CatalogItemChanged{CatalogItemID: item}, true)
}

func (w *world) quotePolicy(version string) error {
	for _, o := range w.lastQuote.Offers {
		if o.PolicyVersion != version {
			return fmt.Errorf("%s offer carries policy %s, want %s", o.Tier, o.PolicyVersion, version)
		}
	}
	if len(w.lastQuote.Offers) == 0 {
		return fmt.Errorf("the quote has no offers")
	}
	return nil
}
