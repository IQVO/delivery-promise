---
id: use-cases
title: Use cases
sidebar_label: Use cases
description: "Every application use case of Delivery Promise: trigger, inputs, invariants and the events it raises."
---

# Use cases

The application layer is `internal/application/usecases`: one method on `usecases.Service` per command or query, plus the projections that keep the local read models and drive the three reactive policies. Every use case that changes an aggregate saves it and appends its events to the outbox **in one Postgres unit of work**; a lost optimistic-concurrency race (`ports.ErrConcurrentUpdate`) is retried up to `MaxAttempts = 3` times before the caller sees 409 `concurrent-update` (`service.go`, ADR 0002 A28). A Kafka-driven use case also claims the CloudEvents `id` in `processed_events` in the same transaction, so a redelivery commits nothing.

All three binaries drive the **same** `Service`: REST (`cmd/api`) exposes the commands and queries, MCP and A2A expose only reads. The mapping from errors to problem slugs is in [API reference](/docs/api-reference#problems); the events are described on [Domain events](/docs/ddd/domain-events).

## Overview

| Use case | Trigger | Adapter | Events raised |
| --- | --- | --- | --- |
| `QuoteDeliveryOffers` | shopper or checkout | REST `POST /delivery-quotes` | `DeliveryOfferQuoted` (analytics) per offer |
| `MakePromise` | checkout | REST `POST /promises` | `PromiseMade`, and `PromiseBookExhausted` when the hold fills the book |
| `ConfirmPromise` | checkout, then `CustomerOrderPlaced` | REST `POST /promises/{promiseId}/confirmations`; consumer `OnCustomerOrderPlaced` | `PromiseConfirmed` |
| `CancelPromise` | checkout compensation, `CustomerOrderCancelled` | REST `POST /promises/{promiseId}/cancellations`; consumer `OnCustomerOrderCancelled` | `PromiseCancelled` |
| `PublishPromisePolicy` | operator | REST `POST /promise-policies` | `PromisePolicyPublished` (analytics) |
| Flag at risk | six consumed events | consumer | `PromiseAtRisk` |
| `ReviseAtRisk` (RevisePromise) | right after flagging, and the sweeper | consumer, sweeper | `PromiseRevised` or `PromiseUnkeepable` |
| `JudgeOnDelivery` | package facts | consumer | `PromiseKept` or `PromiseBroken` |
| `ReleaseExpiredHolds` | clock | sweeper | `PromiseReleased` |
| `ExpireOffers` | clock | sweeper | none |
| `CloseBooks` | clock | sweeper | none |
| `RepriceAtRisk` | clock | sweeper | as `ReviseAtRisk` |
| Read-model projections | 34 consumed event types | consumer | none |
| Reads | any | REST GET routes, MCP tools, A2A skills | none |

## QuoteDeliveryOffers

Source: `quote.go`, `internal/domain/search`.

- **Input:** `destinationPostalPrefix` (valid prefix, never a full address), `lines[]` (`catalogItemId`, `quantity` of at least 1; at most 100 lines), optional `tiers[]` (default all four of `SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`), optional `Idempotency-Key`.
- **What it does:** reads the current policy (version 0 default when nothing was published), runs the PromisePolicy search over the **local read models only** (no synchronous call) and splits the cart into the fewest shipment groups (one node for the whole cart when one can cover every line). For each (group, tier) with a feasible witness path (node cutoff, pickup departure with room and an OPEN book with share left, linehaul legs, a delivery wave with stops left) it persists a `QUOTED` offer; every other requested (group, tier) is reported with the constraint that blocked it: `DESTINATION_NOT_SERVED`, `ITEM_QUANTITY_NOT_AVAILABLE`, `DELIVERY_SLA_UNAVAILABLE`, `NODE_CAPACITY_EXHAUSTED` or `PAST_ORDER_DEADLINE`.
- **Invariants:** `expiresAt` is the earlier of `quotedAt + offerTtl` and the latest instant the witness pickup cutoff can still be met (`ErrOfferExpiryTooLate` is never returned, such an offer is not built); only `STANDARD` may fall back to a `NOMINAL` basis (`ErrNominalBasisNotAllowedForTier`); a tier whose order deadline in the destination region has passed is `PAST_ORDER_DEADLINE`.
- **Errors:** 422 `validation-failed` for a bad prefix, line or tier. "No offer" is a normal 200 with `unavailable` entries.
- **Idempotency:** safe to repeat; with an `Idempotency-Key`, a repeat inside the offers' TTL returns the same offers.
- **Events:** `DeliveryOfferQuoted` per offer, written to the outbox in the same transaction as the offers.

## MakePromise

Source: `make_promise.go`, `internal/domain/promise`, `internal/domain/book`.

- **Input:** `offerId` and the group's `lines` (they must equal the offer's lines), `Idempotency-Key` required.
- **What it does, in one transaction** (the documented exception to one aggregate per transaction, gap G22): consumes the offer, revalidates the witness path against the current read models (node ACTIVE, pickup departure and every leg scheduled, not cancelled, with room for `packagesPerOrder`; wave scheduled with a stop left), checks the lines are shippable from the witness node, holds one order of capacity on the witness `PromiseBook` (`nodeId:pickupDepartureId`, opened lazily by the first hold from the node's operational profile) and creates the `PROVISIONAL` promise with `holdExpiresAt = now + holdTtl`.
- **Invariants:** the offer must be `QUOTED` and unexpired (`ErrOfferNotQuotable`); `shipBy < deliverByEarliest <= deliverByLatest` (`ErrInvalidPromiseTerms`); a fast tier is `SCHEDULED` (`ErrNominalBasisNotAllowedForTier`); lines shippable from the witness node (`ErrGroupSpansNodes`); `provisional + confirmed <= share` (`ErrPromiseCapacityExhausted`); no hold once the book is CLOSED or after `cutoffAt` minus handling time (`ErrBookClosed`). A witness path that is no longer feasible is refused like a full book (409 `promise-capacity-exhausted`) so checkout re-quotes (ADR 0002 A27).
- **Replay:** a retry for an offer already `CONSUMED` with the same lines returns the existing promise (200 instead of 201).
- **Events:** `PromiseMade`; `PromiseBookExhausted` when the hold makes the book full. The business counter `promising.promises.made` is incremented with `outcome=made` or `refused` and the problem slug as `reason`.

## ConfirmPromise

Source: `lifecycle.go`.

- **Input:** `promiseId` and, in the body, `orderId` and `groupNo` (REST); or the groups of a `CustomerOrderPlaced` event.
- **What it does:** `PROVISIONAL` to `CONFIRMED` for an order group, moves the hold from `provisional` to `confirmed` on its book, in one transaction.
- **Invariants:** confirm only before `holdExpiresAt` (`ErrPromiseHoldExpired`); idempotent, a promise already confirmed (or at risk) for the same order and group changes nothing and raises nothing (ADR 0002 A16), another order or group is an illegal transition. Confirming and releasing stay legal on a CLOSED book so an in-flight confirm never fails for the clock.
- **Consumer path:** `OnCustomerOrderPlaced` is the reconciling confirm: normally the synchronous confirm already ran and this is a no-op; a promise still PROVISIONAL inside its hold is confirmed; an unknown or dead promise is logged (`order placed with an unknown promise`, `order placed on a dead promise`) and skipped, never retried.
- **Events:** `PromiseConfirmed`.

## CancelPromise

- **REST (`CancelPromise`):** `POST /promises/{promiseId}/cancellations`, no body, `Idempotency-Key` required. A `CONFIRMED` or `AT_RISK` promise becomes `CANCELLED` with `cancelReason` `CANCELLED_ON_REQUEST`, its confirmed hold goes back to the book and `PromiseCancelled` is raised, in one transaction. An already `CANCELLED` promise is returned unchanged with no event. Any other state (PROVISIONAL, KEPT, BROKEN, RELEASED) is 409 `illegal-transition` (ADR 0002 A30).
- **Consumer (`CancelPromises`, `OnCustomerOrderCancelled`):** cancels the promises of the named groups of the order, or every group when none is named, with the event's reason (default `ORDER_CANCELLED`); promises already closed are skipped.
- **Events:** `PromiseCancelled`.

## PublishPromisePolicy

Source: `lifecycle.go`, `internal/domain/policy`.

- **Input:** `offerTtlMinutes` and `holdTtlMinutes` (1 to 1440), `packagesPerOrder` (1 to 100), `capacitySharePercent` (1 to 100) and at most 200 `orderDeadlines` (`tier`, optional `regionId`, `localTime` as `HH:mm` in the destination station's time zone, no duplicate tier and region); `Idempotency-Key` required.
- **Invariants:** a published version is immutable and strictly increasing; the current policy is the highest version, and version 0 is the built-in default (10 minutes offer TTL, 15 minutes hold TTL, 1 package, 100 % share, no deadlines). A concurrent publish answers 409 `concurrent-update`.
- **Events:** `PromisePolicyPublished` on the analytics topic.

## Reactive policies (consumer)

These are the only places where other contexts' facts change a promise. All run inside the consumer's unit of work with the inbox claim.

### Flag at risk and re-promise (`RepromiseOnRisk`)

| Trigger event | Reason recorded | Which promises |
| --- | --- | --- |
| `FulfillmentUnsourceable` | `NODE_UNSOURCEABLE` | the event's `promiseId`; its `excludedNodes` are remembered on the assignment |
| `PackageExceptionRaised` | `PACKAGE_EXCEPTION` | the promise of the event's assignment |
| `PackageInductionDeferred` | `INDUCTION_DEFERRED` | the promise of the assignment (found through the tracking number when needed) |
| `DepartureCancelled` | `DEPARTURE_CANCELLED` | every active promise whose witness path uses the departure |
| `DeliveryWaveCancelled` | `WAVE_CANCELLED` | every active promise whose witness wave it is |
| `BookingFailed` | `BOOKING_FAILED` | the assignment's promise; that departure is skipped from now on |
| `BookingMoved` | `BOOKING_MOVED` | the assignment's promise; the departure it left is skipped |

`flagAtRisk` moves a `CONFIRMED` promise to `AT_RISK` (`PromiseAtRisk`); closed or not yet confirmed promises are skipped. A risk for an assignment whose lock was not seen is logged (`risk for an assignment whose lock was not seen`) and ignored.

`ReviseAtRisk` then searches again from now for the promise's group, for its tier and each slower tier down to `STANDARD`, skipping the assignment's excluded nodes and departures and, once the assignment is locked, every node but the locked one. The first candidate whose `deliverByLatest` is not earlier and whose new book accepts the hold becomes the new witness: it holds and confirms the new book, releases the old one and appends a `Revision` (`PromiseRevised`) in one transaction. A candidate that moves earlier (`ErrRevisionMovesEarlier`) or whose book is full or closed is skipped. When none works, `PromiseUnkeepable` is raised once. A failure is logged (`re-promise failed; the sweeper retries`) and left to the sweeper.

### JudgeOnDelivery

`FulfillmentLocked` records assignment to (promise, order, group, node); `FulfillmentShipped` records the package set; `PackageDelivered`, `PackageLost` and `PackageReturned` record per-package outcomes; each calls `judge`, which does nothing until the facts are complete (ADR 0002 A25). The earliest lost or returned package breaks the promise with cause `LOST` or `RETURNED`; otherwise it waits until every shipped package is delivered and judges on the latest delivery: `KEPT` if on or before `deliverByLatest`, else `BROKEN` with cause `LATE` and `lateByMinutes`. KEPT, BROKEN, RELEASED and CANCELLED are terminal (`ErrPromiseClosed`). **Events:** `PromiseKept` or `PromiseBroken`.

### Read-model projections

Node, lane, service area and region events (node-topology), catalogue item and listing events (offer-availability), scorecards (node-performance), departures and bookings (linehaul-transportation) and waves (last-mile-delivery) are written to the `rm_*` tables that the quote search reads. They change no aggregate and raise no event; a row that a capacity event created earlier keeps its remaining count when the schedule event arrives later.

## Sweeper use cases

Run every `SWEEP_INTERVAL` in batches of 100, independently; a failing pass never stops the others.

| Use case | Selects | Effect |
| --- | --- | --- |
| `ExpireOffers` | `QUOTED` offers past `expiresAt` | `EXPIRED` (an offer consumed meanwhile is skipped) |
| `ReleaseExpiredHolds` | `PROVISIONAL` promises past `holdExpiresAt` | `RELEASED` with reason `HOLD_EXPIRED`, releases the provisional hold on the book, raises `PromiseReleased` |
| `CloseBooks` | `OPEN` books whose cutoff was reached | `CLOSED` (already closed is a no-op) |
| `RepriceAtRisk` | `AT_RISK` promises, including those already reported unkeepable | retries `ReviseAtRisk`, since a new departure may have made them feasible |

## Reads

`GetOffer` (any state), `GetPromise` (any state), `ListPromisesByOrder` (`orderId`), `ListPromiseBooks` (`nodeId` and `date` as `YYYY-MM-DD`, the node's local date, resolved in the node's time zone and UTC when the node is unknown) and `CurrentPolicy`. They back the REST GET routes, the five MCP tools and the two A2A skills.
