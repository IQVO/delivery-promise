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
)

// projectedKinds maps the FULL CloudEvents type of each projected event
// (ADR 0018) to its report.Kind.
func projectedKinds() map[string]report.Kind {
	return map[string]report.Kind{
		cloudevents.Type(entityDeliveryOffer, string(report.KindDeliveryOfferQuoted)): report.KindDeliveryOfferQuoted,
		cloudevents.Type(entityPromise, string(report.KindPromiseMade)):               report.KindPromiseMade,
		cloudevents.Type(entityPromise, string(report.KindPromiseRevised)):            report.KindPromiseRevised,
		cloudevents.Type(entityPromise, string(report.KindPromiseUnkeepable)):         report.KindPromiseUnkeepable,
		cloudevents.Type(entityPromise, string(report.KindPromiseKept)):               report.KindPromiseKept,
		cloudevents.Type(entityPromise, string(report.KindPromiseBroken)):             report.KindPromiseBroken,
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
	RevisionNo        int       `json:"revisionNo"`
	Cause             string    `json:"cause"`
	LateByMinutes     int       `json:"lateByMinutes"`
}

// toEvent decodes and validates a CloudEvent of a projected kind. Every error
// is deterministic: the same bytes can never pass.
func toEvent(kind report.Kind, evt ce.Event) (report.Event, error) {
	var d analyticsData
	if err := evt.DataAs(&d); err != nil {
		return report.Event{}, fmt.Errorf("decode %s %s data: %w", kind, evt.ID(), err)
	}
	subjectOf := d.PromiseID
	if kind == report.KindDeliveryOfferQuoted {
		subjectOf = d.OfferID
	}
	if subjectOf == "" || subjectOf != evt.Subject() {
		return report.Event{}, fmt.Errorf("%s %s: the aggregate id %q does not match the subject %q", kind, evt.ID(), subjectOf, evt.Subject())
	}
	e := report.Event{
		Kind: kind, EventID: evt.ID(), At: evt.Time().UTC(), PromiseID: d.PromiseID, OfferID: d.OfferID, Tier: d.Tier,
		Basis: d.Basis, DeliverByEarliest: d.DeliverByEarliest.UTC(), DeliverByLatest: d.DeliverByLatest.UTC(), TimeZone: d.TimeZone,
		RevisionNo: d.RevisionNo, Cause: d.Cause, LateByMinutes: d.LateByMinutes,
	}
	if err := e.Validate(); err != nil {
		return report.Event{}, err
	}
	return e, nil
}
