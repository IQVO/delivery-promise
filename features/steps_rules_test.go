package features_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// The steps of the documentation-derived rule scenarios (features/*_rules.feature):
// the network facts a producer can publish (a scorecard, a cancelled wave, a
// stale capacity figure), the policy knobs of PromisePolicy, and the lifecycle
// facts of orders and packages that the older step files do not cover. They
// reuse the worked example of world_test.go and its quote/promise steps.

// tierNames is the alternation of every tier a step may name.
const tierNames = `NEXT_DAY|TWO_DAY|SAME_DAY|STANDARD|CONSOLIDATED`

// rules is the scenario-scoped memory of the rule steps: offers and promises
// a scenario keeps under a name while it makes others.
type rules struct {
	w        *world
	offers   map[string]views.Offer
	promises map[string]string
	swept    int
}

func registerRulesSteps(sc *godog.ScenarioContext, w *world) {
	r := &rules{w: w}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		r.offers, r.promises, r.swept = map[string]views.Offer{}, map[string]string{}, 0
		return ctx, nil
	})
	r.registerClockAndQuote(sc)
	r.registerPolicy(sc)
	r.registerNetwork(sc)
	r.registerFeeds(sc)
	r.registerLifecycle(sc)
	r.registerJudgement(sc)
}

func parseInstant(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return t, fmt.Errorf("%q is not an RFC 3339 instant: %w", s, err)
	}
	return t, nil
}

func (r *rules) registerClockAndQuote(sc *godog.ScenarioContext) {
	sc.Step(`^the clock is set to "([^"]*)"$`, r.setClock)
	sc.Step(`^the (`+tierNames+`) offer expires at "([^"]*)"$`, r.offerExpires)
	sc.Step(`^the (`+tierNames+`) offer is remembered as "([^"]*)"$`, r.rememberOffer)
	sc.Step(`^a promise exists from the remembered offer "([^"]*)"$`, r.givenFromRemembered)
	sc.Step(`^I quote the cart "([^"]*)" for postal prefix "([^"]*)"$`, r.quoteCart)
	sc.Step(`^the quote has (\d+) groups?$`, r.quoteGroups)
	sc.Step(`^the group (\d+) NEXT_DAY offer ships from the node "([^"]*)" and covers "([^"]*)"$`, r.groupOffer)
}

func (r *rules) setClock(at string) error {
	t, err := parseInstant(at)
	if err != nil {
		return err
	}
	r.w.clock.Set(t)
	return nil
}

func (r *rules) offerExpires(tier, at string) error {
	o, err := r.w.offer(tier)
	if err != nil {
		return err
	}
	want, err := parseInstant(at)
	if err != nil {
		return err
	}
	if !o.ExpiresAt.Equal(want) {
		return fmt.Errorf("the %s offer expires at %s, want %s", tier, o.ExpiresAt.Format(time.RFC3339), want.Format(time.RFC3339))
	}
	return nil
}

func (r *rules) rememberOffer(tier, label string) error {
	o, err := r.w.offer(tier)
	if err != nil {
		return err
	}
	r.offers[label] = o
	return nil
}

// givenFromRemembered makes a promise from a kept offer, as a precondition: it
// must succeed, or the scenario is broken.
func (r *rules) givenFromRemembered(label string) error {
	o, ok := r.offers[label]
	if !ok {
		return fmt.Errorf("the scenario has not remembered an offer as %q", label)
	}
	ls := make([]shared.Line, len(o.Lines))
	for i, l := range o.Lines {
		ls[i] = shared.Line{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity}
	}
	r.w.run(func() error {
		res, err := r.w.svc.MakePromise(r.w.ctx(), usecases.MakePromiseCommand{OfferID: o.OfferID, Lines: ls})
		if err == nil {
			r.w.promiseID, r.w.created = res.Promise.PromiseID, res.Created
		}
		return err
	})
	return r.w.given("make a promise from the remembered offer "+label, func() error { return r.w.lastErr })
}

// parseCart reads "ci-1001:2,ci-2002:1" into lines.
func parseCart(cart string) ([]shared.Line, error) {
	var out []shared.Line
	for _, part := range strings.Split(cart, ",") {
		item, qty, ok := strings.Cut(strings.TrimSpace(part), ":")
		n, err := strconv.Atoi(qty)
		if !ok || err != nil {
			return nil, fmt.Errorf("cart entry %q is not item:quantity", part)
		}
		out = append(out, shared.Line{CatalogItemID: item, Quantity: n})
	}
	return out, nil
}

func (r *rules) quoteCart(cart, prefix string) error {
	ls, err := parseCart(cart)
	if err != nil {
		return err
	}
	r.w.run(func() error {
		res, err := r.w.svc.QuoteDeliveryOffers(r.w.ctx(), usecases.QuoteCommand{DestinationPostalPrefix: prefix, Lines: ls})
		if err == nil {
			r.w.lastQuote = res
		}
		return err
	})
	return nil
}

func (r *rules) quoteGroups(want int) error {
	seen := map[int]bool{}
	for _, o := range r.w.lastQuote.Offers {
		seen[o.GroupNo] = true
	}
	if len(seen) != want {
		return fmt.Errorf("the quote has %d groups %v, want %d", len(seen), seen, want)
	}
	return nil
}

func (r *rules) groupOffer(group int, node, cart string) error {
	for _, o := range r.w.lastQuote.Offers {
		if o.Tier != "NEXT_DAY" || o.GroupNo != group {
			continue
		}
		got := make([]string, len(o.Lines))
		for i, l := range o.Lines {
			got[i] = fmt.Sprintf("%s:%d", l.CatalogItemID, l.Quantity)
		}
		if o.WitnessPath.NodeID != node || strings.Join(got, ",") != cart {
			return fmt.Errorf("group %d NEXT_DAY offer ships %s from %s, want %s from %s", group, strings.Join(got, ","), o.WitnessPath.NodeID, cart, node)
		}
		return nil
	}
	return fmt.Errorf("the quote has no NEXT_DAY offer for group %d (offers %d, unavailable %+v)", group, len(r.w.lastQuote.Offers), r.w.lastQuote.Unavailable)
}

func (r *rules) registerPolicy(sc *godog.ScenarioContext) {
	sc.Step(`^the promise policy is published with an offer TTL of (\d+) minutes, a hold TTL of (\d+) minutes, (\d+) packages? per order and a (\d+) percent share$`, r.publishPolicy)
	sc.Step(`^the NEXT_DAY order deadlines are "(\d\d):(\d\d)" for every region and "(\d\d):(\d\d)" for the region "([^"]*)"$`, r.publishRegionDeadlines)
	sc.Step(`^the TWO_DAY order deadline in the region "([^"]*)" is "(\d\d):(\d\d)" local time$`, r.publishTwoDayDeadline)
	sc.Step(`^the current promise policy is "([^"]*)"$`, r.currentPolicy)
	sc.Step(`^the promise carries the policy version "([^"]*)"$`, r.promisePolicy)
}

func basePolicy() policy.Input {
	return policy.Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100}
}

func (r *rules) publish(in policy.Input) error {
	return r.w.given("publish the promise policy", func() error {
		_, err := r.w.svc.PublishPromisePolicy(r.w.ctx(), in)
		return err
	})
}

func (r *rules) publishPolicy(offerTTL, holdTTL, packages, share int) error {
	in := basePolicy()
	in.OfferTTLMinutes, in.HoldTTLMinutes, in.PackagesPerOrder, in.SharePercent = offerTTL, holdTTL, packages, share
	return r.publish(in)
}

func (r *rules) publishRegionDeadlines(allH, allM, regH, regM int, region string) error {
	in := basePolicy()
	in.Deadlines = []policy.Deadline{
		{Tier: shared.TierNextDay, Minute: allH*60 + allM},
		{Tier: shared.TierNextDay, RegionID: region, Minute: regH*60 + regM},
	}
	return r.publish(in)
}

func (r *rules) publishTwoDayDeadline(region string, h, m int) error {
	in := basePolicy()
	in.Deadlines = []policy.Deadline{{Tier: shared.TierTwoDay, RegionID: region, Minute: h*60 + m}}
	return r.publish(in)
}

func (r *rules) currentPolicy(want string) error {
	p, err := r.w.svc.CurrentPolicy(r.w.ctx())
	if err != nil {
		return err
	}
	if p.PolicyVersion != want {
		return fmt.Errorf("the current policy is %s, want %s", p.PolicyVersion, want)
	}
	return nil
}

func (r *rules) promisePolicy(want string) error {
	p, err := r.w.promise()
	if err != nil {
		return err
	}
	if p.Terms.PolicyVersion != want {
		return fmt.Errorf("the promise carries policy %s, want %s", p.Terms.PolicyVersion, want)
	}
	return nil
}

func (r *rules) registerNetwork(sc *godog.ScenarioContext) {
	sc.Step(`^the node "([^"]*)" is graded "(A|B|C|D|PROVISIONAL)" in the (30d|7d) window(?: computed at "([^"]*)")?$`, r.grade)
	sc.Step(`^a linehaul departure "([^"]*)" is scheduled with the cutoff "([^"]*)" and the arrival "([^"]*)" for (\d+) packages$`, r.linehaul)
	sc.Step(`^the departure "([^"]*)" has (\d+) packages? left$`, r.departureLeft)
	sc.Step(`^the delivery wave "([^"]*)" has (\d+) stops? left$`, r.waveLeft)
	sc.Step(`^the delivery wave "([^"]*)" is cancelled$`, r.cancelWave)
	sc.Step(`^the node "([^"]*)" is retired$`, r.retire)
	sc.Step(`^the node "([^"]*)" declares profile revision (\d+) with a cutoff slot of (\d+) orders$`, r.declareRevision)
	sc.Step(`^the node "([^"]*)" declares profile revision (\d+) with no order limit and a throughput cap of (\d+) per (HOUR|DAY)$`, r.declareThroughput)
	sc.Step(`^the delivery station "([^"]*)" is registered$`, func(id string) error { return r.w.node(id, "DELIVERY_STATION", "", nil) })
	sc.Step(`^the postal prefix "([^"]*)" is assigned to the station "([^"]*)" with the assignment version (\d+)$`, r.assignArea)
	sc.Step(`^the (MICRO|SUB_SAME_DAY|SORTABLE) fulfilment node "N-MIC1" feeds the station "N-DAU1" directly for a same-day wave$`, r.directNode)
	sc.Step(`^the second fulfilment node "N-DFW7" is projected with a pickup at 18:00$`, r.secondNode)
	sc.Step(`^the node "([^"]*)" stocks (\d+) of "([^"]*)"$`, r.stockAt)
	sc.Step(`^the node "([^"]*)" is (eligible|ineligible) for (NEXT_DAY|SAME_DAY) as of "([^"]*)"$`, r.eligibility)
}

func (r *rules) grade(node, grade, window, computedAt string) error {
	at := r.w.clock.Now()
	if computedAt != "" {
		t, err := parseInstant(computedAt)
		if err != nil {
			return err
		}
		at = t
	}
	return r.w.svc.OnScorecardUpdated(r.w.ctx(), r.w.meta("NodeScorecardUpdated"), usecases.ScorecardUpdated{
		NodeID: node, Window: window, Grade: grade, Observations: 200, ComputedAt: at,
	})
}

func (r *rules) linehaul(id, cutoff, arrival string, capacity int) error {
	c, err := parseInstant(cutoff)
	if err != nil {
		return err
	}
	a, err := parseInstant(arrival)
	if err != nil {
		return err
	}
	return r.w.departure(id, "lane-2", "LINEHAUL", "N-SAU1", "N-DAU1", c, a, capacity)
}

func (r *rules) departureLeft(id string, remaining int) error {
	return r.w.svc.OnDepartureCapacityChanged(r.w.ctx(), r.w.meta("DepartureCapacityChanged"), usecases.DepartureCapacityChanged{DepartureID: id, RemainingPackages: remaining})
}

func (r *rules) waveLeft(id string, remaining int) error {
	return r.w.svc.OnWaveCapacityChanged(r.w.ctx(), r.w.meta("DeliveryWaveCapacityChanged"), usecases.WaveCapacityChanged{WaveID: id, RemainingStops: remaining})
}

// cancelWave is the trigger of the wave re-promising scenarios: the last
// command, whose published events the Then steps inspect.
func (r *rules) cancelWave(id string) error {
	r.w.run(func() error {
		return r.w.svc.OnWaveCancelled(r.w.ctx(), r.w.meta("DeliveryWaveCancelled"), usecases.WaveCancelled{WaveID: id})
	})
	return r.w.lastErr
}

func (r *rules) retire(node string) error {
	return r.w.svc.OnNodeStateChanged(r.w.ctx(), r.w.meta("NodeRetired"), usecases.NodeStateChanged{NodeID: node}, readmodel.NodeRetired)
}

func (r *rules) declareRevision(node string, revision, maxOrders int) error {
	p := fcProfile(revision, 95, maxOrders)
	p.NodeID = node
	return r.w.svc.OnNodeProfileDeclared(r.w.ctx(), r.w.meta("NodeOperationalProfileDeclared"), p)
}

func (r *rules) declareThroughput(node string, revision, value int, unit string) error {
	p := fcProfile(revision, 95, 0)
	p.NodeID = node
	p.ThroughputCap = usecases.ThroughputCapDTO{Value: value, TimeUnit: unit}
	return r.w.svc.OnNodeProfileDeclared(r.w.ctx(), r.w.meta("NodeOperationalProfileDeclared"), p)
}

func (r *rules) assignArea(prefix, station string, version int) error {
	return r.w.svc.OnServiceAreaAssigned(r.w.ctx(), r.w.meta("ServiceAreaAssigned"), usecases.ServiceAreaAssigned{
		PostalPrefix: prefix, DeliveryStationNodeID: station, RegionID: "R-SC", AssignmentVersion: version,
	})
}

// directNode adds a fulfilment node whose pickup goes straight to the
// destination's delivery station, ahead of a delivery wave of the same day:
// the one shape of path SAME_DAY admits, and only for a SUB_SAME_DAY or MICRO
// subtype (use-cases step 6, ADR 0002 A5).
func (r *rules) directNode(subtype string) error {
	w := r.w
	profile := fcProfile(1, 30, 500)
	steps := []func() error{
		func() error { return w.node("N-MIC1", "FULFILLMENT_CENTER", subtype, &profile) },
		func() error {
			return w.svc.OnLaneOpened(w.ctx(), w.meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-3", OriginNodeID: "N-MIC1", DestinationNodeID: "N-DAU1", NominalTransitMinutes: 30})
		},
		func() error {
			return w.departure("dep-MIC1-1630", "lane-3", "PICKUP", "N-MIC1", "N-DAU1", at(22, 16, 30), at(22, 17, 0), 40)
		},
		func() error {
			return w.svc.OnWaveScheduled(w.ctx(), w.meta("DeliveryWaveScheduled"), usecases.WaveScheduled{
				WaveID: "W-DAU1-1022-E", StationNodeID: "N-DAU1", InductCutoffAt: at(22, 18, 0), WindowStart: at(22, 19, 0), WindowEnd: at(22, 21, 0), StopCapacity: 100,
			})
		},
		func() error { return w.stock("N-MIC1", "ci-1001", "lst-mic", 10) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("projecting the direct node: %w", err)
		}
	}
	return nil
}

// secondNode adds a second sortable fulfilment node on its own lane into the
// same sortation centre and linehaul departure, stocking the item ci-2002 only.
func (r *rules) secondNode() error {
	w := r.w
	profile := fcProfile(1, 95, 2500)
	steps := []func() error{
		func() error { return w.node("N-DFW7", "FULFILLMENT_CENTER", "SORTABLE", &profile) },
		func() error {
			return w.svc.OnLaneOpened(w.ctx(), w.meta("LaneOpened"), usecases.LaneOpened{LaneID: "lane-4", OriginNodeID: "N-DFW7", DestinationNodeID: "N-SAU1", NominalTransitMinutes: 75})
		},
		func() error {
			return w.departure("dep-DFW7-1800", "lane-4", "PICKUP", "N-DFW7", "N-SAU1", at(22, 18, 0), at(22, 19, 15), 40)
		},
		func() error { return w.stock("N-DFW7", "ci-2002", "lst-dfw-2002", 5) },
	}
	for _, step := range steps {
		if err := step(); err != nil {
			return fmt.Errorf("projecting the second node: %w", err)
		}
	}
	return nil
}

func (r *rules) stockAt(node string, qty int, item string) error {
	return r.w.stock(node, item, "lst-"+node+"-"+item, qty)
}

func (r *rules) eligibility(node, state, tier, at string) error {
	t, err := parseInstant(at)
	if err != nil {
		return err
	}
	eligible := state == "eligible"
	return r.w.svc.OnNodeTierEligibilityChanged(r.w.ctx(), r.w.meta("NodeTierEligibilityChanged"), usecases.NodeTierEligibilityChanged{
		NodeID: node, Tier: tier, Eligible: &eligible, ChangedAt: t,
	})
}

func (r *rules) registerFeeds(sc *godog.ScenarioContext) {
	sc.Step(`^the listing "([^"]*)" reports (\d+) units as of "([^"]*)"$`, r.listingReports)
	sc.Step(`^the listing "([^"]*)" is (suppressed|restored)$`, r.listingToggled)
	sc.Step(`^the departure "([^"]*)" cancellation is delivered twice with the same CloudEvents id$`, r.cancelTwice)
	sc.Step(`^the delivery of the package "([^"]*)" at "([^"]*)" is delivered twice with the same CloudEvents id$`, r.deliverTwice)
}

func (r *rules) listingReports(id string, qty int, asOf string) error {
	t, err := parseInstant(asOf)
	if err != nil {
		return err
	}
	l, err := r.w.store.Network().GetListing(r.w.ctx(), id)
	if err != nil {
		return err
	}
	return r.w.svc.OnListingAvailabilityChanged(r.w.ctx(), r.w.meta("ListingAvailabilityChanged"), usecases.ListingAvailabilityChanged{
		ListingID: id, CatalogItemID: l.ItemID, NodeID: l.NodeID, OwnerRef: l.OwnerRef, AvailableQty: qty, AsOf: t,
	})
}

func (r *rules) listingToggled(id, change string) error {
	l, err := r.w.store.Network().GetListing(r.w.ctx(), id)
	if err != nil {
		return err
	}
	name := "ListingRestored"
	if change == "suppressed" {
		name = "ListingSuppressed"
	}
	return r.w.svc.OnListingToggled(r.w.ctx(), r.w.meta(name), usecases.ListingToggled{ListingID: id, NodeID: l.NodeID}, change == "suppressed")
}

// cancelTwice redelivers one DepartureCancelled message (same CloudEvents id):
// the inbox makes the second delivery commit nothing.
func (r *rules) cancelTwice(id string) error {
	m := r.w.meta("DepartureCancelled")
	e := usecases.DepartureCancelled{DepartureID: id}
	if err := r.w.svc.OnDepartureCancelled(r.w.ctx(), m, e); err != nil {
		return err
	}
	r.w.run(func() error { return r.w.svc.OnDepartureCancelled(r.w.ctx(), m, e) })
	return nil
}

func (r *rules) deliverTwice(tracking, at string) error {
	when, err := parseInstant(at)
	if err != nil {
		return err
	}
	m := r.w.meta("PackageDelivered")
	e := usecases.PackageDelivered{TrackingNumber: tracking, DeliveredAt: when}
	if err := r.w.svc.OnPackageDelivered(r.w.ctx(), m, e); err != nil {
		return err
	}
	r.w.run(func() error { return r.w.svc.OnPackageDelivered(r.w.ctx(), m, e) })
	return nil
}
