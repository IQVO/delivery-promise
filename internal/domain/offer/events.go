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
	PolicyVersion     string
	ExpiresAt         time.Time
}

// EventName returns "DeliveryOfferQuoted".
func (DeliveryOfferQuoted) EventName() string { return "DeliveryOfferQuoted" }

// Entity returns "deliveryoffer".
func (DeliveryOfferQuoted) Entity() string { return "deliveryoffer" }

// SubjectID returns the offerId.
func (e DeliveryOfferQuoted) SubjectID() string { return string(e.OfferID) }

var _ shared.Event = DeliveryOfferQuoted{}
