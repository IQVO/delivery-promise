---
paths:
  - "internal/adapters/**/kafka/**"
  - "internal/adapters/outbound/outbox/**"
  - "apis/asyncapi*"
---

# Cross-service integration events (Kafka)

delivery-promise PUBLISHES twelve events (six integration events to
`retailnetwork.delivery-promise.events`, six analytics-only events to
`retailnetwork.delivery-promise.analytics`) and CONSUMES 34 integration events from
the eight producing contexts (ADR 0003, ADR 0004). Never produce or consume a
`warehouse.*` topic.

## Events: CloudEvents 1.0 is MANDATORY

Every Kafka message this service produces or consumes is a CloudEvents 1.0 event in
structured content mode. This is a hard rule, not a preference:

- No flat envelope (`event_id`/`event_type`/`occurred_at`), no dual-write, no
  envelope toggle env var. `internal/architecture/fitness_test.go`'s
  `TestNoEventEnvelopeToggleOrFlatEnvelope` fails CI on any of them.
- Build and validate the envelope with `github.com/cloudevents/sdk-go/v2/event`
  through ONE helper package, `internal/adapters/kafka/cloudevents` (`New`,
  `Decode`, `ContentTypeHeader`). Transport stays `segmentio/kafka-go`; no
  hand-rolled CloudEvent structs. The only producer-side caller is the encoder
  `internal/adapters/outbound/kafka/encoder.go`.
- Every produced message carries the Kafka header
  `content-type: application/cloudevents+json; charset=UTF-8`. Message key =
  aggregate id, `kafkago.Hash{}` balancer (`internal/adapters/outbound/kafka/writer.go`).
- Attributes: `specversion=1.0`; `id` (UUID v4 minted ONCE per event at outbox
  append and stored in `outbox_events.event_id`, so a redelivery carries the same
  id); `source=/retailnetwork/delivery-promise`; `type`; `subject` (the aggregate
  id: promiseId, offerId, bookId or policyVersion; never empty); `time` (the domain
  occurred-at, UTC); `datacontenttype=application/json`;
  `dataschema=urn:retailnetwork:delivery-promise:<events|analytics>:<EventName>:v1`.
  No custom extension attributes without an ADR.
- `type` = `com.retailnetwork.commerce.delivery-promise.<entity>.<EventName>` with
  entity `promise`, `promisebook`, `deliveryoffer` or `promisepolicy` (lowercase,
  no separators). A breaking payload change is a new `.v2` dataschema, never a
  mutation of v1; evolution is additive only.
- Payloads follow the wire conventions of contracts-delta: ids are strings,
  instants RFC 3339 UTC, durations integers ending in `Minutes`, enums UPPER_SNAKE,
  optional fields omitted, arrays always present.
- Tests: a golden exact-JSON test per published type
  (`internal/adapters/outbound/kafka/encoder_test.go`, files in `testdata/`), the
  catalogue-vs-contract sensor (`TestEventCatalogueMatchesContract`), the handler
  table vs the contract (`internal/adapters/inbound/kafka/dispatch_test.go`
  `TestHandlersAreExactlyTheConsumedMessagesOfTheContract`) and the real-broker
  proof (`cmd/api/e2e_integration_test.go`), all via testcontainers.

### Published types

`subject` and the Kafka key are the aggregate id; `dataschema` is
`urn:retailnetwork:delivery-promise:<stream>:<Event>:v1`.

| `type` | topic | `subject` |
| --- | --- | --- |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseAtRisk` | `retailnetwork.delivery-promise.events` | promiseId |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseRevised` | `retailnetwork.delivery-promise.events` | promiseId |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseKept` | `retailnetwork.delivery-promise.events` | promiseId |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseBroken` | `retailnetwork.delivery-promise.events` | promiseId |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseUnkeepable` | `retailnetwork.delivery-promise.events` | promiseId |
| `com.retailnetwork.commerce.delivery-promise.promisebook.PromiseBookExhausted` | `retailnetwork.delivery-promise.events` | bookId |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseMade` | `retailnetwork.delivery-promise.analytics` | promiseId |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseConfirmed` | `retailnetwork.delivery-promise.analytics` | promiseId |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseReleased` | `retailnetwork.delivery-promise.analytics` | promiseId |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseCancelled` | `retailnetwork.delivery-promise.analytics` | promiseId |
| `com.retailnetwork.commerce.delivery-promise.deliveryoffer.DeliveryOfferQuoted` | `retailnetwork.delivery-promise.analytics` | offerId |
| `com.retailnetwork.commerce.delivery-promise.promisepolicy.PromisePolicyPublished` | `retailnetwork.delivery-promise.analytics` | policyVersion |

### Consumed types

The EXACT `type` strings of the producers' AsyncAPI (re-checked against
`origin/feature/service` of each producer); the dispatch table is
`internal/adapters/inbound/kafka/dispatch.go` `Handlers`.

| `type` | topic | producer |
| --- | --- | --- |
| `com.retailnetwork.logistics.node-topology.node.NodeRegistered` | `retailnetwork.node-topology.events` | node-topology |
| `com.retailnetwork.logistics.node-topology.node.NodeOperationalProfileDeclared` | `retailnetwork.node-topology.events` | node-topology |
| `com.retailnetwork.logistics.node-topology.node.NodeActivated` | `retailnetwork.node-topology.events` | node-topology |
| `com.retailnetwork.logistics.node-topology.node.NodeSuspended` | `retailnetwork.node-topology.events` | node-topology |
| `com.retailnetwork.logistics.node-topology.node.NodeRetired` | `retailnetwork.node-topology.events` | node-topology |
| `com.retailnetwork.logistics.node-topology.lane.LaneOpened` | `retailnetwork.node-topology.events` | node-topology |
| `com.retailnetwork.logistics.node-topology.lane.LaneClosed` | `retailnetwork.node-topology.events` | node-topology |
| `com.retailnetwork.logistics.node-topology.servicearea.ServiceAreaAssigned` | `retailnetwork.node-topology.events` | node-topology |
| `com.retailnetwork.logistics.node-topology.region.RegionDefined` | `retailnetwork.node-topology.events` | node-topology |
| `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemCreated` | `retailnetwork.offer-availability.events` | offer-availability |
| `com.retailnetwork.commerce.offer-availability.catalogitem.CatalogItemDiscontinued` | `retailnetwork.offer-availability.events` | offer-availability |
| `com.retailnetwork.commerce.offer-availability.listing.ListingCreated` | `retailnetwork.offer-availability.events` | offer-availability |
| `com.retailnetwork.commerce.offer-availability.listing.ListingAvailabilityChanged` | `retailnetwork.offer-availability.events` | offer-availability |
| `com.retailnetwork.commerce.offer-availability.listing.ListingSuppressed` | `retailnetwork.offer-availability.events` | offer-availability |
| `com.retailnetwork.commerce.offer-availability.listing.ListingRestored` | `retailnetwork.offer-availability.events` | offer-availability |
| `com.retailnetwork.partner.node-performance.scorecard.NodeScorecardUpdated` | `retailnetwork.node-performance.events` | node-performance |
| `com.retailnetwork.logistics.linehaul-transportation.departure.DepartureScheduled` | `retailnetwork.linehaul-transportation.events` | linehaul-transportation |
| `com.retailnetwork.logistics.linehaul-transportation.departure.DepartureCapacityChanged` | `retailnetwork.linehaul-transportation.events` | linehaul-transportation |
| `com.retailnetwork.logistics.linehaul-transportation.departure.DepartureCancelled` | `retailnetwork.linehaul-transportation.events` | linehaul-transportation |
| `com.retailnetwork.logistics.linehaul-transportation.departure.BookingMoved` | `retailnetwork.linehaul-transportation.events` | linehaul-transportation |
| `com.retailnetwork.logistics.linehaul-transportation.departure.BookingFailed` | `retailnetwork.linehaul-transportation.events` | linehaul-transportation |
| `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveScheduled` | `retailnetwork.last-mile-delivery.events` | last-mile-delivery |
| `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCapacityChanged` | `retailnetwork.last-mile-delivery.events` | last-mile-delivery |
| `com.retailnetwork.logistics.last-mile-delivery.wave.DeliveryWaveCancelled` | `retailnetwork.last-mile-delivery.events` | last-mile-delivery |
| `com.retailnetwork.logistics.last-mile-delivery.packageattempthistory.PackageInductionDeferred` | `retailnetwork.last-mile-delivery.events` | last-mile-delivery |
| `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentLocked` | `retailnetwork.order-sourcing.events` | order-sourcing |
| `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentShipped` | `retailnetwork.order-sourcing.events` | order-sourcing |
| `com.retailnetwork.commerce.order-sourcing.assignment.FulfillmentUnsourceable` | `retailnetwork.order-sourcing.events` | order-sourcing |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageDelivered` | `retailnetwork.shipping-and-tracking.events` | shipping-and-tracking |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageExceptionRaised` | `retailnetwork.shipping-and-tracking.events` | shipping-and-tracking |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageLost` | `retailnetwork.shipping-and-tracking.events` | shipping-and-tracking |
| `com.retailnetwork.logistics.shipping-and-tracking.journey.PackageReturned` | `retailnetwork.shipping-and-tracking.events` | shipping-and-tracking |
| `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderPlaced` | `retailnetwork.customer-ordering.events` | customer-ordering |
| `com.retailnetwork.commerce.customer-ordering.customerorder.CustomerOrderCancelled` | `retailnetwork.customer-ordering.events` | customer-ordering |

## Consuming

`internal/adapters/inbound/kafka` runs ONE consumer over the eight topics: it
decodes with `cloudevents.Decode`, dispatches on the FULL `type`, ignores unknown
types and skips (WARN) anything that is not a valid CloudEvent. Each handler is a
`usecases.Service.On...` method that claims the CloudEvents `id` in
`processed_events` (the inbox) and writes the read model or changes the aggregate in
ONE unit of work, so a redelivery commits nothing. A transient failure is retried
with capped backoff (5 attempts), then the message goes to `<topic>.dlq`; a payload
that can never be applied (`errBadPayload`) goes there at once. Offsets are committed
only after handling.

## Transactional outbox

A use case saves the aggregates and appends the encoded events in ONE Postgres
transaction (`ports.EventOutbox`, `internal/adapters/outbound/postgres/outbox.go`).
The relay (`internal/adapters/outbound/outbox/relay.go`, always on, started by
`cmd/api/workers.go`) drains `outbox_events` in id order, at-least-once. The cap
`OUTBOX_MAX_ATTEMPTS` (default 20, 0 = unlimited) excludes a poison row; re-arm it
with `UPDATE outbox_events SET attempts = 0 WHERE id = ...` (ADR 0005). The first
write to a topic that does not exist yet can fail once with an unknown-topic
error: the relay retries it with the same id. The caller of `NewOutboxRelay` must
pass `WithMaxAttempts(...)` inline (`TestOutboxRelaysPassAttemptCap`).

## Consumer group id

`DELIVERY_PROMISE_CONSUMER_GROUP` is mandatory (a boot error naming it, raised
before the listener binds), one stable group for the eight topics, a chart value and
never a string literal (`TestKafkaConsumerGroupNeverHardcodedInline`). The consumer
resumes from its committed offsets; the read models live in Postgres, so the group
is NOT per process instance.
