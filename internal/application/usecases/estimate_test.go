package usecases_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// estimate runs EstimateDeliveries for prefix 787 over the items.
func (e *env) estimate(items []shared.Line, tiers ...shared.Tier) views.EstimateResult {
	e.t.Helper()
	res, err := e.svc.EstimateDeliveries(ctx, usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: items, Tiers: tiers})
	must(e.t, err)
	return res
}

func optionOf(t *testing.T, est views.Estimate, tier string) views.EstimateOption {
	t.Helper()
	for _, o := range est.Options {
		if o.Tier == tier {
			return o
		}
	}
	t.Fatalf("no %s option for %s in %+v (constraints %+v)", tier, est.CatalogItemID, est.Options, est.Constraints)
	return views.EstimateOption{}
}

func constraintsText(cs []views.EstimateConstraint) string {
	out := ""
	for _, c := range cs {
		out += fmt.Sprintf("[%s %s]", c.Tier, c.Code)
	}
	return out
}

// An estimate stores nothing at all: no offer, no event, no book, no promise,
// no version bump (ADR 0019).
func TestAnEstimateStoresNothing(t *testing.T) {
	e := newEnv(t)
	before := e.store.Counts()
	e.estimate([]shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}, {CatalogItemID: "ci-unknown", Quantity: 1}})
	if after := e.store.Counts(); after != before {
		t.Fatalf("store changed %+v -> %+v", before, after)
	}
	eq(t, "offers", e.store.Counts().Offers, 0)
	eq(t, "outbox", e.store.Counts().OutboxEvents, 0)
	eq(t, "no offer id was minted", e.ids.NewOfferID(), "off-1")
}

// Each option is the quote's best offer of that tier for the same single line:
// tier, window, basis, orderByAt and the time zone all agree.
func TestAnEstimateEqualsTheOneLineQuote(t *testing.T) {
	e := newEnv(t)
	est := e.estimate(lines())
	q := e.quote()
	eq(t, "estimates", len(est.Estimates), 1)
	got := est.Estimates[0]
	eq(t, "item", got.CatalogItemID, "ci-1001")
	eq(t, "quantity", got.Quantity, 2)
	eq(t, "options", len(got.Options), len(q.Offers))
	for _, o := range q.Offers {
		opt := optionOf(t, got, o.Tier)
		if !opt.Window.Start.Equal(o.Window.Earliest) || !opt.Window.End.Equal(o.Window.Latest) || opt.Window.TimeZone != o.Window.TimeZone {
			t.Errorf("%s window %+v, quote %+v", o.Tier, opt.Window, o.Window)
		}
		if opt.Basis != o.Basis || opt.OrderByAt == nil || !opt.OrderByAt.Equal(o.OrderByAt) {
			t.Errorf("%s option %+v, quote basis %s orderByAt %s", o.Tier, opt, o.Basis, o.OrderByAt)
		}
	}
	eq(t, "constraints", constraintsText(got.Constraints), "[SAME_DAY DELIVERY_SLA_UNAVAILABLE]")
	eq(t, "options ordered by tier", got.Options[0].Tier, "NEXT_DAY")
	eq(t, "computedAt", est.ComputedAt.Equal(at(22, 14, 5)), true)
	eq(t, "prefix", est.DestinationPostalPrefix, "787")
}

// Estimates come back in request order; an unknown item and a quantity no
// owner covers are ITEM_QUANTITY_NOT_AVAILABLE once, without a tier, never an
// error.
func TestEstimatesKeepRequestOrderAndUnknownItemsAreConstraints(t *testing.T) {
	e := newEnv(t)
	e.stock("N-AUS3", "ci-2002", "lst-2", 5)
	est := e.estimate([]shared.Line{
		{CatalogItemID: "ci-unknown", Quantity: 1}, {CatalogItemID: "ci-2002", Quantity: 3},
		{CatalogItemID: "ci-1001", Quantity: 38}, {CatalogItemID: "ci-1001-b", Quantity: 1},
	})
	order := ""
	for _, x := range est.Estimates {
		order += x.CatalogItemID + ","
	}
	eq(t, "request order", order, "ci-unknown,ci-2002,ci-1001,ci-1001-b,")
	for _, i := range []int{0, 2, 3} {
		eq(t, est.Estimates[i].CatalogItemID+" options", len(est.Estimates[i].Options), 0)
		eq(t, est.Estimates[i].CatalogItemID+" constraints", constraintsText(est.Estimates[i].Constraints), "[ ITEM_QUANTITY_NOT_AVAILABLE]")
	}
	optionOf(t, est.Estimates[1], "NEXT_DAY")
}

func TestAnUnservedDestinationIsOneItemWideConstraint(t *testing.T) {
	e := newEnv(t)
	res, err := e.svc.EstimateDeliveries(ctx, usecases.EstimateCommand{DestinationPostalPrefix: "999", Items: lines()})
	must(t, err)
	eq(t, "options", len(res.Estimates[0].Options), 0)
	eq(t, "constraints", constraintsText(res.Estimates[0].Constraints), "[ DESTINATION_NOT_SERVED]")
}

// A tier past its order deadline carries its tier; the requested tiers bound
// the answer.
func TestADeadlineIsAPerTierConstraintAndTiersFilter(t *testing.T) {
	e := newEnv(t)
	_, err := e.svc.PublishPromisePolicy(ctx, policy.Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100,
		Deadlines: []policy.Deadline{{Tier: shared.TierNextDay, RegionID: "R-SC", Minute: 14 * 60}}})
	must(t, err)
	got := e.estimate(lines(), shared.TierStandard, shared.TierNextDay).Estimates[0]
	eq(t, "constraints", constraintsText(got.Constraints), "[NEXT_DAY PAST_ORDER_DEADLINE]")
	eq(t, "options", len(got.Options), 1)
	eq(t, "only the asked tier", got.Options[0].Tier, "STANDARD")
}

// The measured-transit basis of ADR 0014 reaches the estimate like the quote.
func TestAnEstimateOnAMeasuredLaneCarriesBasisMEASURED(t *testing.T) {
	e := newEnv(t)
	for i := range 20 {
		e.loadObserved("ld-e-"+string(rune('a'+i)), "N-SAU1", "N-DAU1", 135, at(21, 20, 0).Add(time.Duration(i)*time.Hour))
	}
	got := e.estimate(lines(), shared.TierNextDay).Estimates[0]
	eq(t, "basis", optionOf(t, got, "NEXT_DAY").Basis, string(shared.BasisMeasured))
}

func TestEstimateValidation(t *testing.T) {
	e := newEnv(t)
	many := make([]shared.Line, usecases.MaxEstimateItems+1)
	for i := range many {
		many[i] = shared.Line{CatalogItemID: fmt.Sprintf("ci-%03d", i), Quantity: 1}
	}
	bad := []struct {
		name string
		cmd  usecases.EstimateCommand
	}{
		{"prefix", usecases.EstimateCommand{DestinationPostalPrefix: "78 7", Items: lines()}},
		{"no items", usecases.EstimateCommand{DestinationPostalPrefix: "787"}},
		{"51 items", usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: many}},
		{"duplicate", usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: append(lines(), lines()...)}},
		{"tier", usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: lines(), Tiers: []shared.Tier{"EXPRESS"}}},
		{"quantity 0", usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: []shared.Line{{CatalogItemID: "ci-1", Quantity: 0}}}},
		{"quantity 1000", usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: []shared.Line{{CatalogItemID: "ci-1", Quantity: 1000}}}},
		{"item id", usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: []shared.Line{{CatalogItemID: "", Quantity: 1}}}},
	}
	for _, c := range bad {
		if _, err := e.svc.EstimateDeliveries(ctx, c.cmd); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	ok := usecases.EstimateCommand{DestinationPostalPrefix: "787", Items: many[:usecases.MaxEstimateItems]}
	ok.Items[0].Quantity = usecases.MaxEstimateQuantity
	if _, err := e.svc.EstimateDeliveries(ctx, ok); err != nil {
		t.Fatalf("50 items with quantity 999 refused: %v", err)
	}
}
