---
id: tools
title: MCP tools
sidebar_label: MCP tools
description: "The five read-only MCP tools of Delivery Promise, captured from a live tools/list: inputs, annotations and outputs."
---

# MCP tools

`cmd/mcp` (ADR 0007) is the read-only MCP server of the context: Streamable HTTP only (no stdio, no SSE), on `MCP_ADDR` (default `:8090`) at `/mcp` and `/`, plus `GET /healthz`. In the cluster it is the Service `delivery-promise-mcp:8090`, exposed through Kong at `/api/mcp/delivery-promise` (rewritten to `/mcp`); `warehouse-ops-agent` is configured with `http://delivery-promise-mcp.warehouse-systems.svc.cluster.local:8090/mcp`. It drives the same use cases as the REST GET routes, writes nothing (no outbox, no consumer, no sweeper, never dials Kafka) and carries **no authentication**; the in-cluster ClusterIP boundary is the access control.

The list below is the output of a live `tools/list` against the binary built from this branch (server `delivery-promise-mcp` 1.0.0, protocol `2025-03-26`, capabilities `tools` with `listChanged`). Every tool carries the annotations `readOnlyHint: true`, `idempotentHint: true`, `openWorldHint: false`, the `_meta` authority `com.warehouse/authority: "L1"` (Agentic Standard v2, [ADR 0013](/docs/adr/0013-agent-authority-levels-and-complete-mcp-surface): every tool of this context is a pure read, so every level is L1), and `additionalProperties: false` on its input. `TestToolSurface`, `TestWriteVerbSensorFailsOnWriteNames`, `TestEveryAgentToolDeclaresAuthority` and `TestToolRegistryGolden` (`internal/adapters/inbound/mcp`) pin the surface and refuse any write verb or missing authority.

| Tool | Access | Authority | Input fields | Reads (REST equivalent) |
| --- | --- | --- | --- | --- |
| `get_current_policy` | read-only | L1 | none | `GET /promise-policies/current` |
| `get_delivery_offer` | read-only | L1 | `offerId` (string, required) | `GET /delivery-offers/{offerId}` |
| `get_promise` | read-only | L1 | `promiseId` (string, required) | `GET /promises/{promiseId}` |
| `list_promise_books` | read-only | L1 | `nodeId` (string, required), `date` (string `YYYY-MM-DD`, required) | `GET /promise-books?nodeId=&date=` |
| `list_promises_by_order` | read-only | L1 | `orderId` (string, required) | `GET /promises?orderId=` |

## Tools in detail

### `get_current_policy`

Reads the current promise policy: `policyVersion` (`v0` is the built-in default when nothing was published), `offerTtlMinutes`, `holdTtlMinutes`, `packagesPerOrder`, `capacitySharePercent` and the per-tier `orderDeadlines` (`tier`, optional `regionId`, `localTime` in the destination region's time zone). Output keys: `policyVersion`, `version`, `offerTtlMinutes`, `holdTtlMinutes`, `packagesPerOrder`, `capacitySharePercent`, `orderDeadlines`, `publishedAt`. Publishing a policy is a REST operation.

### `get_delivery_offer`

Reads one offer in any state (`QUOTED`, `EXPIRED`, `CONSUMED`): tier, `groupNo`, destination postal prefix, lines, the delivery `window` (`earliest`, `latest` and the station's IANA `timeZone`), the `witnessPath` (node, pickup departure, legs, delivery wave), `basis` (`SCHEDULED` or `NOMINAL`), `policyVersion`, `quotedAt` and `expiresAt`. Offers are kept for a retention period after they expire. An unknown offer fails with `not-found`. Input: `offerId`, the id returned by the quote, for example `off-1`.

### `get_promise`

Reads one promise in any state (`PROVISIONAL`, `CONFIRMED`, `AT_RISK`, `KEPT`, `BROKEN`, `RELEASED`, `CANCELLED`): `terms` (tier, `shipBy`, `deliverByEarliest`, `deliverByLatest`, `timeZone`, `basis`, `policyVersion`), `witnessPath`, `holdExpiresAt`, the `revisions` that moved it and the final `judgement` (outcome, cause, `lateByMinutes`). A promise is never deleted. Output keys: `promiseId`, `offerId`, `orderId`, `groupNo`, `state`, `destinationPostalPrefix`, `lines`, `terms`, `witnessPath`, `holdExpiresAt`, `revisions`, `judgement`, `atRiskReason`, `cancelReason`, `releaseReason`, `version`. Unknown promise: `not-found`. Input: `promiseId`, for example `prm-1`.

### `list_promise_books`

Lists the capacity books of one node whose pickup cutoff falls on one date (`YYYY-MM-DD` in the node's own time zone), ordered by `cutoffAt`: one book per cutoff (`bookId` is `nodeId:pickupDepartureId`), its `OPEN` or `CLOSED` state and its capacity (`share`, `provisional`, `confirmed`). A malformed node id or date fails with `validation-failed`. Output: `items`.

### `list_promises_by_order`

Lists the promises of one customer order, one per shipment group, ordered by `groupNo`, in the same shape as `get_promise`. Only `CONFIRMED` promises belong to an order, so this is the checkout view of what was promised. A malformed order id fails with `validation-failed`. Output: `items`.

## Calling them

The handler is **stateless** (Agentic Standard v2, AS-4, [ADR 0013](/docs/adr/0013-agent-authority-levels-and-complete-mcp-surface)): it issues no `Mcp-Session-Id` and answers the 2026-07-28 revision's per-request handshake — a raw POST with `MCP-Protocol-Version: 2026-07-28` and `Mcp-Method: server/discover` returns `supportedVersions` without any `initialize`. A legacy `initialize` (2025-06-18 and older) still answers 200, so session-based clients keep working; requests must accept both `application/json` and `text/event-stream`. A captured `get_current_policy` call and its structured result are on the [Quickstart](/docs/overview/quickstart#mcp-streamable-http-read-only). The optional inputs of the registry carry `omitempty` so they stay optional in the schema (`TestMCPOptionalToolInputsCarryOmitempty`); today every input listed above is required.

## Resource

`promise://books/{nodeId}/{date}` (MIME `application/json`, [ADR 0013](/docs/adr/0013-agent-authority-levels-and-complete-mcp-surface)): the capacity ledger of one node's pickup cutoffs on one date — the same bounded read model as `list_promise_books` and `GET /promise-books`, as JSON text (`{nodeId, date, items}`). Listed via `resources/templates/list`; the URI segments are model-supplied and a malformed URI is rejected.

## Prompt

`check_order_promises` ([ADR 0013](/docs/adr/0013-agent-authority-levels-and-complete-mcp-surface)) is the operator SOP: how to judge whether an order's promises are intact — list them, read states and judgements, consult the node's books, escalate what is AT_RISK or BROKEN. It names tools but calls none, and takes no arguments.

The server's `instructions` string tells a client that nothing here changes data: quoting and the promise lifecycle are REST operations driven by checkout, and the policy is published over REST too. The A2A agent offers the same reads as goal-shaped skills, see the [API overview](/docs/api-reference).
