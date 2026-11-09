package views

import "time"

// EstimateWindow is the delivery window of an estimate option: start and end
// are the instants a quote's offer calls window.earliest and window.latest,
// timeZone the IANA zone of the delivery station whose calendar the days
// belong to (ADR 0011). A computed window always names its zone.
type EstimateWindow struct {
	Start    time.Time `json:"start"`
	End      time.Time `json:"end"`
	TimeZone string    `json:"timeZone"`
}

// EstimateOption is the best option of one tier for one item: the same tier,
// window, basis and orderByAt a quote's best offer of that tier carries
// (ADR 0019). OrderByAt is omitted when unknown.
type EstimateOption struct {
	Tier   string         `json:"tier"`
	Window EstimateWindow `json:"window"`
	Basis  string         `json:"basis"`
	// Confidence is the calibrated on-time probability of the option, omitted
	// when unknown (ADR 0020).
	Confidence *float64   `json:"confidence,omitempty"`
	OrderByAt  *time.Time `json:"orderByAt,omitempty"`
}

// EstimateConstraint says why a tier has no option, in the quote's
// OfferConstraint vocabulary. Tier is omitted when the constraint applies to
// the item on every tier alike (DESTINATION_NOT_SERVED,
// ITEM_QUANTITY_NOT_AVAILABLE).
type EstimateConstraint struct {
	Tier string `json:"tier,omitempty"`
	Code string `json:"code"`
}

// Estimate is the advisory answer for one item of the request.
type Estimate struct {
	CatalogItemID string               `json:"catalogItemId"`
	Quantity      int                  `json:"quantity"`
	Options       []EstimateOption     `json:"options"`
	Constraints   []EstimateConstraint `json:"constraints"`
}

// EstimateResult is the answer of EstimateDeliveries: one estimate per
// requested item, in request order. Nothing behind it was stored.
type EstimateResult struct {
	ComputedAt              time.Time  `json:"computedAt"`
	DestinationPostalPrefix string     `json:"destinationPostalPrefix"`
	Estimates               []Estimate `json:"estimates"`
}
