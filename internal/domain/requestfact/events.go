package requestfact

import "github.com/claudioed/delivery-promise/internal/domain/shared"

// DeliveryRequestAnswered is the analytics-only event of one answered delivery
// request (ADR 0032): one per quote or estimate the service answered, whether
// or not a promise came back. It carries no address and no shopper id: the
// region is the first two characters of the destination postal prefix.
type DeliveryRequestAnswered struct {
	shared.Header
	// RequestID is a fresh opaque id of the fact: the CloudEvents subject.
	RequestID string
	Region    string
	Kind      Kind
	Items     int
	Answered  bool
	// ShortestPromisedDays is nil when no promise was answered.
	ShortestPromisedDays *int
}

// EventName returns "DeliveryRequestAnswered".
func (DeliveryRequestAnswered) EventName() string { return "DeliveryRequestAnswered" }

// Entity returns "deliveryrequest".
func (DeliveryRequestAnswered) Entity() string { return "deliveryrequest" }

// SubjectID returns the requestId.
func (e DeliveryRequestAnswered) SubjectID() string { return e.RequestID }

var _ shared.Event = DeliveryRequestAnswered{}

// Event is the DeliveryRequestAnswered of the fact under the given request id;
// its instant is the instant the request was answered.
func (f Fact) Event(requestID string) DeliveryRequestAnswered {
	return DeliveryRequestAnswered{
		Header:    shared.Header{Version: 1, At: f.At.UTC()},
		RequestID: requestID, Region: f.Region, Kind: f.Kind, Items: f.Items,
		Answered: f.Answered, ShortestPromisedDays: f.ShortestDays,
	}
}
