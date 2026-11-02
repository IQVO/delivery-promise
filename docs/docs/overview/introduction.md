---
id: introduction
title: Introduction
sidebar_label: Introduction
description: "What Delivery Promise owns and does not own, its Core classification, capabilities and where to read next."
---

# Delivery Promise

Delivery Promise is the bounded context of the retail network that tells a shopper, before and at purchase, **the date a package will arrive**, and makes that date true by construction. It is a domain-driven design study; every name is synthetic (see the disclaimer in the [README](https://github.com/IQVO/delivery-promise/blob/develop/README.md)).

- **Area:** Network (retail network, commerce tier). The service belongs to the retail network, not to the warehouse-systems fleet; the fleet reaches the network only through the `vendor-connectivity` Vendor API.
- **Subdomain class:** **Core** ([reasoning](/docs/ddd/subdomain-classification)).
- **Repository:** `IQVO/delivery-promise`, Go, three binaries (`cmd/api`, `cmd/mcp`, `cmd/a2a`), a Module Federation remote in `web/`.

## The business problem

A retailer that shows an early date sells more; one that shows an early date and then misses it loses trust and pays for the failure. The context shows the **earliest date the network can actually keep**, and keeps it. Three things make that hard:

1. The network does not own the buildings that ship. A vendor declares operating hours, cutoffs and handling time (`node-topology`), and the network measures how well it keeps them (`node-performance`). The promise uses both.
2. The date depends on a chain of capacities owned elsewhere: a pickup departure at the node, scheduled middle-mile departures (`linehaul-transportation`) and a delivery wave at a delivery station (`last-mile-delivery`). Each has a cutoff and a finite capacity.
3. Promising is not assigning. The node that proves a date feasible now (the **witness**) need not be the node that ships; `order-sourcing` decides that later.

## What it owns

| Aggregate | Role | Where |
| --- | --- | --- |
| `DeliveryOffer` | A short-lived quote per shipment group and tier, with its witness path, basis (`SCHEDULED` or `NOMINAL`) and `policyVersion`. States `QUOTED`, `EXPIRED`, `CONSUMED`. | `internal/domain/offer` |
| `Promise` | The commitment for one shipment group of an order. `PROVISIONAL` holds capacity until the hold expires, `CONFIRMED` once the order is placed, then `AT_RISK`, `KEPT`, `BROKEN`, `RELEASED` or `CANCELLED`. Carries terms, witness path, revisions and the judgement. | `internal/domain/promise` |
| `PromiseBook` | The promise-side capacity ledger of one (node, pickup departure) cutoff: `provisional + confirmed <= share`. Opened lazily by the first hold, closed at the cutoff. | `internal/domain/book` |
| `PromisePolicy` | Versioned tier deadlines, offer and hold TTLs, packages per order, capacity share. Version 0 is the built-in default. | `internal/domain/policy` |

It also owns the **search** (`internal/domain/search`): the earliest-arrival witness path over the local read models, the `ScorecardHandicap` of a node and the shipment grouping of a cart.

## What it does not own

| Not here | Owner |
| --- | --- |
| Choosing the node that actually ships, and moving it until the vendor accepts | `order-sourcing` |
| Registering nodes, lanes, service areas and regions | `node-topology` |
| Scheduling departures and booking packages | `linehaul-transportation` |
| Delivery waves, routes and attempts | `last-mile-delivery` |
| Grading nodes | `node-performance` (this context only applies the grade) |
| Listings and available quantities | `offer-availability` |
| The shopper, the ship-to address and the order | `customer-ordering` (it holds the PII; this context promises to a **destination postal prefix** only) |
| Notifying the customer of a revised date | `customer-ordering` |
| Anything inside a vendor's building | the vendor |

Every input of the search is a **local read model** (`rm_*` tables) kept from events; the service makes **no synchronous call** to any sibling and reads no `warehouse.*` topic.

## Main capabilities

- **Quote** delivery offers per tier (`SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`) for a cart and a destination prefix; "no offer" comes with the constraint that blocked it.
- **Promise** a chosen offer: consume it, create the `PROVISIONAL` promise and hold capacity on the witness book in one transaction; confirm it when the order is placed; cancel it on request (checkout compensation) or when the order is cancelled.
- **Keep** the promise: flag it at risk when a departure, wave or package fails, re-promise it from the current network state (never earlier than before) and judge it `KEPT` or `BROKEN` (cause `LATE`, `LOST` or `RETURNED`) on delivery.
- **Publish** twelve event types (six integration, six analytics) through a transactional outbox, and consume 34 event types from eight producers.
- **Expose** a REST API, a read-only MCP server (five tools), a read-only A2A agent (two skills) and a console remote (`web/`, four read-only screens).
- **Operate** as a stateful consumer, sweeper and relay in one pod, on real Postgres and Kafka only.

## Read next

| If you want to | Page |
| --- | --- |
| understand the layers, binaries and data stores | [Architecture](/docs/overview/architecture) |
| run it locally | [Quickstart](/docs/overview/quickstart) |
| know every setting | [Configuration](/docs/operations/configuration) |
| operate or debug it | [Runtime](/docs/overview/runtime), [Runbook](/docs/operations/runbook), [Observability](/docs/operations/observability), [Troubleshooting](/docs/operations/troubleshooting) |
| test or extend it | [Testing](/docs/development/testing) |
| see who talks to it | [Integration](/docs/ecosystem/integration), [Context map](/docs/ddd/context-map) |
| call it | [API reference](/docs/api-reference), [Events](/docs/api-reference/events), [MCP tools](/docs/mcp/tools) |
| learn the model | [Ubiquitous language](/docs/ddd/ubiquitous-language), [Use cases](/docs/ddd/use-cases), [DDD artifacts](/docs/ddd/ddd-artifacts) |
| see why decisions were taken | [ADR index](/docs/adr) |
