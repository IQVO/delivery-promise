---
id: ubiquitous-language
title: Ubiquitous language
sidebar_label: Ubiquitous language
description: "The terms Delivery Promise speaks, each with its code identifier and the term it is most often confused with, plus the neighbour terms it reads."
---

# Ubiquitous language

The first table is this context's own language, with the identifier each term has in the code. The second lists neighbour terms this context reads through its event-fed read models; they keep the owner's meaning. The domain-wide glossary is on the aggregator ([ubiquitous language](https://iqvo.github.io/warehouse-docs/network/strategic-design/ubiquitous-language)).

## This context's terms

| Term | Definition | Code identifier | Not to be confused with |
| --- | --- | --- | --- |
| **Delivery Offer** | A quoted delivery window for one shipment group at one tier, valid until it expires. Holds no capacity. | `offer.Offer` (`internal/domain/offer`) | **Promise**: an offer commits nothing. **Listing** (`offer-availability`): what can be sold from a node, not when it arrives |
| **Service Tier** | `SAME_DAY`, `NEXT_DAY`, `TWO_DAY` or `STANDARD`; bounds the calendar days of the window (0, 1, 2, 7). | `shared.Tier` | A **Delivery Wave** window: the tier bounds the days, the wave gives the hours |
| **Promise** | The network's commitment of a delivery window for one shipment group of an order. | `promise.Promise` | **Fulfillment Assignment** (`order-sourcing`): which node actually ships. A promise names a witness, not an assignment |
| **Ship-By** | The pickup cutoff at the witness node the package must be ready for. | `promise.Terms.ShipBy` | **Required Ship-By** on a vendor purchase order |
| **Deliver-By Window** | `deliverByEarliest` to `deliverByLatest` shown to the customer, with the destination's `timeZone`. | `promise.Terms.Window`, `shared.Window` | The offer's `window`: the same shape before it becomes a promise |
| **Witness Path** | The concrete node, pickup departure, legs and delivery wave that prove a promise is feasible. `order-sourcing` may later choose a different path. | `shared.Witness` | **Candidate** (`order-sourcing`): a path optimised for cost. The witness is proof, not a plan |
| **Promise Basis** | `SCHEDULED` (from real departures and waves) or `NOMINAL` (`STANDARD` only, from nominal lane times). | `shared.Basis` | **Availability Basis** (`offer-availability`): about quantity, not time |
| **Promise Book** | The capacity ledger of one node pickup cutoff on the promise side, `bookId = nodeId:pickupDepartureId`. | `book.Book` | **Departure capacity** (`linehaul-transportation`): packages on a truck. The book counts orders against the node's share |
| **Capacity Hold** | A provisional reservation on a book, released if the order is not placed in time. | `book.Book.Hold`, field `provisional` | The confirmed count, which only a placed order creates |
| **Share** | The orders a book may hold for the cutoff, derived when the book opens from the node's profile and the policy's `capacitySharePercent`. | `book.Book` field `share`, `search.OpeningFor` | The node's **Throughput Cap** (declared by `node-topology`) |
| **Handicap** | The scorecard-driven delay of a node's ship-by: grade B multiplies handling time by 1.25, C skips one more feasible pickup, D two, A, PROVISIONAL and no scorecard add nothing. | `search.Handicap` | **Grade** (`node-performance`): the measurement. The handicap is this context's reaction |
| **Re-promise** | Revising a confirmed promise to a later window when its path is no longer feasible. | `promise.Promise.Revise`, `usecases.ReviseAtRisk` | **Reassignment** (`order-sourcing`): moving the group to another node without changing the window |
| **At risk** | A confirmed promise whose witness path stopped being feasible; carries a reason such as `DEPARTURE_CANCELLED`. | `promise.StateAtRisk`, `FlagAtRisk` | **Broken**: only judgement breaks a promise |
| **Unkeepable** | Re-promising found no path at any tier; the promise stays `AT_RISK`. | `promise.Promise.MarkUnkeepable` | **Broken** |
| **Promise Kept / Broken** | The judgement at delivery against `deliverByLatest`; a broken promise has cause `LATE`, `LOST` or `RETURNED`. | `promise.Judgement`, `promise.Verdict` | A **Delivery Attempt** outcome (`last-mile-delivery`) |
| **Shipment Group** | The lines of an order promised and shipped together from one node. | offer `groupNo`, promise `groupNo`, `search.GroupLines` | **Load** (`linehaul-transportation`) |
| **Policy** | A versioned set of parameters: offer and hold TTL, packages per order, capacity share, per-tier order deadlines. Version 0 is the built-in default. | `policy.Policy` | The policies of the Event Storming vocabulary |
| **Order Deadline** | Per tier and optional region, the local `HH:mm` after which a tier is no longer quoted (`PAST_ORDER_DEADLINE`). | `policy.Deadline`, `Policy.PastDeadline` | A node **Cutoff** |
| **Constraint** | Why a tier has no offer: `DESTINATION_NOT_SERVED`, `ITEM_QUANTITY_NOT_AVAILABLE`, `DELIVERY_SLA_UNAVAILABLE`, `NODE_CAPACITY_EXHAUSTED`, `PAST_ORDER_DEADLINE`. | `search` constraints, API `OfferConstraint` | An error: "no offer" is a normal 200 answer |
| **Revision** | One re-promise: `{no, previousTerms, reason, revisedAt}`. | `promise.Revision` | |
| **Hold expiry** | `holdExpiresAt`: the instant a provisional promise is released if no order confirmed it. | `promise.Promise.HoldExpiresAt` | The offer's `expiresAt` |
| **Read model** | A local copy of another context's facts, fed only by events. | `rm_*` tables, `internal/application/readmodel` | An aggregate: no invariant is enforced on it |
| **Outbox / Inbox** | Events written with the aggregates and published later; consumed event ids claimed with their effect. | `outbox_events`, `processed_events` | |

## Neighbour terms this context uses

| Term | Owner | Meaning here |
| --- | --- | --- |
| **Node**, **Node Type**, **Operational Profile**, **Cutoff**, **Handling Time**, **Throughput Cap** | `node-topology` | A building of the network, its declared hours, cutoffs and handling time. The witness node is the one node a promise names |
| **Lane**, **Region**, **Service Area** | `node-topology` | A directed connection between nodes, a partition of the network, and the postal prefixes one delivery station serves. The destination prefix is the key into the service area |
| **Catalog Item**, **Listing**, **Available Quantity** | `offer-availability` | What a node can ship now. Book capacity counts orders, not units |
| **Departure**, **Pickup Departure** | `linehaul-transportation` | One scheduled trip on a lane with a cutoff and capacity; the departure that collects packages at a vendor node. A book is keyed by its id |
| **Delivery Wave** | `last-mile-delivery` | A station's dispatch slot: induct cutoff, delivery window and stop capacity. The wave's window becomes the promise window |
| **Scorecard**, **Grade** | `node-performance` | A node's measured performance and its `A` to `D` grade, `PROVISIONAL` below the observation minimum |
| **Customer Order**, **Checkout** | `customer-ordering` | What the shopper bought, with a promise per shipment group. Checkout calls `MakePromise` once per group |
| **Fulfillment Assignment**, **Path Lock**, **Unsourceable** | `order-sourcing` | The decision of which node and path fulfil a group; the point after which the node never changes; no node can meet the promise any more |
| **Package Journey**, **Tracking Number** | `shipping-and-tracking` | The timeline of one package and its network-wide id |

## Fleet translation

None. This context never touches the warehouse-systems fleet: it consumes no `warehouse.*` topic and calls no fleet service. The fleet's words (CPT, process path) meet the network only in `vendor-connectivity` and `node-topology`.
