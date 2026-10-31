---
id: events
title: Event catalogue
sidebar_label: Events
description: "Every CloudEvents type Delivery Promise publishes and consumes, with topics, keys, payload fields and the contract tests that pin them."
---

# Event catalogue

The contract is `apis/asyncapi.yaml` (AsyncAPI 3.0.0, document version 1.1.0). `TestEventCatalogueMatchesContract` (`internal/architecture/catalogue_fitness_test.go`) ties the types in code to it. Every message is a **CloudEvents 1.0** event in structured content mode, built only by `internal/adapters/kafka/cloudevents` (`TestCloudEventsOnly`). Field-level detail and the model behind each event are on [Domain events](/docs/ddd/domain-events); how they flow is in [Integration](/docs/ecosystem/integration).

## Envelope

| Attribute | Value |
| --- | --- |
| `specversion` | `1.0` |
| `id` | minted once when the outbox row is written; a redelivery carries the same `id` |
| `source` | `/retailnetwork/delivery-promise` |
| `type` | `com.retailnetwork.commerce.delivery-promise.<entity>.<EventName>`, entity `promise`, `promisebook`, `deliveryoffer` or `promisepolicy` |
| `subject` and Kafka key | the aggregate root id (`promiseId`; `bookId` for the book; `offerId`; the `policyVersion`) |
| `datacontenttype` | `application/json` |
| `dataschema` | `urn:retailnetwork:delivery-promise:<events\|analytics>:<EventName>:v1` |
| headers | W3C `traceparent` and `tracestate` of the request that wrote the outbox row |

Delivery is **at-least-once**; consumers deduplicate on `id`.

## Published

Topics: integration events on `retailnetwork.delivery-promise.events`, analytics-only events on `retailnetwork.delivery-promise.analytics` (`internal/adapters/outbound/kafka/encoder.go`). `type` below is shortened to `<entity>.<EventName>` after the common prefix.

| Event | Entity | Topic | Raised by | Key data fields |
| --- | --- | --- | --- | --- |
| `PromiseAtRisk` | `promise` | events | flag at risk | `promiseId`, `orderId`, `reason` (`NODE_UNSOURCEABLE`, `PACKAGE_EXCEPTION`, `INDUCTION_DEFERRED`, `DEPARTURE_CANCELLED`, `WAVE_CANCELLED`, `BOOKING_FAILED`, `BOOKING_MOVED`), `detectedAt` |
| `PromiseRevised` | `promise` | events | `ReviseAtRisk` | `promiseId`, `orderId`, `revisionNo`, `previousDeliverByLatest`, `deliverByEarliest`, `deliverByLatest`, `timeZone`, `shipBy`, `nodeId`, `reason` |
| `PromiseKept` | `promise` | events | `JudgePromise` | `promiseId`, `orderId`, `nodeId`, `deliveredAt`, `deliverByLatest` |
| `PromiseBroken` | `promise` | events | `JudgePromise` | as kept, plus `lateByMinutes` and `cause` (`LATE`, `LOST`, `RETURNED`); `deliveredAt` omitted for `LOST` and `RETURNED` |
| `PromiseUnkeepable` | `promise` | events | `ReviseAtRisk` finds no path at any tier | `promiseId`, `orderId`, `groupNo`, `reason`; at most once per promise |
| `PromiseBookExhausted` | `promisebook` | events | the hold that fills the book | `bookId`, `nodeId`, `pickupDepartureId`, `share` |
| `PromiseMade` | `promise` | analytics | `MakePromise` | `promiseId`, `offerId`, `tier`, `nodeId`, `pickupDepartureId`, `shipBy`, `deliverByEarliest`, `deliverByLatest`, `timeZone`, `basis`, `policyVersion`, `holdExpiresAt`, `lines[]` |
| `PromiseConfirmed` | `promise` | analytics | `ConfirmPromise` | `promiseId`, `orderId`, `groupNo` |
| `PromiseReleased` | `promise` | analytics | `ReleaseExpiredHolds` | `promiseId`, `reason` (`HOLD_EXPIRED`) |
| `PromiseCancelled` | `promise` | analytics | `CancelPromise` | `promiseId`, `orderId`, `reason` (the order's reason, or `CANCELLED_ON_REQUEST` through REST) |
| `DeliveryOfferQuoted` | `deliveryoffer` | analytics | `QuoteDeliveryOffers`, one per offer | `offerId`, `tier`, `destinationPostalPrefix`, `nodeId`, `deliverByEarliest`, `deliverByLatest`, `timeZone`, `basis`, `policyVersion`, `expiresAt` |
| `PromisePolicyPublished` | `promisepolicy` | analytics | `PublishPromisePolicy` | `policyVersion`, `version`, `offerTtlMinutes`, `holdTtlMinutes`, `packagesPerOrder`, `capacitySharePercent`, `orderDeadlines[]`, `publishedAt` |

That is twelve types: six integration, six analytics-only. Consumers of the events topic that exist in the sibling repositories (read 2026-10-31): customer-ordering (`PromiseAtRisk`, `PromiseRevised`, `PromiseKept`, `PromiseBroken`, `PromiseUnkeepable`), order-sourcing (`PromiseRevised`, `PromiseBookExhausted`), node-performance (`PromiseKept`, `PromiseBroken`). Nothing consumes the analytics topic.

The AsyncAPI examples for each message carry the full envelope; the payload fields above are copied from them. A real published row can be inspected in `outbox_events` (`event_type`, `topic`, `attempts`, `published_at`); in the quickstart run the policy event went out on the first attempt on `retailnetwork.delivery-promise.analytics`.

### Ordering and partition

The Kafka key is the `subject`, so all events of one promise share a partition and keep their order. The relay sends one message at a time in `outbox_events.id` order with `RequireAll` acks, so a row that cannot be sent is retried (up to `OUTBOX_MAX_ATTEMPTS`) while later rows overtake it only once it is parked.

## Consumed

Dispatch is on the full `type`; payloads are decoded tolerantly (unknown fields ignored). 34 types from eight producer topics, each handled idempotently on the CloudEvents `id`. The full type is `com.retailnetwork.<tier>.<producer>.<entity>.<Event>`; `Handlers` in `internal/adapters/inbound/kafka/dispatch.go` is the registry.

| Topic | Tier and entity | Events | Effect |
| --- | --- | --- | --- |
| `retailnetwork.customer-ordering.events` | `commerce.customer-ordering.customerorder` | `CustomerOrderPlaced`, `CustomerOrderCancelled` | reconciling confirm; cancel |
| `retailnetwork.order-sourcing.events` | `commerce.order-sourcing.assignment` | `FulfillmentLocked`, `FulfillmentShipped`, `FulfillmentUnsourceable` | assignment facts; at risk |
| `retailnetwork.shipping-and-tracking.events` | `logistics.shipping-and-tracking.journey` | `PackageDelivered`, `PackageExceptionRaised`, `PackageLost`, `PackageReturned` | judge; at risk |
| `retailnetwork.linehaul-transportation.events` | `logistics.linehaul-transportation.departure` | `DepartureScheduled`, `DepartureCapacityChanged`, `DepartureCancelled`, `BookingMoved`, `BookingFailed` | `rm_departures`; at risk |
| `retailnetwork.last-mile-delivery.events` | `logistics.last-mile-delivery.wave` (and `packageattempthistory` for `PackageInductionDeferred`) | `DeliveryWaveScheduled`, `DeliveryWaveCapacityChanged`, `DeliveryWaveCancelled`, `PackageInductionDeferred` | `rm_waves`; at risk |
| `retailnetwork.node-performance.events` | `partner.node-performance.scorecard` | `NodeScorecardUpdated` | `rm_scorecards` |
| `retailnetwork.node-topology.events` | `logistics.node-topology.node`, `.lane`, `.servicearea`, `.region` | `NodeRegistered`, `NodeOperationalProfileDeclared`, `NodeActivated`, `NodeSuspended`, `NodeRetired`, `LaneOpened`, `LaneClosed`, `ServiceAreaAssigned`, `RegionDefined` | `rm_nodes`, `rm_lanes`, `rm_areas`, `rm_regions` |
| `retailnetwork.offer-availability.events` | `commerce.offer-availability.catalogitem`, `.listing` | `CatalogItemCreated`, `CatalogItemDiscontinued`, `ListingCreated`, `ListingAvailabilityChanged`, `ListingSuppressed`, `ListingRestored` | `rm_items`, `rm_listings` |

What the handlers require (events without these ids are dead-lettered as malformed): `decodeData` checks the identifying fields per type, for example `NodeRegistered` needs `nodeId`, `ListingCreated` needs `listingId`, `catalogItemId` and `nodeId`, `FulfillmentLocked` needs `assignmentId` and `promiseId`, `CustomerOrderPlaced` needs `orderId`.

### Dead letters

`<topic>.dlq` (`DLQSuffix`) carries the original key, value and headers plus `x-dlq-source-topic`, `x-dlq-source-partition`, `x-dlq-source-offset`, `x-dlq-error` and `x-dlq-failed-at`. Reasons: five failed attempts at a transient error, or a payload that can never be applied. See the [Runbook](/docs/operations/runbook#kafka-topics-and-consumer-groups).

## Changing an event

A new event needs, in the **same commit**: the domain struct, `apis/asyncapi.yaml`, the ADR 0004 `type:` line, the encoder payload and a golden test (`.claude/skills/how-to-add-an-integration-event`). Re-read the producer's AsyncAPI before changing a consumed shape (ADR 0003).
