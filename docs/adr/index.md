---
id: index
title: Architecture decision records
sidebar_label: ADR index
slug: /
sidebar_position: 0
---

# Architecture decision records

The decisions of `delivery-promise`, in the order they were taken. The ADR bodies are immutable; a changed decision gets a new ADR that supersedes the old one. The *Status* column is copied from each ADR's own `## Status` section.

| ADR | Title | Status |
| --- | --- | --- |
| [0001](./0001-delivery-promise-bounded-context.md) | Delivery Promise is a bounded context of the retail network | Accepted (bootstrap) |
| [0002](./0002-aggregates-and-invariants.md) | the DeliveryOffer, Promise, PromiseBook and PromisePolicy aggregates, their invariants and the assumptions the model left open | Accepted (2026-10-23) |
| [0003](./0003-consumed-contracts-and-local-read-models.md) | consumed contracts, local read models and relationships | Accepted (2026-10-23) |
| [0004](./0004-cloudevents-envelope-and-type-catalogue.md) | CloudEvents envelope and type catalogue | Accepted (2026-10-23) |
| [0005](./0005-outbox-relay-and-attempt-cap.md) | the outbox relay, its attempt cap and the CloudEvents encoder | Accepted (2026-10-23) |
| [0006](./0006-http-adapter-consumer-and-readiness.md) | the HTTP adapter, the consumer, the sweeper and readiness | Accepted (2026-10-23) |
| [0007](./0007-mcp-server-adoption.md) | a read-only MCP server | Accepted (2026-10-24) |
| [0008](./0008-console-remote.md) | a warehouse-console web remote | Accepted (2026-10-24) |
| [0009](./0009-fleet-observability-standard.md) | the fleet observability standard (logs, metrics, traces) | Accepted (2026-10-26) |
| [0010](./0010-a2a-inbound-adapter.md) | A2A inbound adapter | Accepted (2026-10-29) |
| [0011](./0011-the-delivery-window-names-its-local-time-zone.md) | the delivery window names its local time zone | Accepted (2026-10-30) |
| [0012](./0012-adopt-the-fleet-rest-api-guideline.md) | adopt the fleet REST API guideline | Accepted (2026-11-01) |
| [0013](./0013-agent-authority-levels-and-complete-mcp-surface.md) | agent authority levels and complete MCP surface | Accepted (2026-11-02) |
| [0014](./0014-measured-transit-basis.md) | measured-transit promising (basis MEASURED) | Accepted (2026-11-05) |
| [0015](./0015-confirmed-units-stay-decremented.md) | confirmed units stay decremented until the feed that accounts for them lands | Accepted (2026-11-05) |
| [0016](./0016-book-charge-follows-the-locked-node.md) | the PromiseBook charge follows the node order-sourcing locks | Accepted (2026-11-05) |
| [0017](./0017-owner-aware-availability-and-promise-netting.md) | Owner-aware availability and promise netting | Accepted (2026-11-08) |
| [0018](./0018-analytics-read-side.md) | Analytics read side | Accepted (2026-11-09) |
| [0019](./0019-batch-delivery-estimates-for-listing-pages.md) | Batch delivery estimates for listing pages | Accepted (2026-11-09) |
| [0022](./0022-fast-tier-candidates-are-filtered-on-node-eligibility.md) | fast-tier candidates are filtered on node eligibility | Accepted (2026-11-09) |
