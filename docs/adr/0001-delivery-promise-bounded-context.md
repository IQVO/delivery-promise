# 1. Delivery Promise is a bounded context of the retail network

Status: Accepted (bootstrap). Design source: retail-network-docs `model/model.yaml`, context `delivery-promise`, and its ADRs RN-0001 and RN-0007.

## Context

The retail network is modelled as ten bounded contexts, each its own service and database. Tells a shopper, before and at purchase, the date a package will arrive, and makes that date true by construction: a delivery offer is quoted only from a feasible witness path (node cutoff, scheduled departures, delivery wave) that has remaining capacity, and a promise holds that capacity until the order is placed or the hold expires. Judges every promise kept or broken.

## Decision

- This repo owns the aggregates DeliveryOffer, Promise, PromiseBook; the invariants and error names are those of the model.
- Business decisions of the context:
- A delivery offer exists only if at least one feasible path exists now; no path means no offer at that tier, with the constraint that blocked it.
- Fast tiers (SAME_DAY, NEXT_DAY, TWO_DAY) are never quoted from nominal lane times; only STANDARD may fall back to nominal transit, and the promise records basis NOMINAL.
- A promise reserves capacity on the node's pickup cutoff (PromiseBook); provisional holds expire if the order is not placed.
- A promise is per shipment group; a multi-node order gets one promise per group.
- A poorly performing node is handicapped (later shipBy) by its scorecard grade, never silently excluded.
- Kept means delivered on or before deliverByLatest.
- Events use the network CloudEvents namespace `com.retailnetwork.commerce.delivery-promise.<entity>.<Event>` on `retailnetwork.delivery-promise.events`
  (analytics-only events on `.analytics`). Published: PromiseMade, PromiseConfirmed, PromiseAtRisk, PromiseRevised, PromiseKept, PromiseBroken, PromiseReleased, PromiseCancelled, PromiseBookExhausted. No `warehouse.*` topic is consumed or produced.
- No live REST lookup into sibling contexts except the calls the model lists under `sync_calls`.
- No authentication on REST, consistent with the fleet. PII exists only in customer-ordering and shipping-and-tracking.

## Consequences

Relationships and the full consumed-event list are in the model and in the AsyncAPI contract added with the first feature PR.
