package usecases

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/views"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// QuoteCommand is the input of QuoteDeliveryOffers.
type QuoteCommand struct {
	DestinationPostalPrefix string
	Lines                   []shared.Line
	// Tiers defaults to all four when empty.
	Tiers []shared.Tier
}

func (c QuoteCommand) validate() error {
	if !shared.ValidPostalPrefix(c.DestinationPostalPrefix) {
		return shared.Invalid("destinationPostalPrefix %q is not valid", c.DestinationPostalPrefix)
	}
	if err := shared.ValidateLines(c.Lines); err != nil {
		return err
	}
	for _, t := range c.Tiers {
		if !t.Valid() {
			return shared.Invalid("tier %q is not a tier", t)
		}
	}
	return nil
}

// QuoteDeliveryOffers runs the PromisePolicy search over the local read models
// and persists one QUOTED offer per (group, tier) that has a feasible witness
// path; every other requested (group, tier) gets the constraint that blocked it.
// It makes no synchronous call.
func (s *Service) QuoteDeliveryOffers(ctx context.Context, cmd QuoteCommand) (views.QuoteResult, error) {
	if err := cmd.validate(); err != nil {
		return views.QuoteResult{}, err
	}
	now := s.Clock.Now().UTC()
	pol, err := s.currentPolicy(ctx)
	if err != nil {
		return views.QuoteResult{}, err
	}
	in, err := s.searchInput(ctx, now, pol, cmd.DestinationPostalPrefix, cmd.Lines, cmd.Tiers, searchOptions{})
	if err != nil {
		return views.QuoteResult{}, err
	}
	res := search.Run(in)
	var offers []*offer.Offer
	var events []shared.Event
	out := views.QuoteResult{Offers: []views.Offer{}, Unavailable: []views.Unavailable{}}
	tiers := sortedTiers(res.WholeQuote)
	for _, t := range tiers {
		out.Unavailable = append(out.Unavailable, views.Unavailable{Tier: string(t), Constraint: string(res.WholeQuote[t])})
	}
	for _, g := range res.Groups {
		os, evs, un := s.offersOfGroup(g, cmd.DestinationPostalPrefix, pol, now)
		offers, events, out.Unavailable = append(offers, os...), append(events, evs...), append(out.Unavailable, un...)
	}
	if err := s.tx(ctx, func(ctx context.Context) error {
		for _, o := range offers {
			if err := s.Offers.Save(ctx, o); err != nil {
				return err
			}
		}
		return s.Outbox.Append(ctx, events...)
	}); err != nil {
		return views.QuoteResult{}, err
	}
	for _, o := range offers {
		out.Offers = append(out.Offers, views.FromOffer(o))
	}
	return out, nil
}

func sortedTiers(m map[shared.Tier]search.Constraint) []shared.Tier {
	out := make([]shared.Tier, 0, len(m))
	for t := range m {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b shared.Tier) int { return a.Rank() - b.Rank() })
	return out
}

// offersOfGroup turns the best candidate of every tier of a group into an
// offer; a tier without one, or whose offer would break ErrOfferExpiryTooLate,
// is reported with its constraint.
func (s *Service) offersOfGroup(g search.Group, prefix string, pol *policy.Policy, now time.Time) ([]*offer.Offer, []shared.Event, []views.Unavailable) {
	var offers []*offer.Offer
	var events []shared.Event
	var un []views.Unavailable
	tiers := make([]shared.Tier, 0, len(g.Candidates)+len(g.Unavailable))
	for t := range g.Candidates {
		tiers = append(tiers, t)
	}
	for t := range g.Unavailable {
		tiers = append(tiers, t)
	}
	slices.SortFunc(tiers, func(a, b shared.Tier) int { return a.Rank() - b.Rank() })
	for _, t := range slices.Compact(tiers) {
		cs := g.Candidates[t]
		if len(cs) == 0 {
			un = append(un, views.Unavailable{Tier: string(t), Constraint: string(g.Unavailable[t]), GroupNo: g.No})
			continue
		}
		c := cs[0]
		o, evs, err := offer.New(offer.Quote{
			ID: s.IDs.NewOfferID(), GroupNo: g.No, DestinationPrefix: prefix, Tier: t, Lines: g.Lines, Window: c.Window, Witness: c.Witness,
			Basis: c.Basis, PolicyVersion: pol.VersionName(), TTL: pol.OfferTTL(), ExpiresAt: c.ExpiresAt, LatestMeetableAt: c.LatestMeetableAt, PickupCutoffAt: c.ShipBy,
		}, now)
		if err != nil {
			if errors.Is(err, offer.ErrOfferExpiryTooLate) {
				un = append(un, views.Unavailable{Tier: string(t), Constraint: string(search.DeliverySLAUnavailable), GroupNo: g.No})
			}
			continue
		}
		offers, events = append(offers, o), append(events, evs...)
	}
	return offers, events, un
}
