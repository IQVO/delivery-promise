package offer

import (
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// DeliveryOfferQuoted is raised by New, one per offer (analytics only).
type DeliveryOfferQuoted struct {
	shared.Header
	OfferID           ID
	Tier              shared.Tier
	DestinationPrefix string
	NodeID            string
	Window            shared.Window
	Basis             shared.Basis
	Confidence        shared.Confidence
	PolicyVersion     string
	ExpiresAt         time.Time
	// CapacityBand is the pressure on the book of the offer's window at quote
	// time (ADR 0023); empty when unknown.
	CapacityBand shared.CapacityBand
	// ConsolidationEligible is set only for the tier CONSOLIDATED (ADR 0024).
	ConsolidationEligible *bool
}

// EventName returns "DeliveryOfferQuoted".
func (DeliveryOfferQuoted) EventName() string { return "DeliveryOfferQuoted" }

// Entity returns "deliveryoffer".
func (DeliveryOfferQuoted) Entity() string { return "deliveryoffer" }

// SubjectID returns the offerId.
func (e DeliveryOfferQuoted) SubjectID() string { return string(e.OfferID) }

var _ shared.Event = DeliveryOfferQuoted{}
