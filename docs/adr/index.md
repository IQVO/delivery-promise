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
