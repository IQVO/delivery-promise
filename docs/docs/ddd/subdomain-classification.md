---
id: subdomain-classification
title: Subdomain classification
sidebar_label: Subdomain classification
description: "Delivery Promise is a Core subdomain of the retail network: the reasoning, and how its neighbours are classified."
---

# Subdomain classification

**Delivery Promise is Core** (retail network, Commerce tier, custom-built; business model: revenue through conversion, cost through not promising what the network cannot keep). Domain-Driven Design splits a domain into Core, Supporting and Generic subdomains by how much competitive advantage each gives, not by size or difficulty.

## Why Core

- **The promise is the customer-facing commitment.** A shopper decides partly on one line, "Arrives Thursday". The context makes that date true by construction: an offer exists only if a concrete witness path (node cutoff, scheduled departures, a delivery wave) with remaining capacity exists now, and a promise holds capacity on the witness node's pickup cutoff before the shopper pays. Source: `internal/domain/search`, `internal/domain/book`, `internal/domain/promise`.
- **It depends on the network's own model of itself.** The search combines node declarations, scorecard handicaps, linehaul schedules and last-mile waves; no packaged product can share that model, and buying one would mean conforming to someone else's idea of your own network.
- **It carries rules that differentiate:** fast tiers are quoted only from scheduled departures and waves (only `STANDARD` may fall back to nominal lane times), capacity is held per pickup cutoff against a declared share, a revision never moves the date earlier, and every promise is judged kept or broken.
- **Cost of getting it wrong is visible:** an early date sells more, a missed date loses trust and pays for the failure. The `promising.promises.made` metric and the `PromiseKept`/`PromiseBroken` judgements exist to make that visible.

Together with `order-sourcing` (late assignment) it is one of only two Core contexts: the test for Core is "would buying it, and conforming to the vendor's model, cost us the reason customers choose us".

## Neighbours

Copied from the strategic-design page of the aggregator (`warehouse-docs`, `network/docs/strategic-design/subdomain-classification.md`, ref `origin/main`). Full page: [Subdomain classification](https://iqvo.github.io/warehouse-docs/network/strategic-design/subdomain-classification).

| Bounded context | Classification | Tier | Relation to Delivery Promise |
| --- | --- | --- | --- |
| `delivery-promise` | Core | `commerce` | this context |
| `order-sourcing` | Core | `commerce` | consumed (`FulfillmentLocked`, `FulfillmentShipped`, `FulfillmentUnsourceable`); per the aggregator's model it reads this context's revision events |
| `node-topology` | Supporting | `logistics` | consumed (nodes, lanes, service areas, regions) |
| `offer-availability` | Supporting | `commerce` | consumed (catalogue items, listings) |
| `vendor-connectivity` | Supporting | `partner` | not a neighbour of this context |
| `linehaul-transportation` | Supporting | `logistics` | consumed (departures, bookings) |
| `last-mile-delivery` | Supporting | `logistics` | consumed (delivery waves, induction deferral) |
| `node-performance` | Supporting | `partner` | consumed (scorecards); per the aggregator's model it grades nodes from `PromiseKept`/`PromiseBroken` |
| `seller-inbound` | Supporting | `partner` | not a neighbour of this context |
| `customer-ordering` | Generic | `commerce` | consumed (`CustomerOrderPlaced`, `CustomerOrderCancelled`); calls this context over REST |
| `shipping-and-tracking` | Generic | `logistics` | consumed (package delivered, exception, lost, returned) |

The aggregator counts two Core, seven Supporting and two Generic contexts. Whether a neighbour consumes this context's events is the neighbour's decision; this repository records only what it publishes and what it consumes (see [Integration](/docs/ecosystem/integration)).

## Evolution

The aggregator's verdict for this context is "Custom-built". The code agrees: there is no off-the-shelf component for the search or the capacity ledger, while the plumbing around it (outbox relay, CloudEvents codec, chi, pgx, kafka-go) is conventional. See the [Core domain chart](/docs/ddd/core-domain-chart) for the strategic placement and the [Context map](/docs/ddd/context-map) for the relationships.
