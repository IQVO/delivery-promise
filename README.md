# delivery-promise

Retail network, bounded context **Delivery Promise** (Core, commerce tier).

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

Tells a shopper, before and at purchase, the date a package will arrive, and makes that date true by construction: a delivery offer is quoted only from a feasible witness path (node cutoff, scheduled departures, delivery wave) that has remaining capacity, and a promise holds that capacity until the order is placed or the hold expires. Judges every promise kept or broken.

## What it does

A quote asks: when can these lines arrive at this destination, per tier (`SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`)? The answer is built from a feasible witness path: a node cutoff, scheduled departures with room, a delivery wave with stops left. Every input is a local read model kept from events; the service makes no synchronous call.

| Aggregate | Role |
| --- | --- |
| `DeliveryOffer` | A quoted offer per shipment group and tier, with its witness path, `basis` and `policyVersion`; expires or is consumed by a promise. |
| `Promise` | `PROVISIONAL` holds capacity until the hold expires; `CONFIRMED` once the order is placed; then `AT_RISK`, `KEPT`, `BROKEN`, `RELEASED` or `CANCELLED`. A revision moves the hold to a new book in one transaction. |
| `PromiseBook` | The capacity of one (node, pickup departure) cutoff, opened lazily on the first hold and closed at the cutoff by the sweeper. |
| `PromisePolicy` | Versioned tier deadlines, offer and hold TTLs, `packagesPerOrder`, book share. |

It judges every promise: `PromiseKept` when the last package is delivered by `deliverByLatest`, `PromiseBroken` with cause `LATE`, `LOST` or `RETURNED` otherwise. When re-promising finds no path it raises `PromiseUnkeepable`.

Published (CloudEvents type `com.retailnetwork.commerce.delivery-promise.<entity>.<Event>`): `PromiseAtRisk`, `PromiseRevised`, `PromiseKept`, `PromiseBroken`, `PromiseUnkeepable` and `PromiseBookExhausted` on `retailnetwork.delivery-promise.events`; `PromiseMade`, `PromiseConfirmed`, `PromiseReleased`, `PromiseCancelled`, `DeliveryOfferQuoted` and `PromisePolicyPublished` on `retailnetwork.delivery-promise.analytics`.

Consumed (34 types from eight contexts, ADR 0003): customer-ordering, order-sourcing, shipping-and-tracking, linehaul-transportation, last-mile-delivery, node-performance, node-topology, offer-availability.

| Route | Use case |
| --- | --- |
| `POST /delivery-offers:quote` | quote offers per group and tier (optional `Idempotency-Key`) |
| `GET /delivery-offers/{offerId}` | read an offer |
| `POST /promises` | make a PROVISIONAL promise from an offer, holding capacity |
| `POST /promises/{promiseId}:confirm` | confirm it for an order group (called inside checkout) |
| `POST /promises/{promiseId}:cancel` | cancel a confirmed promise and release its capacity (checkout compensation; no body, `Idempotency-Key` required) |
| `GET /promises/{promiseId}`, `GET /promises?orderId=` | read promises |
| `GET /promise-books?nodeId=&date=` | capacity books of a node and day |
| `POST /promise-policies`, `GET /promise-policies/current` | publish and read the policy |

Contracts: [`apis/openapi.yaml`](apis/openapi.yaml), [`apis/asyncapi.yaml`](apis/asyncapi.yaml).

## Status and design

The design is proposed in [retail-network docs](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise); `model/model.yaml` there is the single source of truth. This repository is a service of the retail network, not of the warehouse-systems fleet; the fleet reaches the network only through the `vendor-connectivity` Vendor API.

Decisions are in [`docs/adr/`](docs/adr/index.md): the bounded context (0001), the aggregates, invariants and numbered assumptions (0002), consumed contracts and local read models (0003), the CloudEvents catalogue (0004), the outbox relay (0005), the HTTP adapter, consumer, sweeper and readiness (0006), the read-only MCP server (0007), the A2A agent (0010). Configuration, probes and shutdown are in [`docs/runtime.md`](docs/runtime.md); the packaging checklist is [`docs/before-first-release.md`](docs/before-first-release.md).

## Runtime

Go, hexagonal (`internal/domain`, `internal/application`, `internal/adapters`, `cmd/api`), real PostgreSQL and Kafka only (`DATABASE_URL`, `KAFKA_BROKERS` and `DELIVERY_PROMISE_CONSUMER_GROUP` are mandatory), CloudEvents 1.0 on every message, transactional outbox with an attempt cap, chi REST adapter with problem+json, dependency-aware `/readyz`.

## Deployment

The Helm chart is [`charts/delivery-promise`](charts/delivery-promise) (the `api` component only: Deployment, Service, ConfigMap, Secret or `database.existingSecret`, ServiceAccount, and an optional HPA, Ingress and Gateway API HTTPRoute, all off by default). It renders exactly the variables `cmd/api` reads and refuses to render without `database.url` or `database.existingSecret`, `kafka.brokers` and `config.consumerGroup`, with a `*_MODE` or a non-`kafka` `EVENT_PUBLISHER` in `extraEnv`, or with a `terminationGracePeriodSeconds` that does not exceed the shutdown bounds. See [`docs/runtime.md`](docs/runtime.md#deployment-helm-chart).

```
helm template delivery-promise charts/delivery-promise \
  --set database.existingSecret=delivery-promise-db \
  --set kafka.brokers=kafka:9092 \
  --set config.consumerGroup=delivery-promise-consumer
bash charts/delivery-promise/tests/render_test.sh
python3 charts/delivery-promise/tests/test_service_selectors.py
```

## MCP server (read-only)

`cmd/mcp` (ADR 0007) exposes the five REST reads to AI agents over the Model Context Protocol, Streamable HTTP on `:8090`:

| Tool | Reads |
| --- | --- |
| `get_delivery_offer` | `GET /delivery-offers/{offerId}` |
| `get_promise` | `GET /promises/{promiseId}` |
| `list_promises_by_order` | `GET /promises?orderId=` |
| `list_promise_books` | `GET /promise-books?nodeId=&date=` |
| `get_current_policy` | `GET /promise-policies/current` |

It drives the same use cases as the REST adapter, writes nothing (no outbox, no consumer, no sweeper, never dials Kafka) and carries **no auth** -- the fleet-wide decision; the in-cluster ClusterIP boundary is the access control. Run it locally:

```
DATABASE_URL=postgres://... go run ./cmd/mcp    # MCP_ADDR :8090, LOG_LEVEL, SERVICE_VERSION, ENVIRONMENT
```

The chart deploys it with `mcp.enabled=true` (plus `mcp.route.enabled=true` for the Kong route `/api/mcp/delivery-promise` -> `/mcp`).

## A2A agent (read-only)

`cmd/a2a` (ADR 0010) makes the context reachable as an Agent2Agent agent: JSON-RPC 2.0 on `/` and `/a2a` (`:8093`), the Agent Card at `/.well-known/agent-card.json`. It carries the *delegation of a goal*, never data sync (facts stay CloudEvents on Kafka). Two goal-shaped skills, both reads derived from the MCP tools (the MCP charter is read-only, so the agent is too):

| Skill | Arguments | Reads |
| --- | --- | --- |
| `promise_status` | exactly one of `orderId`, `promiseId` | the promises of an order, or one promise: state, terms, witness path, revisions, judgement |
| `book_capacity` | `nodeId`, `date` (YYYY-MM-DD) | the node's promise books on that day: share, provisional and confirmed holds |

A request is a message whose first part is `{"skill":"promise_status","arguments":{"orderId":"ord-1001"}}` (Data part, or Text part holding that JSON); unknown fields are refused. The answer is one message: a Data part `{skill, data | problem}` and a one-line Text summary. Rejections are RFC 7807-style `problem`s inside the message with the REST status classification (400 request shape, 404 `not-found`, 422 `validation-failed`, 500 `internal-error`), never JSON-RPC errors. Like MCP it writes nothing (no outbox, consumer, sweeper or Kafka) and carries **no auth**. Run it locally:

```
DATABASE_URL=postgres://... A2A_AGENT_URL=http://localhost:8093/ go run ./cmd/a2a    # A2A_ADDR :8093, LOG_LEVEL, SERVICE_VERSION, ENVIRONMENT
```

`A2A_AGENT_URL` (absolute http(s) URL advertised on the card) is mandatory; the binary refuses to boot without it. The chart deploys it with `a2a.enabled=true` (it defaults the URL to the release's in-cluster Service DNS; `a2a.agentUrl` overrides it; `a2a.route.enabled=true` adds the Kong route `/api/a2a/delivery-promise` -> `/`).

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0009](docs/adr/0009-fleet-observability-standard.md)): JSON logs on stdout carry `trace_id`/`span_id`
and a `request_id`; `cmd/api`, `cmd/mcp` and `cmd/a2a` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`;
export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business counter
`promising.promises.made` (`outcome`, `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is picked up by the
consumer. The chart's `otel.*` values render the env.

## Web remote (console MFE)

`web/` (ADR 0008) is the `delivery_promise_mfe` Module Federation remote for the warehouse console: four read-only screens (Promises of an order, one Offer, a node's capacity Books, the current Policy) over the REST GETs above. Dev/preview port **4013**, production base `/mfes/delivery-promise/` behind the Nginx web gateway, API base from the shell's `/config.json` (`apiOrigin` + `/api/delivery-promise`). `npm` runs only inside `web/`:

```
cd web && npm install && npm run dev     # :4013, proxies nothing; run cmd/api for data
npm run lint && npx tsc -b && npm test && npm run build
```

## Develop

```
make check          # format, vet, build, lint, unit + handler + BDD tests (the pre-push gate)
make coverage       # >= 90% over internal/domain and internal/application
make mutation       # gremlins, one run per internal/domain package
make arch-test      # fitness tests
make integration    # testcontainers Postgres and Kafka (needs Docker)
make vuln
```
