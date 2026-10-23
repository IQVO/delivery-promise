# ADR 0004: CloudEvents envelope and type catalogue

## Status

Accepted (2026-10-23).

## Context

Every Kafka message in the retail network is a CloudEvents 1.0 event in structured mode (retail-network-docs `model/model.yaml` `conventions.cloudevents`; the repo rule `.claude/rules/fleet/cloudevents.md`). This ADR is the catalogue of every type delivery-promise publishes. `TestEventCatalogueMatchesContract` checks it against `apis/asyncapi.yaml`. The types delivery-promise only consumes are listed in ADR 0003, not here.

## Decision

### Envelope

- `specversion` `1.0`; `id` a UUID persisted with the outbox row (stable across relay retries; consumers dedupe on it); `source` `/retailnetwork/delivery-promise`; `subject` the aggregate root id (`promiseId`, `bookId`, `offerId` or `policyVersion`); `time` the instant the change was committed, in UTC; `datacontenttype` `application/json`; `dataschema` `urn:retailnetwork:delivery-promise:<stream>:<EventName>:v1` where `<stream>` is `events` or `analytics`.
- Kafka value = the JSON event format; header `content-type: application/cloudevents+json; charset=UTF-8`; Kafka key = the subject, so all events of one aggregate stay ordered on one partition.
- `data` is camelCase; optional fields are omitted when unset (never null); arrays are always present; timestamps are RFC 3339 UTC.
- Built and decoded only through `internal/adapters/kafka/cloudevents`.
- `<entity>` is the raising aggregate, lowercase, no separators: `promise`, `promisebook`, `deliveryoffer`, `promisepolicy`. Tier is `commerce`.

### Published types

Integration events (`stream: events`) go ONLY to `retailnetwork.delivery-promise.events`; analytics-only events (`stream: analytics`) go ONLY to `retailnetwork.delivery-promise.analytics`.

```text
type: com.retailnetwork.commerce.delivery-promise.promise.PromiseMade stream: analytics
type: com.retailnetwork.commerce.delivery-promise.promise.PromiseConfirmed stream: analytics
type: com.retailnetwork.commerce.delivery-promise.promise.PromiseAtRisk stream: events
type: com.retailnetwork.commerce.delivery-promise.promise.PromiseRevised stream: events
type: com.retailnetwork.commerce.delivery-promise.promise.PromiseKept stream: events
type: com.retailnetwork.commerce.delivery-promise.promise.PromiseBroken stream: events
type: com.retailnetwork.commerce.delivery-promise.promise.PromiseReleased stream: analytics
type: com.retailnetwork.commerce.delivery-promise.promise.PromiseCancelled stream: analytics
type: com.retailnetwork.commerce.delivery-promise.promise.PromiseUnkeepable stream: events
type: com.retailnetwork.commerce.delivery-promise.promisebook.PromiseBookExhausted stream: events
type: com.retailnetwork.commerce.delivery-promise.deliveryoffer.DeliveryOfferQuoted stream: analytics
type: com.retailnetwork.commerce.delivery-promise.promisepolicy.PromisePolicyPublished stream: analytics
```

| Event | Stream | Subject / key | Raised when | Consumers |
|---|---|---|---|---|
| `PromiseMade` | analytics | `promiseId` | MakePromise is saved (PROVISIONAL, capacity held) | analytics only |
| `PromiseConfirmed` | analytics | `promiseId` | ConfirmPromise is saved (the synchronous confirm of E7, or the reconciling consumer) | analytics only |
| `PromiseAtRisk` | events | `promiseId` | FlagPromiseAtRisk is saved | customer-ordering |
| `PromiseRevised` | events | `promiseId` | RevisePromise is saved | customer-ordering, order-sourcing |
| `PromiseKept` | events | `promiseId` | JudgePromise: delivered on or before `deliverByLatest` | node-performance, customer-ordering |
| `PromiseBroken` | events | `promiseId` | JudgePromise: late, lost or returned | node-performance, customer-ordering |
| `PromiseReleased` | analytics | `promiseId` | ReleaseExpiredHolds released a hold | analytics only |
| `PromiseCancelled` | analytics | `promiseId` | CancelPromise on CustomerOrderCancelled | analytics only |
| `PromiseUnkeepable` | events | `promiseId` | RevisePromise found no path at any tier (E9) | customer-ordering |
| `PromiseBookExhausted` | events | `bookId` | a hold made `provisional + confirmed = share` | order-sourcing |
| `DeliveryOfferQuoted` | analytics | `offerId` | QuoteDeliveryOffers persisted an offer (one event per offer) | analytics only |
| `PromisePolicyPublished` | analytics | `policyVersion` | PublishPromisePolicy is saved (G19) | analytics only |

Payload rules:

- The `data` field lists are exactly the model's `publishes[].data` lists. `PromiseUnkeepable` (E9), `DeliveryOfferQuoted` and `PromisePolicyPublished` are not in the model; their lists are defined in `apis/asyncapi.yaml` (ADR 0002, assumptions A19 and A20). `pickupDepartureId` is omitted on a NOMINAL `PromiseMade` (contracts-delta E8). `deliveredAt` is omitted on a `PromiseBroken` of cause LOST or RETURNED.
- `PromiseBroken.cause` is `LATE`, `LOST` or `RETURNED`.
- A breaking change is a new `.v2` type; a consumer ignores unknown fields.
- The analytics-only events carry no PII (there is none in this context) and are not read by any integration consumer.

## Consequences

- `TestEventCatalogueMatchesContract` fails when `apis/asyncapi.yaml` declares a type this table lacks.
- Adding a published event needs: a line above, a message and schema in `apis/asyncapi.yaml`, an encoder in `internal/adapters/outbound/kafka`, and a golden JSON test.
