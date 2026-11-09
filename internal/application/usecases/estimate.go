package usecases

import (
	"context"
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Estimate bounds (ADR 0019).
const (
	// MaxEstimateItems is how many items one estimate request may carry: a
	// listing page's worth.
	MaxEstimateItems = 50
	// MaxEstimateQuantity bounds the quantity of one estimated item.
	MaxEstimateQuantity = 999
)

// estimateOfferID names the in-memory offer an estimate builds to read the
// quote's best option of a tier. It is never saved, published or returned.
const estimateOfferID offer.ID = "estimate"

// EstimateCommand is the input of EstimateDeliveries.
type EstimateCommand struct {
	DestinationPostalPrefix string
	// Items are estimated one by one, each as a single-line group.
	Items []shared.Line
	// Tiers defaults to all four when empty.
	Tiers []shared.Tier
}

func (c EstimateCommand) validate() error {
	if !shared.ValidPostalPrefix(c.DestinationPostalPrefix) {
		return shared.Invalid("destinationPostalPrefix %q is not valid", c.DestinationPostalPrefix)
	}
	if len(c.Items) == 0 || len(c.Items) > MaxEstimateItems {
		return shared.Invalid("an estimate needs between 1 and %d items", MaxEstimateItems)
	}
	if err := shared.ValidateLines(c.Items); err != nil {
		return err
	}
	for _, it := range c.Items {
		if it.Quantity > MaxEstimateQuantity {
			return shared.Invalid("quantity of %q must be between 1 and %d", it.CatalogItemID, MaxEstimateQuantity)
		}
	}
	for _, t := range c.Tiers {
		if !t.Valid() {
			return shared.Invalid("tier %q is not a tier", t)
		}
	}
	return nil
}

// EstimateDeliveries answers, per item, the best option of every requested
// tier that a one-line quote of that item would offer, or the constraint that
// blocks the tier, and stores NOTHING: no offer, no event, no hold, no unit of
// work (ADR 0019). The shared inputs (policy, service area, station, network
// slice, holds, books) are loaded once for the whole request and every item
// is searched against that same copy; the measured-transit lookup is read at
// most once per lane pair. An estimate is advisory and reserves no capacity.
func (s *Service) EstimateDeliveries(ctx context.Context, cmd EstimateCommand) (views.EstimateResult, error) {
	if err := cmd.validate(); err != nil {
		return views.EstimateResult{}, err
	}
	now := s.Clock.Now().UTC()
	pol, err := s.currentPolicy(ctx)
	if err != nil {
		return views.EstimateResult{}, err
	}
	in, err := s.searchInput(ctx, now, pol, cmd.DestinationPostalPrefix, cmd.Items, cmd.Tiers, searchOptions{})
	if err != nil {
		return views.EstimateResult{}, err
	}
	in.MeasuredTransit = memoTransit(in.MeasuredTransit)
	out := views.EstimateResult{ComputedAt: now, DestinationPostalPrefix: cmd.DestinationPostalPrefix, Estimates: make([]views.Estimate, 0, len(cmd.Items))}
	for _, it := range cmd.Items {
		one := in
		one.Lines = []shared.Line{it}
		out.Estimates = append(out.Estimates, estimateOf(it, search.Run(one), cmd.DestinationPostalPrefix, pol, now))
	}
	return out, nil
}

// estimateOf reads one item's search result the way the quote does: the
// whole-quote constraints, then each group's best offer per tier (built in
// memory by offersOfGroup and dropped), ordered by tier.
func estimateOf(it shared.Line, res search.Result, prefix string, pol *policy.Policy, now time.Time) views.Estimate {
	est := views.Estimate{CatalogItemID: it.CatalogItemID, Quantity: it.Quantity, Options: []views.EstimateOption{}, Constraints: []views.EstimateConstraint{}}
	var perTier []views.Unavailable
	for _, t := range sortedTiers(res.WholeQuote) {
		perTier = append(perTier, views.Unavailable{Tier: string(t), Constraint: string(res.WholeQuote[t])})
	}
	for _, g := range res.Groups {
		offers, _, un := offersOfGroup(g, prefix, pol, now, func() offer.ID { return estimateOfferID })
		for _, o := range offers {
			est.Options = append(est.Options, optionOf(o))
		}
		perTier = append(perTier, un...)
	}
	slices.SortStableFunc(est.Options, func(a, b views.EstimateOption) int { return rankOf(a.Tier) - rankOf(b.Tier) })
	est.Constraints = constraintsOf(perTier)
	return est
}

func rankOf(tier string) int { return shared.Tier(tier).Rank() }

func optionOf(o *offer.Offer) views.EstimateOption {
	w := o.Window()
	opt := views.EstimateOption{
		Tier: string(o.Tier()), Basis: string(o.Basis()), Confidence: o.Confidence().Ptr(), CapacityBand: string(o.CapacityBand()),
		Window: views.EstimateWindow{Start: w.Earliest.UTC(), End: w.Latest.UTC(), TimeZone: w.TimeZone},
	}
	if by := o.OrderByAt(); !by.IsZero() {
		by = by.UTC()
		opt.OrderByAt = &by
	}
	return opt
}

// itemWide are the constraints that hold for the item on every tier alike:
// they are listed once, without a tier.
func itemWide(code string) bool {
	return code == string(search.DestinationNotServed) || code == string(search.ItemQuantityNotAvailable)
}

// constraintsOf lists the item-wide constraints first (once each, no tier),
// then the per-tier ones by tier.
func constraintsOf(un []views.Unavailable) []views.EstimateConstraint {
	out := []views.EstimateConstraint{}
	var tiered []views.EstimateConstraint
	for _, u := range un {
		if !itemWide(u.Constraint) {
			tiered = append(tiered, views.EstimateConstraint{Tier: u.Tier, Code: u.Constraint})
			continue
		}
		c := views.EstimateConstraint{Code: u.Constraint}
		if !slices.Contains(out, c) {
			out = append(out, c)
		}
	}
	slices.SortStableFunc(tiered, func(a, b views.EstimateConstraint) int { return rankOf(a.Tier) - rankOf(b.Tier) })
	return append(out, tiered...)
}

// memoTransit reads each (origin, destination) pair's measured transit once
// per request, so a batch of items never repeats the same lookup. A nil
// lookup (no measured-transit port) stays nil.
func memoTransit(f func(origin, dest string) (bool, int)) func(origin, dest string) (bool, int) {
	if f == nil {
		return nil
	}
	type pair struct{ origin, dest string }
	type answer struct {
		ok      bool
		minutes int
	}
	seen := map[pair]answer{}
	return func(origin, dest string) (bool, int) {
		k := pair{origin, dest}
		if a, hit := seen[k]; hit {
			return a.ok, a.minutes
		}
		ok, minutes := f(origin, dest)
		seen[k] = answer{ok, minutes}
		return ok, minutes
	}
}
