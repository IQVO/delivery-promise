package http

import (
	"strconv"
	"strings"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

type lineDTO struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

func toLines(in []lineDTO) []shared.Line {
	out := make([]shared.Line, len(in))
	for i, l := range in {
		out[i] = shared.Line{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity}
	}
	return out
}

type quoteRequest struct {
	DestinationPostalPrefix string    `json:"destinationPostalPrefix"`
	Lines                   []lineDTO `json:"lines"`
	Tiers                   []string  `json:"tiers"`
}

func (q quoteRequest) command() usecases.QuoteCommand {
	tiers := make([]shared.Tier, len(q.Tiers))
	for i, t := range q.Tiers {
		tiers[i] = shared.Tier(t)
	}
	return usecases.QuoteCommand{DestinationPostalPrefix: q.DestinationPostalPrefix, Lines: toLines(q.Lines), Tiers: tiers}
}

type makePromiseRequest struct {
	OfferID string    `json:"offerId"`
	Lines   []lineDTO `json:"lines"`
}

func (m makePromiseRequest) command() usecases.MakePromiseCommand {
	return usecases.MakePromiseCommand{OfferID: m.OfferID, Lines: toLines(m.Lines)}
}

type confirmRequest struct {
	OrderID string `json:"orderId"`
	GroupNo int    `json:"groupNo"`
}

type orderDeadlineDTO struct {
	Tier      string `json:"tier"`
	RegionID  string `json:"regionId"`
	LocalTime string `json:"localTime"`
}

type publishPolicyRequest struct {
	OfferTTLMinutes      int                `json:"offerTtlMinutes"`
	HoldTTLMinutes       int                `json:"holdTtlMinutes"`
	PackagesPerOrder     int                `json:"packagesPerOrder"`
	CapacitySharePercent int                `json:"capacitySharePercent"`
	OrderDeadlines       []orderDeadlineDTO `json:"orderDeadlines"`
}

// input maps the request to the domain input; an omitted value takes the policy default.
func (p publishPolicyRequest) input() (policy.Input, error) {
	def := policy.Default()
	in := policy.Input{
		OfferTTLMinutes: orDefault(p.OfferTTLMinutes, int(def.OfferTTL().Minutes())), HoldTTLMinutes: orDefault(p.HoldTTLMinutes, int(def.HoldTTL().Minutes())),
		PackagesPerOrder: orDefault(p.PackagesPerOrder, def.PackagesPerOrder()), SharePercent: orDefault(p.CapacitySharePercent, def.SharePercent()),
	}
	for _, d := range p.OrderDeadlines {
		minute, err := parseLocalTime(d.LocalTime)
		if err != nil {
			return policy.Input{}, err
		}
		in.Deadlines = append(in.Deadlines, policy.Deadline{Tier: shared.Tier(d.Tier), RegionID: d.RegionID, Minute: minute})
	}
	return in, nil
}

func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// parseLocalTime reads "HH:mm" into minutes since midnight.
func parseLocalTime(s string) (int, error) {
	h, m, ok := strings.Cut(s, ":")
	hh, herr := strconv.Atoi(h)
	mm, merr := strconv.Atoi(m)
	if !ok || len(h) != 2 || len(m) != 2 || herr != nil || merr != nil || hh < 0 || hh > 23 || mm < 0 || mm > 59 {
		return 0, shared.Invalid("localTime %q is not HH:mm", s)
	}
	return hh*60 + mm, nil
}
