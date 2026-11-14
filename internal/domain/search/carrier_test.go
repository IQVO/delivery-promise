package search

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// carrierFixture is the worked example with the destination area declared
// CARRIER_HANDOFF: the same network, the arrival ends at the handoff node
// (here the delivery station itself, a legal handoff node), and no wave is
// needed at all (ADR 0026).
func carrierFixture(options ...CarrierOption) Input {
	in := fixture()
	in.Waves = nil
	in.Carrier = &CarrierArea{Options: options}
	return in
}

func TestChooseCarrierRuleIsDeterministic(t *testing.T) {
	cheap := CarrierOption{CarrierID: "c-cheap", HandoffNodeID: "N-DAU1", CostPerPackageCents: 500, TransitMinutes: 600}
	cases := []struct {
		name string
		in   []CarrierOption
		want string
		ok   bool
	}{
		{"single option is the chosen one", []CarrierOption{cheap}, "c-cheap", true},
		{"lowest cost wins whatever the transit", []CarrierOption{
			{CarrierID: "c-pricey", HandoffNodeID: "N-DAU1", CostPerPackageCents: 501, TransitMinutes: 60},
			cheap,
		}, "c-cheap", true},
		{"equal cost: the later transit wins", []CarrierOption{
			{CarrierID: "c-a", HandoffNodeID: "N-DAU1", CostPerPackageCents: 500, TransitMinutes: 599},
			{CarrierID: "c-b", HandoffNodeID: "N-DAU1", CostPerPackageCents: 500, TransitMinutes: 601},
		}, "c-b", true},
		{"equal cost and transit: smallest carrierId", []CarrierOption{
			{CarrierID: "c-b", HandoffNodeID: "N-DAU1", CostPerPackageCents: 500, TransitMinutes: 600},
			{CarrierID: "c-a", HandoffNodeID: "N-DAU1", CostPerPackageCents: 500, TransitMinutes: 600},
		}, "c-a", true},
		{"four options choose", []CarrierOption{
			{CarrierID: "c-d", HandoffNodeID: "N-DAU1", CostPerPackageCents: 900, TransitMinutes: 60},
			{CarrierID: "c-c", HandoffNodeID: "N-DAU1", CostPerPackageCents: 800, TransitMinutes: 60},
			{CarrierID: "c-b", HandoffNodeID: "N-DAU1", CostPerPackageCents: 700, TransitMinutes: 60},
			cheap,
		}, "c-cheap", true},
		{"no option chooses nothing", nil, "", false},
		{"more than MaxCarrierOptions chooses nothing", []CarrierOption{
			{CarrierID: "c-1", HandoffNodeID: "N-DAU1"}, {CarrierID: "c-2", HandoffNodeID: "N-DAU1"},
			{CarrierID: "c-3", HandoffNodeID: "N-DAU1"}, {CarrierID: "c-4", HandoffNodeID: "N-DAU1"},
			{CarrierID: "c-5", HandoffNodeID: "N-DAU1"},
		}, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := ChooseCarrier(c.in)
			if ok != c.ok || got.CarrierID != c.want {
				t.Fatalf("ChooseCarrier = (%s, %v), want (%s, %v)", got.CarrierID, ok, c.want, c.ok)
			}
		})
	}
	// the rule never mutates the caller's declaration
	in := []CarrierOption{{CarrierID: "c-b", CostPerPackageCents: 500, TransitMinutes: 600}, {CarrierID: "c-a", CostPerPackageCents: 500, TransitMinutes: 700}}
	ChooseCarrier(in)
	if in[0].CarrierID != "c-b" || in[1].CarrierID != "c-a" {
		t.Fatalf("the declaration was reordered: %+v", in)
	}
}

func TestCarrierAreaPromisesWithoutAWave(t *testing.T) {
	in := carrierFixture(CarrierOption{CarrierID: "c-x", HandoffNodeID: "N-DAU1", CostPerPackageCents: 500, TransitMinutes: 600})
	c := best(t, onlyGroup(t, Run(in)), shared.TierStandard)
	// the arrival search is unchanged: pickup 18:00, one leg, arrival 23:30 CDT;
	// the window is that arrival plus the declared transit, wide the default 24h.
	if !c.Window.Earliest.Equal(at(22, 23, 30).Add(600 * time.Minute)) {
		t.Fatalf("earliest = %v, want the 23:30 arrival plus 600 minutes", c.Window.Earliest)
	}
	if got := c.Window.Latest.Sub(c.Window.Earliest); got != 24*time.Hour {
		t.Fatalf("window width = %v, want the default 24h (ADR 0026)", got)
	}
	w := c.Witness
	if w.CarrierID != "c-x" || w.HandoffNodeID != "N-DAU1" || w.DeliveryWaveID != "" {
		t.Fatalf("witness = %+v", w)
	}
	if w.NodeID != "N-AUS3" || w.PickupDepartureID != "dep-AUS3-1800" || len(w.Legs) != 1 {
		t.Fatalf("the path to the handoff node is the pickup and the legs: %+v", w)
	}
	if err := w.Validate(c.Basis); err != nil {
		t.Fatalf("a carrier witness validates: %v", err)
	}
	if c.Basis != shared.BasisScheduled {
		t.Fatalf("basis = %s, want SCHEDULED", c.Basis)
	}
}

func TestCarrierAreaWindowFollowsThePolicyWidth(t *testing.T) {
	width := 5
	in := carrierFixture(CarrierOption{CarrierID: "c-x", HandoffNodeID: "N-DAU1", TransitMinutes: 60})
	in.Policy, _, _ = policy.Publish(0, policy.Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100, CarrierWindowHours: &width}, at(22, 0, 0))
	c := best(t, onlyGroup(t, Run(in)), shared.TierStandard)
	if got := c.Window.Latest.Sub(c.Window.Earliest); got != 5*time.Hour {
		t.Fatalf("window width = %v, want the published 5h", got)
	}
}

func TestCarrierMeasuredTransitPushesLaterOnly(t *testing.T) {
	base := carrierFixture(CarrierOption{CarrierID: "c-x", HandoffNodeID: "N-DAU1", TransitMinutes: 600})
	arrival := at(22, 23, 30)

	later := base
	later.MeasuredCarrierTransit = func(string) (bool, int) { return true, 900 }
	c := best(t, onlyGroup(t, Run(later)), shared.TierStandard)
	if !c.Window.Earliest.Equal(arrival.Add(900*time.Minute)) || c.Basis != shared.BasisMeasured {
		t.Fatalf("a later measured carrier transit must push the window and the basis: %+v", c)
	}

	earlier := base
	earlier.MeasuredCarrierTransit = func(string) (bool, int) { return true, 100 }
	c = best(t, onlyGroup(t, Run(earlier)), shared.TierStandard)
	if !c.Window.Earliest.Equal(arrival.Add(600*time.Minute)) || c.Basis != shared.BasisScheduled {
		t.Fatalf("an earlier measured transit never promises earlier than the declaration: %+v", c)
	}

	tooFew := base
	tooFew.MeasuredCarrierTransit = func(string) (bool, int) { return false, 0 }
	c = best(t, onlyGroup(t, Run(tooFew)), shared.TierStandard)
	if !c.Window.Earliest.Equal(arrival.Add(600 * time.Minute)) {
		t.Fatalf("below minObservations the declaration stands: %+v", c)
	}
}

func TestCarrierAreaTierBoundRefusesTooSlowAWindow(t *testing.T) {
	// a 36-hour transit pushes the window two local days out: NEXT_DAY (one
	// day) is DELIVERY_SLA_UNAVAILABLE, STANDARD (seven) still offers.
	in := carrierFixture(CarrierOption{CarrierID: "c-x", HandoffNodeID: "N-DAU1", TransitMinutes: 36 * 60})
	g := onlyGroup(t, Run(in))
	if _, exists := g.Candidates[shared.TierNextDay]; exists {
		t.Fatal("NEXT_DAY must not be offered")
	}
	if got := g.Unavailable[shared.TierNextDay]; got != DeliverySLAUnavailable {
		t.Fatalf("NEXT_DAY constraint = %s", got)
	}
	if len(g.Candidates[shared.TierStandard]) == 0 {
		t.Fatal("STANDARD must still be offered")
	}
}

func TestCarrierAreaWithoutOptionsFindsNothing(t *testing.T) {
	in := carrierFixture()
	g := onlyGroup(t, Run(in))
	for _, tier := range shared.AllTiers() {
		if len(g.Candidates[tier]) != 0 || g.Unavailable[tier] != DeliverySLAUnavailable {
			t.Fatalf("%s must be DELIVERY_SLA_UNAVAILABLE with no carrier options: %+v", tier, g.Unavailable[tier])
		}
	}
}
