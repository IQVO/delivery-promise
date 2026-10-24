# ADR 0007: a read-only MCP server

## Status

Accepted (2026-10-24).

## Context

Every other bounded context of the fleet that exposes an HTTP API also ships an MCP server so that agents (warehouse-ops-agent first) can read its data without a bespoke REST client. delivery-promise had none. The service already has the read queries behind its REST adapter (`GetOffer`, `ListPromisesByOrder`, `GetPromise`, `ListPromiseBooks`, `CurrentPolicy` -- one per GET of apis/openapi.yaml), so an MCP adapter is another driving adapter over them, not new domain.

## Decision

- **A second driving adapter, a separate binary.** `internal/adapters/inbound/mcp` is built on the official MCP Go SDK (`github.com/modelcontextprotocol/go-sdk` v1.8.0) and served over Streamable HTTP only (no stdio, no SSE) by `cmd/mcp`, mounted at `/mcp` and `/` with an open `/healthz`. It is a second deployable beside `cmd/api`; the Helm chart gets an mcp Deployment and Service (default OFF) and an optional Kong HTTPRoute rewriting `/api/mcp/delivery-promise` to `/mcp`.
- **Five read tools over the existing queries, nothing else.** `get_delivery_offer(offerId)`, `get_promise(promiseId)`, `list_promises_by_order(orderId)`, `list_promise_books(nodeId, date)` and `get_current_policy()` (no arguments: the REST route takes none either). Arguments and results are lowerCamelCase like the REST contract -- the tools return the very views the REST adapter returns; typed errors carry the REST problem slug (`not-found`, `validation-failed`) as the prefix of the tool error. Quoting and the promise lifecycle are simulations and state changes, and stay REST-only.
- **Read-only is structural.** The adapter's `Deps` holds one `Reads` interface with the five query methods and nothing that can write; the adapter imports no outbound adapter, no event sink, no outbox, no Kafka. `cmd/mcp` never dials Kafka and never runs the consumer, the sweeper or the relay: those stay in `cmd/api`. `TestToolSurface` pins the exact tool set (budget 5), the read-only annotations and the absence of write verbs in tool names; `TestToolRegistryGolden` pins the whole advertised registry; the integration test proves the binary reads what the write use cases persisted in a real Postgres and adds no outbox row.
- **No sibling calls, no auth.** The server reads this context's own tables only (the event-fed local copies of ADR 0003 are not exposed), calls no other context over REST or MCP, and has no auth layer (the fleet-wide revert of 2026-09-11). Access control is the in-cluster ClusterIP boundary.
- **Boot.** `DATABASE_URL` is mandatory (no in-memory mode, ADR 0006); the binary runs the idempotent embedded migrations on start (with `MIGRATIONS_DATABASE_URL` for a direct DSN behind PgBouncer) exactly like `cmd/api`, so it can start against a fresh database before `cmd/api` has. `MCP_ADDR` (default `:8090`), `LOG_LEVEL`, `SERVICE_VERSION` and `ENVIRONMENT` complete the contract.

## Consequences

- Agents can answer "what was promised for this order, is the promise at risk, which node cutoff backs it, how full is the book, what policy is in force" without REST glue.
- Adding a tool is a reviewed act: the tool budget, the curated list and the golden file all change together (an amendment of this ADR).
- The REST surface stays the write path: quote, make, confirm, cancel and publish keep their Idempotency-Key semantics, and checkout keeps calling them, not the MCP server.
