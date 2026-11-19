package kafka

import (
	"fmt"
	"time"

	ce "github.com/cloudevents/sdk-go/v2/event"

	"github.com/claudioed/delivery-promise/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// The CloudEvents entity segments of this context's four aggregates.
const (
	entityPromise       = "promise"
	entityDeliveryOffer = "deliveryoffer"
	entityPromiseBook   = "promisebook"
	entityPromisePolicy = "promisepolicy"
	// entityDeliveryRequest is the entity of the request-level fact (ADR 0032).
	entityDeliveryRequest = "deliveryrequest"
)

// projectedKinds maps the FULL CloudEvents type of each projected event
// (ADR 0018) to its report.Kind.
func projectedKinds() map[string]report.Kind {
	return map[string]report.Kind{
		cloudevents.Type(entityDeliveryOffer, string(report.KindDeliveryOfferQuoted)):       report.KindDeliveryOfferQuoted,
		cloudevents.Type(entityPromise, string(report.KindPromiseMade)):                     report.KindPromiseMade,
		cloudevents.Type(entityPromise, string(report.KindPromiseRevised)):                  report.KindPromiseRevised,
		cloudevents.Type(entityPromise, string(report.KindPromiseUnkeepable)):               report.KindPromiseUnkeepable,
		cloudevents.Type(entityPromise, string(report.KindPromiseKept)):                     report.KindPromiseKept,
		cloudevents.Type(entityPromise, string(report.KindPromiseBroken)):                   report.KindPromiseBroken,
		cloudevents.Type(entityDeliveryRequest, string(report.KindDeliveryRequestAnswered)): report.KindDeliveryRequestAnswered,
	}
}

// acknowledgedTypes are this context's other published types: committed past
// without a warning, because no report needs them (ADR 0018).
func acknowledgedTypes() map[string]bool {
	return map[string]bool{
		cloudevents.Type(entityPromise, "PromiseConfirmed"):             true,
		cloudevents.Type(entityPromise, "PromiseAtRisk"):                true,
		cloudevents.Type(entityPromise, "PromiseCancelled"):             true,
		cloudevents.Type(entityPromise, "PromiseReleased"):              true,
		cloudevents.Type(entityPromiseBook, "PromiseBookExhausted"):     true,
		cloudevents.Type(entityPromisePolicy, "PromisePolicyPublished"): true,
	}
}

// analyticsData is the union of the payload fields the projection reads,
// hand-mirrored from apis/asyncapi.yaml (never imported from the encoder).
// Unknown fields are ignored; an absent timestamp stays zero, an absent number
// stays zero and an absent string stays empty.
type analyticsData struct {
	PromiseID         string    `json:"promiseId"`
	OfferID           string    `json:"offerId"`
	Tier              string    `json:"tier"`
	Basis             string    `json:"basis"`
	DeliverByEarliest time.Time `json:"deliverByEarliest"`
	DeliverByLatest   time.Time `json:"deliverByLatest"`
	TimeZone          string    `json:"timeZone"`
	DestinationPrefix string    `json:"destinationPostalPrefix"`
	RevisionNo        int       `json:"revisionNo"`
	Cause             string    `json:"cause"`
	LateByMinutes     int       `json:"lateByMinutes"`

	// DeliveryRequestAnswered (ADR 0032).
	RequestID            string `json:"requestId"`
	DestinationRegion    string `json:"destinationRegion"`
	RequestKind          string `json:"kind"`
	ItemCount            int    `json:"itemCount"`
	Answered             bool   `json:"answered"`
	ShortestPromisedDays *int   `json:"shortestPromisedDays"`
}

// subject is the aggregate id the CloudEvents subject must equal: the offerId
// of an offer, the requestId of a request fact, the promiseId of the rest.
func (d analyticsData) subject(kind report.Kind) string {
	if kind == report.KindDeliveryOfferQuoted {
		return d.OfferID
	}
	if kind == report.KindDeliveryRequestAnswered {
		return d.RequestID
	}
	return d.PromiseID
}

// region is the report region of the fact: the first two characters of an
// offer's destination prefix; a request fact carries the region itself, which
// is never cut here (a region past two characters is poison, not a prefix to
// trim).
func (d analyticsData) region(kind report.Kind) string {
	if kind == report.KindDeliveryRequestAnswered {
		return d.DestinationRegion
	}
	return report.RegionOf(d.DestinationPrefix)
}

// toEvent decodes and validates a CloudEvent of a projected kind. Every error
// is deterministic: the same bytes can never pass.
func toEvent(kind report.Kind, evt ce.Event) (report.Event, error) {
	var d analyticsData
	if err := evt.DataAs(&d); err != nil {
		return report.Event{}, fmt.Errorf("decode %s %s data: %w", kind, evt.ID(), err)
	}
	if subjectOf := d.subject(kind); subjectOf == "" || subjectOf != evt.Subject() {
		return report.Event{}, fmt.Errorf("%s %s: the aggregate id %q does not match the subject %q", kind, evt.ID(), subjectOf, evt.Subject())
	}
	e := report.Event{
		Kind: kind, EventID: evt.ID(), At: evt.Time().UTC(), PromiseID: d.PromiseID, OfferID: d.OfferID, Tier: d.Tier,
		Basis: d.Basis, Region: d.region(kind), DeliverByEarliest: d.DeliverByEarliest.UTC(), DeliverByLatest: d.DeliverByLatest.UTC(), TimeZone: d.TimeZone,
		RevisionNo: d.RevisionNo, Cause: d.Cause, LateByMinutes: d.LateByMinutes,
		RequestID: d.RequestID, RequestKind: d.RequestKind, Items: d.ItemCount, Answered: d.Answered, ShortestDays: d.ShortestPromisedDays,
	}
	if err := e.Validate(); err != nil {
		return report.Event{}, err
	}
	return e, nil
}
