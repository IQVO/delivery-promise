package kafka

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/claudioed/delivery-promise/internal/adapters/kafka/cloudevents"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/outbox"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// Topics the context publishes to (AsyncAPI channels).
const (
	TopicEvents    = "retailnetwork.delivery-promise.events"
	TopicAnalytics = "retailnetwork.delivery-promise.analytics"
)

// SchemaVersion is the dataschema version of every payload below.
const SchemaVersion = 1

// streamOf is the stream (and so the topic) of each published event name (ADR 0004).
var streamOf = map[string]string{
	"PromiseMade": cloudevents.StreamAnalytics, "PromiseConfirmed": cloudevents.StreamAnalytics,
	"PromiseReleased": cloudevents.StreamAnalytics, "PromiseCancelled": cloudevents.StreamAnalytics,
	"DeliveryOfferQuoted": cloudevents.StreamAnalytics, "PromisePolicyPublished": cloudevents.StreamAnalytics,
	"PromiseAtRisk": cloudevents.StreamEvents, "PromiseRevised": cloudevents.StreamEvents,
	"PromiseKept": cloudevents.StreamEvents, "PromiseBroken": cloudevents.StreamEvents,
	"PromiseUnkeepable": cloudevents.StreamEvents, "PromiseBookExhausted": cloudevents.StreamEvents,
}

// Encoder turns domain events into outbox messages: one CloudEvents 1.0
// structured-mode JSON per event, with a fresh UUID minted here, once.
type Encoder struct {
	newID func() string
}

// NewEncoder returns an Encoder minting random UUIDs.
func NewEncoder() *Encoder { return &Encoder{newID: uuid.NewString} }

// NewEncoderWithIDs returns an Encoder minting ids with newID (tests).
func NewEncoderWithIDs(newID func() string) *Encoder { return &Encoder{newID: newID} }

// Encode builds the outbox message of e.
func (en *Encoder) Encode(e shared.Event) (outbox.Message, error) {
	stream, ok := streamOf[e.EventName()]
	if !ok {
		return outbox.Message{}, fmt.Errorf("kafka encoder: %s is not a published event", e.EventName())
	}
	data, err := payload(e)
	if err != nil {
		return outbox.Message{}, err
	}
	id := en.newID()
	value, err := cloudevents.New(cloudevents.Spec{
		ID: id, Entity: e.Entity(), EventName: e.EventName(), Subject: e.SubjectID(), Time: e.OccurredAt(),
		Stream: stream, Version: SchemaVersion, Data: data,
	})
	if err != nil {
		return outbox.Message{}, err
	}
	topic := TopicEvents
	if stream == cloudevents.StreamAnalytics {
		topic = TopicAnalytics
	}
	ct := cloudevents.ContentTypeHeader()
	return outbox.Message{
		EventID: id, Topic: topic, EventType: cloudevents.Type(e.Entity(), e.EventName()), Subject: e.SubjectID(),
		Key: []byte(e.SubjectID()), DataSchema: cloudevents.DataSchema(stream, e.EventName(), SchemaVersion), Value: value,
		Headers: []outbox.Header{{Key: ct.Key, Value: string(ct.Value)}},
	}, nil
}

// EncodeAll encodes events in order.
func (en *Encoder) EncodeAll(events []shared.Event) ([]outbox.Message, error) {
	out := make([]outbox.Message, 0, len(events))
	for _, e := range events {
		m, err := en.Encode(e)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

type groupLine struct {
	CatalogItemID string `json:"catalogItemId"`
	Quantity      int    `json:"quantity"`
}

func groupLines(ls []shared.Line) []groupLine {
	out := make([]groupLine, len(ls))
	for i, l := range ls {
		out[i] = groupLine{CatalogItemID: l.CatalogItemID, Quantity: l.Quantity}
	}
	return out
}

type orderDeadline struct {
	Tier      string `json:"tier"`
	RegionID  string `json:"regionId,omitempty"`
	LocalTime string `json:"localTime"`
}

func utc(t time.Time) time.Time { return t.UTC() }

// payload maps a domain event to its AsyncAPI `data` object.
func payload(e shared.Event) (any, error) {
	switch ev := e.(type) {
	case promise.PromiseMade:
		return madeData(ev), nil
	case promise.PromiseConfirmed:
		return map[string]any{"promiseId": ev.PromiseID, "orderId": ev.OrderID, "groupNo": ev.GroupNo}, nil
	case promise.PromiseAtRisk:
		return map[string]any{"promiseId": ev.PromiseID, "orderId": ev.OrderID, "reason": ev.Reason, "detectedAt": utc(ev.DetectedAt)}, nil
	case promise.PromiseRevised:
		return map[string]any{
			"promiseId": ev.PromiseID, "orderId": ev.OrderID, "revisionNo": ev.RevisionNo,
			"previousDeliverByLatest": utc(ev.PreviousDeliverByLatest), "deliverByEarliest": utc(ev.Terms.Window.Earliest),
			"deliverByLatest": utc(ev.Terms.Window.Latest), "shipBy": utc(ev.Terms.ShipBy), "nodeId": ev.NodeID, "reason": ev.Reason,
		}, nil
	case promise.PromiseKept:
		return map[string]any{
			"promiseId": ev.PromiseID, "orderId": ev.OrderID, "nodeId": ev.NodeID,
			"deliveredAt": utc(ev.DeliveredAt), "deliverByLatest": utc(ev.DeliverByLatest),
		}, nil
	case promise.PromiseBroken:
		return brokenData(ev), nil
	case promise.PromiseReleased:
		return map[string]any{"promiseId": ev.PromiseID, "reason": ev.Reason}, nil
	case promise.PromiseCancelled:
		return map[string]any{"promiseId": ev.PromiseID, "orderId": ev.OrderID, "reason": ev.Reason}, nil
	case promise.PromiseUnkeepable:
		return map[string]any{"promiseId": ev.PromiseID, "orderId": ev.OrderID, "groupNo": ev.GroupNo, "reason": ev.Reason}, nil
	case book.PromiseBookExhausted:
		return map[string]any{"bookId": ev.BookID, "nodeId": ev.NodeID, "pickupDepartureId": ev.PickupDepartureID, "share": ev.Share}, nil
	case offer.DeliveryOfferQuoted:
		return map[string]any{
			"offerId": ev.OfferID, "tier": ev.Tier, "destinationPostalPrefix": ev.DestinationPrefix, "nodeId": ev.NodeID,
			"deliverByEarliest": utc(ev.Window.Earliest), "deliverByLatest": utc(ev.Window.Latest), "basis": ev.Basis,
			"policyVersion": ev.PolicyVersion, "expiresAt": utc(ev.ExpiresAt),
		}, nil
	case policy.PromisePolicyPublished:
		return policyData(ev), nil
	}
	return nil, fmt.Errorf("kafka encoder: no payload mapping for %T", e)
}

func madeData(ev promise.PromiseMade) map[string]any {
	d := map[string]any{
		"promiseId": ev.PromiseID, "offerId": ev.OfferID, "tier": ev.Terms.Tier, "nodeId": ev.Witness.NodeID,
		"shipBy": utc(ev.Terms.ShipBy), "deliverByEarliest": utc(ev.Terms.Window.Earliest), "deliverByLatest": utc(ev.Terms.Window.Latest),
		"basis": ev.Terms.Basis, "policyVersion": ev.Terms.PolicyVersion, "holdExpiresAt": utc(ev.HoldExpiresAt), "lines": groupLines(ev.Lines),
	}
	if ev.Witness.PickupDepartureID != "" { // a NOMINAL promise has none (contracts-delta E8)
		d["pickupDepartureId"] = ev.Witness.PickupDepartureID
	}
	return d
}

func brokenData(ev promise.PromiseBroken) map[string]any {
	d := map[string]any{
		"promiseId": ev.PromiseID, "orderId": ev.OrderID, "nodeId": ev.NodeID, "deliverByLatest": utc(ev.DeliverByLatest),
		"lateByMinutes": ev.LateByMinutes, "cause": ev.Cause,
	}
	if !ev.DeliveredAt.IsZero() { // a lost or returned package was never delivered
		d["deliveredAt"] = utc(ev.DeliveredAt)
	}
	return d
}

func policyData(ev policy.PromisePolicyPublished) map[string]any {
	ds := make([]orderDeadline, len(ev.Deadlines))
	for i, d := range ev.Deadlines {
		ds[i] = orderDeadline{Tier: string(d.Tier), RegionID: d.RegionID, LocalTime: fmt.Sprintf("%02d:%02d", d.Minute/60, d.Minute%60)}
	}
	return map[string]any{
		"policyVersion": ev.PolicyVersion, "version": ev.Number, "offerTtlMinutes": ev.OfferTTLMinutes, "holdTtlMinutes": ev.HoldTTLMinutes,
		"packagesPerOrder": ev.PackagesPerOrder, "capacitySharePercent": ev.SharePercent, "orderDeadlines": ds, "publishedAt": utc(ev.PublishedAt),
	}
}
