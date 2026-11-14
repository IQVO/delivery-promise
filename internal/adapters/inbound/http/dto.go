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
	DestinationPostalPrefix string `json:"destinationPostalPrefix"`
	// PickupPointID is the alternative destination (ADR 0027).
	PickupPointID string    `json:"pickupPointId"`
	Lines         []lineDTO `json:"lines"`
	Tiers         []string  `json:"tiers"`
	// ConsolidatedDeliveryDay is the optional local date chosen for the
	// CONSOLIDATED tier (YYYY-MM-DD, ADR 0024).
	ConsolidatedDeliveryDay string `json:"consolidatedDeliveryDay"`
}

func toTiers(in []string) []shared.Tier {
	tiers := make([]shared.Tier, len(in))
	for i, t := range in {
		tiers[i] = shared.Tier(t)
	}
	return tiers
}

func (q quoteRequest) command() usecases.QuoteCommand {
	return usecases.QuoteCommand{DestinationPostalPrefix: q.DestinationPostalPrefix, PickupPointID: q.PickupPointID, Lines: toLines(q.Lines), Tiers: toTiers(q.Tiers), ConsolidatedDeliveryDay: q.ConsolidatedDeliveryDay}
}

// estimateRequest is the body of POST /delivery-estimates (ADR 0019).
type estimateRequest struct {
	DestinationPostalPrefix string `json:"destinationPostalPrefix"`
	// PickupPointID is the alternative destination (ADR 0027).
	PickupPointID string    `json:"pickupPointId"`
	Items         []lineDTO `json:"items"`
	Tiers         []string  `json:"tiers"`
}

func (e estimateRequest) command() usecases.EstimateCommand {
	return usecases.EstimateCommand{DestinationPostalPrefix: e.DestinationPostalPrefix, PickupPointID: e.PickupPointID, Items: toLines(e.Items), Tiers: toTiers(e.Tiers)}
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
	OfferTTLMinutes            int `json:"offerTtlMinutes"`
	HoldTTLMinutes             int `json:"holdTtlMinutes"`
	PackagesPerOrder           int `json:"packagesPerOrder"`
	CapacitySharePercent       int `json:"capacitySharePercent"`
	TransitWindowDays          int `json:"transitWindowDays"`
	TransitServiceLevelPercent int `json:"transitServiceLevelPercent"`
	MinTransitObservations     int `json:"minTransitObservations"`
	// The calibration fields (ADR 0020). maxCalibrationBufferMinutes is a
	// pointer because 0 is a valid cap: only an absent value takes the default.
	TargetOnTimeProbabilityPercent int  `json:"targetOnTimeProbabilityPercent"`
	MinCalibrationObservations     int  `json:"minCalibrationObservations"`
	CalibrationWindowDays          int  `json:"calibrationWindowDays"`
	MaxCalibrationBufferMinutes    *int `json:"maxCalibrationBufferMinutes"`
	// CarrierWindowHours is the width of a carrier-area promise's window
	// (ADR 0026): a pointer because only an absent value takes the default 24.
	CarrierWindowHours *int               `json:"carrierWindowHours"`
	OrderDeadlines     []orderDeadlineDTO `json:"orderDeadlines"`
}

// input maps the request to the domain input; an omitted value takes the policy default.
func (p publishPolicyRequest) input() (policy.Input, error) {
	def := policy.Default()
	in := policy.Input{
		OfferTTLMinutes: orDefault(p.OfferTTLMinutes, int(def.OfferTTL().Minutes())), HoldTTLMinutes: orDefault(p.HoldTTLMinutes, int(def.HoldTTL().Minutes())),
		PackagesPerOrder: orDefault(p.PackagesPerOrder, def.PackagesPerOrder()), SharePercent: orDefault(p.CapacitySharePercent, def.SharePercent()),
		TransitWindowDays: orDefault(p.TransitWindowDays, def.TransitWindowDays()), TransitServiceLevelPercent: orDefault(p.TransitServiceLevelPercent, def.TransitServiceLevelPercent()),
		MinTransitObservations:         orDefault(p.MinTransitObservations, def.MinTransitObservations()),
		TargetOnTimeProbabilityPercent: orDefault(p.TargetOnTimeProbabilityPercent, def.TargetOnTimeProbabilityPercent()),
		MinCalibrationObservations:     orDefault(p.MinCalibrationObservations, def.MinCalibrationObservations()),
		CalibrationWindowDays:          orDefault(p.CalibrationWindowDays, def.CalibrationWindowDays()),
		MaxCalibrationBufferMinutes:    p.MaxCalibrationBufferMinutes,
		CarrierWindowHours:             p.CarrierWindowHours,
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
