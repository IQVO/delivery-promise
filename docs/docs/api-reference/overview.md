---
id: overview
title: API overview
sidebar_label: Overview
slug: /api-reference
sidebar_position: 1
description: "The interfaces of Delivery Promise (REST, events, MCP, A2A), every endpoint and the conventions of the contract."
---

# API overview

:::info Implemented by cmd/api
`cmd/api` serves every REST route below (`internal/adapters/inbound/http/server.go`) on `HTTP_ADDR` (default `:8080`). The contracts are `apis/openapi.yaml` (OpenAPI 3.0.3, document version 0.2.0) and `apis/asyncapi.yaml` (AsyncAPI 3.0.0, document version 1.1.0). The router registers exactly the ten operations of the spec plus `/healthz` and `/readyz`, which the spec does not list. See the [Quickstart](/docs/overview/quickstart) for real responses.
:::

| Interface | Source of truth | Notes |
| --- | --- | --- |
| REST | `apis/openapi.yaml` | The pages under *REST* are generated from the spec with `npm run gen-api-docs`; do not edit them by hand. The only server named in the spec is `http://localhost:8080`; in the kind cluster the API is reached through Kong at `http://localhost:8000/api/delivery-promise` (prefix stripped). |
| Events | `apis/asyncapi.yaml` | Twelve published types and the consumed ones, see the [event catalogue](/docs/api-reference/events) and [Domain events](/docs/ddd/domain-events). |
| MCP | [ADR 0007](/docs/adr/0007-mcp-server-adoption), `internal/adapters/inbound/mcp` | `cmd/mcp` serves Streamable HTTP on `/mcp` (also `/`) on port 8090; through Kong `/api/mcp/delivery-promise` rewritten to `/mcp`. Five read-only tools, no write tool: `get_current_policy`, `get_delivery_offer`, `get_promise`, `list_promise_books`, `list_promises_by_order`. See [MCP tools](/docs/mcp/tools). No auth, by fleet decision. |
| A2A | [ADR 0010](/docs/adr/0010-a2a-inbound-adapter), `internal/adapters/inbound/a2a` | `cmd/a2a` serves JSON-RPC 2.0 on `/` and `/a2a` on port 8093 (through Kong `/api/a2a/delivery-promise`), the Agent Card at `/.well-known/agent-card.json`. Two read-only skills, no write skill: `promise_status` (exactly one of `orderId`, `promiseId`) and `book_capacity` (`nodeId`, `date` as `YYYY-MM-DD`). Requests must send `A2A-Version: 1.0`. Domain rejections are answered inside the message envelope as RFC 7807 problems with the REST classification (400, 404 `not-found`, 422 `validation-failed`, 500 `internal-error`), never as JSON-RPC errors. No auth, by fleet decision. `/a2a` is not in `apis/openapi.yaml`. |

## Endpoints

| Tag | Endpoint | Operation id | Success |
| --- | --- | --- | --- |
| DeliveryOffers | `POST /delivery-quotes` | `quoteDeliveryOffers` | `200 {offers, unavailable}`; `Idempotency-Key` optional; "no offer" is a normal answer |
| DeliveryOffers | `GET /delivery-offers/{offerId}` | `getDeliveryOffer` | `200`, the offer in any state; `404 not-found` |
| Promises | `POST /promises` | `makePromise` | `201` with `Location`; `200` on an idempotent replay; `Idempotency-Key` required |
| Promises | `POST /promises/{promiseId}/confirmations` | `confirmPromise` | `200`, the promise; body `orderId`, `groupNo`; `Idempotency-Key` required |
| Promises | `POST /promises/{promiseId}/cancellations` | `cancelPromise` | `200`; no body; `Idempotency-Key` required; checkout compensation |
| Promises | `GET /promises/{promiseId}` | `getPromise` | `200`; `404 not-found` |
| Promises | `GET /promises?orderId=` | `listPromisesByOrder` | `200 {items, nextCursor}` keyset page (`limit`, `cursor`), one per group |
| PromiseBooks | `GET /promise-books?nodeId=&date=` | `listPromiseBooks` | `200 {items, nextCursor}` keyset page; `date` is `YYYY-MM-DD` in the node's time zone |
| PromisePolicy | `POST /promise-policies` | `publishPromisePolicy` | `201`, the new version; `Idempotency-Key` required |
| PromisePolicy | `GET /promise-policies/current` | `getCurrentPromisePolicy` | `200`; version 0 (`v0`) when nothing was published |
| Health (not in the spec) | `GET /healthz`, `GET /readyz` | none | `200`; `/readyz` is `503` while draining or when postgres or kafka is down |

The quote route is the **Open Host Service** for storefront contexts; `POST /promises`, `/confirmations`, `/cancellations`, the policy and the books are called only by customer-ordering's checkout or by operators. Storefront contexts must never call the restricted routes.

## Conventions in the contract

- **Idempotency.** `Idempotency-Key` is required on every state-changing POST except the quote, where it is optional (the middleware runs only when the caller sends a key; a repeat inside the offers' TTL then returns the same offers). Missing: `400 idempotency-key-required`. The key is global (not per route) and stored with a hash of method, path and body: the same key with the same request replays the stored status and body (header `Idempotency-Replayed: true`); any other request is `409 idempotency-key-reused`; a key that is not 1-128 printable ASCII is `400 idempotency-key-invalid`. A 2xx commits aggregates, outbox rows and the key together; a 4xx rolls the writes back but stores and replays the problem; a 5xx stores nothing. Keys are purged after 24 hours.
- **Errors** are RFC 9457 `application/problem+json`, `type = https://retailnetwork.example/problems/<slug>` (see below).
- **Windows.** A delivery window is two UTC instants (`earliest`, `latest`) plus the IANA `timeZone` of the destination delivery station, the local calendar its days belong to; `timeZone` is omitted only on an offer stored before ADR 0011 carried it.
- **Tiers** are `SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`; `basis` is `SCHEDULED` or `NOMINAL` (`NOMINAL` only for `STANDARD`).
- **Policy versions.** `policyVersion` is `v` plus an integer; a publish stores the next version (strictly increasing) and a concurrent publish is `409 concurrent-update`.
- **No personal data** appears in any schema: the destination is a postal prefix, never an address.
- **Auth: none.** The fleet reverted REST, MCP and A2A auth on 2026-09-11; `TestNoAuthMiddlewareReintroduced` fails CI if auth middleware returns. The in-cluster boundary is the access control.
- **Strict bodies.** Request bodies are decoded with unknown fields refused (`400 malformed-request`), and NUL bytes are rejected by middleware.

## Problems

Source: `internal/adapters/inbound/http/errors.go`. The first matching entry wins; anything else is `500 internal-error` with the cause withheld.

| Slug | Status | Raised when |
| --- | --- | --- |
| `malformed-request` | 400 | body is not valid JSON for the operation, unknown field, wrong type |
| `idempotency-key-required` | 400 | state-changing POST without `Idempotency-Key` |
| `not-found` | 404 | offer, promise (or other resource) does not exist |
| `offer-not-quotable` | 409 | offer is `EXPIRED` or `CONSUMED` |
| `promise-capacity-exhausted` | 409 | book share reached, or the witness path is no longer feasible |
| `book-closed` | 409 | book `CLOSED` or past its hold deadline |
| `promise-hold-expired` | 409 | confirm after `holdExpiresAt` |
| `promise-closed` | 409 | promise is `KEPT`, `BROKEN`, `RELEASED` or `CANCELLED` |
| `revision-moves-earlier` | 409 | a revision would move `deliverByLatest` earlier |
| `illegal-transition` | 409 | command not legal in the current state (for example cancelling a `PROVISIONAL` promise) |
| `concurrent-update` | 409 | optimistic-concurrency race lost three times, or another policy version was published meanwhile |
| `group-spans-nodes` | 422 | a line is not shippable from the witness node |
| `invalid-promise-terms` | 422 | terms break `shipBy < deliverByEarliest <= deliverByLatest` |
| `nominal-basis-not-allowed-for-tier` | 422 | `NOMINAL` basis on a tier other than `STANDARD` |
| `validation-failed` | 422 | field-level validation (ids, lines 1 to 100, dates, policy bounds) |
| `idempotency-key-reused` | 422 | same key and route, different body |
| `dependency-unavailable` | 503 | `/readyz` only: postgres or kafka down |
| `internal-error` | 500 | anything unclassified or a recovered panic |

The use cases that raise each error are on [Use cases](/docs/ddd/use-cases); symptoms and fixes on [Troubleshooting](/docs/operations/troubleshooting).
