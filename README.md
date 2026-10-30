# Delivery Promise

Retail network, bounded context **Delivery Promise** (Core Subdomain,
commerce tier). Tells a shopper, before and at purchase, the date a package
will arrive, and makes that date true by construction: a delivery offer is
quoted only from a feasible witness path (node cutoff, scheduled departures,
delivery wave) that has remaining capacity, and a promise holds that capacity
until the order is placed or the hold expires. Judges every promise kept or
broken. It owns offers, promises, capacity books and promise policies — not
sourcing (that is `order-sourcing`), not transport execution
(`linehaul-transportation`, `last-mile-delivery`, `shipping-and-tracking`):
every input is a local read model kept from events, and the service makes no
synchronous call. This repository is a service of the retail network, not of
the warehouse-systems fleet; the fleet reaches the network only through the
`vendor-connectivity` Vendor API.

## Disclaimer

This project is a domain-driven design study. It is not associated with, endorsed by or affiliated with any company, and it uses no company's code, data, logos or trademarks. All names, figures and examples are illustrative or synthetic.

## Documentation

The context is documented on the fleet-wide warehouse-docs site, in the
retail-network section: **<https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise>**
(the model under `network/model/model.yaml` in IQVO/warehouse-docs is the
single source of truth). Fleet-wide network pages:
[strategic design](https://iqvo.github.io/warehouse-docs/network/strategic-design/subdomain-classification),
[the CloudEvents event standard](https://iqvo.github.io/warehouse-docs/network/strategic-design/event-standard-cloudevents),
the [promise model](https://iqvo.github.io/warehouse-docs/network/network-model/promise-model),
and the [WMS/WES integration seam](https://iqvo.github.io/warehouse-docs/network/integration/wms-wes-integration).
This repository's own references: [`docs/runtime.md`](docs/runtime.md)
(configuration, probes, shutdown) and the packaging checklist
[`docs/before-first-release.md`](docs/before-first-release.md).

Page map (on the warehouse-docs site, under `network/contexts/delivery-promise/`):

| Area | Pages |
|---|---|
| Business context | [Business context](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/business-context), [Use cases](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/use-cases) |
| Language & model | [Ubiquitous language](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/ubiquitous-language), [Domain events](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/domain-events), [Domain message flow](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/domain-message-flow), [Class diagram](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/class-diagram), [Sequence diagrams](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/sequence-diagrams), [Entity-relationship](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/entity-relationship) |
| DDD canvases | [Core domain chart](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/core-domain-chart), [Bounded context canvas](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/bounded-context-canvas), [Aggregate design canvas](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/aggregate-design-canvas), [EventStorming](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/eventstorming), [Context map](https://iqvo.github.io/warehouse-docs/network/contexts/delivery-promise/context-map) |

## What it does

A quote asks: when can these lines arrive at this destination, per tier (`SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`)? The answer is built from a feasible witness path: a node cutoff, scheduled departures with room, a delivery wave with stops left. Every input is a local read model kept from events; the service makes no synchronous call.

| Aggregate | Role |
| --- | --- |
| `DeliveryOffer` | A quoted offer per shipment group and tier, with its witness path, `basis` and `policyVersion`; expires or is consumed by a promise. |
| `Promise` | `PROVISIONAL` holds capacity until the hold expires; `CONFIRMED` once the order is placed; then `AT_RISK`, `KEPT`, `BROKEN`, `RELEASED` or `CANCELLED`. A revision moves the hold to a new book in one transaction; the delivery window names its local time zone ([ADR 0011](docs/adr/0011-the-delivery-window-names-its-local-time-zone.md)). |
| `PromiseBook` | The capacity of one (node, pickup departure) cutoff, opened lazily on the first hold and closed at the cutoff by the sweeper. |
| `PromisePolicy` | Versioned tier deadlines, offer and hold TTLs, `packagesPerOrder`, book share. |

It judges every promise: `PromiseKept` when the last package is delivered by `deliverByLatest`, `PromiseBroken` with cause `LATE`, `LOST` or `RETURNED` otherwise. When re-promising finds no path it raises `PromiseUnkeepable`.

## Architecture

Hexagonal / ports & adapters; dependencies point inward only:

```
cmd/api/                      composition root: REST adapter, the Kafka consumer, the sweeper and the outbox relay
cmd/mcp/                      read-only MCP server (Streamable HTTP, ADR 0007)
cmd/a2a/                      read-only A2A agent (JSON-RPC over HTTP, ADR 0010)
internal/
  domain/                     pure business logic — book, calendar, offer, policy, promise, search, shared
  application/                ports, readmodel, repository, usecases, views
  adapters/
    inbound/http/             chi handlers, DTOs, problem+json mapping, readiness
    inbound/kafka/            the consumers feeding the local read models
    inbound/mcp/              MCP tools (read-only)
    inbound/a2a/              A2A skills (read-only)
    inbound/requestlog/       request logging middleware
    kafka/cloudevents/        CloudEvents 1.0 codec
    outbound/postgres/        repos, embedded migrations, pgtx UnitOfWork
    outbound/outbox/          transactional outbox + relay with attempt cap
    outbound/kafka/           integration and analytics publishers
    outbound/memory/          test doubles only, never linked into a binary
    outbound/clock/, ids/, repocontract/, telemetry/
  architecture/               the fitness tests
  bootretry/                  bounded boot retry
web/                          the delivery_promise_mfe Module Federation remote
features/                     godog BDD
```

The layering is enforced as an executable test, not a convention:
`internal/architecture` (`make arch-test`) fails the build if the domain ever
imports an outer layer or an adapter reaches sideways into its sibling.

## Domain events

Every message is a **CloudEvents 1.0** event in structured content mode:
`source` `/retailnetwork/delivery-promise`, `dataschema`
`urn:retailnetwork:delivery-promise:<stream>:<EventName>:v1`, Kafka key =
subject (the aggregate root id), type
`com.retailnetwork.commerce.delivery-promise.<entity>.<EventName>` with
entity `promise`, `promisebook`, `deliveryoffer` or `promisepolicy`
(ADR 0004). Published events go through the **transactional outbox**
(ADR 0005). Contract: [`apis/asyncapi.yaml`](apis/asyncapi.yaml).

**Published** — twelve events:

| Topic | Events |
|---|---|
| `retailnetwork.delivery-promise.events` (integration) | `PromiseAtRisk`, `PromiseRevised`, `PromiseKept`, `PromiseBroken`, `PromiseUnkeepable`, `PromiseBookExhausted` |
| `retailnetwork.delivery-promise.analytics` (analytics only) | `PromiseMade`, `PromiseConfirmed`, `PromiseReleased`, `PromiseCancelled`, `DeliveryOfferQuoted`, `PromisePolicyPublished` |

**Consumed** — 34 event types from eight producer topics (ADR 0003), kept as
event-fed local read models or driving RepromiseOnRisk, JudgeOnDelivery and
CancelPromise; every consumer is idempotent on the CloudEvents `id` (inbox
table) and dispatches on the full `type`:

| Topic | Events |
|---|---|
| `retailnetwork.customer-ordering.events` | `CustomerOrderPlaced`, `CustomerOrderCancelled` |
| `retailnetwork.order-sourcing.events` | `FulfillmentLocked`, `FulfillmentShipped`, `FulfillmentUnsourceable` |
| `retailnetwork.shipping-and-tracking.events` | `PackageDelivered`, `PackageExceptionRaised`, `PackageLost`, `PackageReturned` |
| `retailnetwork.linehaul-transportation.events` | `DepartureScheduled`, `DepartureCancelled`, `DepartureCapacityChanged`, `BookingFailed`, `BookingMoved` |
| `retailnetwork.last-mile-delivery.events` | `DeliveryWaveScheduled`, `DeliveryWaveCancelled`, `DeliveryWaveCapacityChanged`, `PackageInductionDeferred` |
| `retailnetwork.node-performance.events` | `NodeScorecardUpdated` |
| `retailnetwork.node-topology.events` | `NodeRegistered`, `NodeOperationalProfileDeclared`, `NodeActivated`, `NodeSuspended`, `NodeRetired`, `LaneOpened`, `LaneClosed`, `ServiceAreaAssigned`, `RegionDefined` |
| `retailnetwork.offer-availability.events` | `CatalogItemCreated`, `CatalogItemDiscontinued`, `ListingCreated`, `ListingAvailabilityChanged`, `ListingSuppressed`, `ListingRestored` |

## Running it

Real PostgreSQL and Kafka only (there is no in-memory mode and no compose
file in this repository); the embedded migrations run at boot, the consumer,
the sweeper and the outbox relay are always on:

```sh
export DATABASE_URL="postgres://user:pass@localhost:5432/delivery_promise?sslmode=disable"
export KAFKA_BROKERS="localhost:9092"
export DELIVERY_PROMISE_CONSUMER_GROUP="delivery-promise-consumer"
go run ./cmd/api                  # listens on :8080 (HTTP_ADDR)
```

`DATABASE_URL`, `KAFKA_BROKERS` and `DELIVERY_PROMISE_CONSUMER_GROUP` are
mandatory; a missing one is a boot error naming it. `cmd/mcp` and `cmd/a2a`
need only `DATABASE_URL` — they never dial Kafka.

### Configuration

| Env var | Default | Meaning |
|---|---|---|
| `DATABASE_URL` | **required** | Postgres DSN of the service's pool |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations (golang-migrate holds a session advisory lock, which breaks behind a transaction-pooling proxy) |
| `KAFKA_BROKERS` | **required** | Comma-separated `host:port` list (consumer, relay and `/readyz`) |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | **required** | Stable group id of the consumer of the eight producer topics (a chart value, never derived from the pod name) |
| `EVENT_PUBLISHER` | `kafka` | Only `kafka` is accepted; any other value is a boot error |
| `HTTP_ADDR` | `:8080` | Listen address |
| `SWEEP_INTERVAL` | `15s` | How often the sweeper runs its four passes (Go duration or seconds) |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Poll interval of the relay (Go duration or seconds) |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row; `0` = unlimited (ADR 0005) |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait between flipping `/readyz` to 503 and closing the listener; `0` disables |
| `LOG_LEVEL` | `info` | `debug`/`info`/`warn`/`error` |
| `OTEL_SERVICE_NAME` | `delivery-promise` | `service.name` of spans and metrics (ADR 0009) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4317` | Collector OTLP/gRPC; export never blocks |
| `SERVICE_VERSION` / `ENVIRONMENT` | `dev` / `local` | `service.version` / `deployment.environment.name` |

`cmd/mcp` adds `MCP_ADDR` (`:8090`); `cmd/a2a` adds `A2A_ADDR` (`:8093`) and
the mandatory `A2A_AGENT_URL` (the absolute URL advertised on the Agent
Card). Both read `DATABASE_URL`, the optional `MIGRATIONS_DATABASE_URL`,
`LOG_LEVEL`, `SERVICE_VERSION`, `ENVIRONMENT` and the OTel endpoint, and
nothing else.

## Observability

Logs, metrics and traces follow the fleet standard ([ADR 0009](docs/adr/0009-fleet-observability-standard.md)): JSON logs on stdout carry `trace_id`/`span_id`
and a `request_id`; `cmd/api`, `cmd/mcp` and `cmd/a2a` export OTLP/gRPC to the Collector (`OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_SERVICE_NAME`, `SERVICE_VERSION`, `ENVIRONMENT`;
export never blocks, so no Collector costs telemetry, nothing else) with HTTP request duration, Go runtime metrics and the business counter
`promising.promises.made` (`outcome`, `reason`); the trace continues across Kafka in the `traceparent` header of each published event and is picked up by the
consumer. The chart's `otel.*` values render the env.

## API

Contract: [`apis/openapi.yaml`](apis/openapi.yaml). Errors are RFC 9457
`application/problem+json` (`type` is
`https://retailnetwork.example/problems/<slug>`): `not-found` (404);
`offer-not-quotable`, `nominal-basis-not-allowed-for-tier`,
`idempotency-key-reused`, `validation-failed` (422). Every state-changing
POST takes an `Idempotency-Key`; a repeat with the same key and body replays
the first response (`POST /promises/{promiseId}:cancel` requires it).

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

### MCP server (read-only)

`cmd/mcp` (ADR 0007) exposes the five REST reads to AI agents over the Model Context Protocol, Streamable HTTP on `:8090`:

| Tool | Reads |
| --- | --- |
| `get_delivery_offer` | `GET /delivery-offers/{offerId}` |
| `get_promise` | `GET /promises/{promiseId}` |
| `list_promises_by_order` | `GET /promises?orderId=` |
| `list_promise_books` | `GET /promise-books?nodeId=&date=` |
| `get_current_policy` | `GET /promise-policies/current` |

It drives the same use cases as the REST adapter, writes nothing (no outbox, no consumer, no sweeper, never dials Kafka) and carries **no auth** — the fleet-wide decision; the in-cluster ClusterIP boundary is the access control. Run it locally:

```
DATABASE_URL=postgres://... go run ./cmd/mcp    # MCP_ADDR :8090, LOG_LEVEL, SERVICE_VERSION, ENVIRONMENT
```

### A2A agent (read-only)

`cmd/a2a` (ADR 0010) makes the context reachable as an Agent2Agent agent: JSON-RPC 2.0 on `/` and `/a2a` (`:8093`), the Agent Card at `/.well-known/agent-card.json`. It carries the *delegation of a goal*, never data sync (facts stay CloudEvents on Kafka). Two goal-shaped skills, both reads derived from the MCP tools (the MCP charter is read-only, so the agent is too):

| Skill | Arguments | Reads |
| --- | --- | --- |
| `promise_status` | exactly one of `orderId`, `promiseId` | the promises of an order, or one promise: state, terms, witness path, revisions, judgement |
| `book_capacity` | `nodeId`, `date` (YYYY-MM-DD) | the node's promise books on that day: share, provisional and confirmed holds |

A request is a message whose first part is `{"skill":"promise_status","arguments":{"orderId":"ord-1001"}}` (Data part, or Text part holding that JSON); unknown fields are refused. The answer is one message: a Data part `{skill, data | problem}` and a one-line Text summary. Rejections are RFC 7807-style `problem`s inside the message with the REST status classification (400 request shape, 404 `not-found`, 422 `validation-failed`, 500 `internal-error`), never JSON-RPC errors. Like MCP it writes nothing and carries **no auth**. `A2A_AGENT_URL` (absolute http(s) URL advertised on the card) is mandatory; the binary refuses to boot without it.

```
DATABASE_URL=postgres://... A2A_AGENT_URL=http://localhost:8093/ go run ./cmd/a2a    # A2A_ADDR :8093, LOG_LEVEL, SERVICE_VERSION, ENVIRONMENT
```

## Helm chart

The Helm chart is [`charts/delivery-promise`](charts/delivery-promise) (the `api` component only: Deployment, Service, ConfigMap, Secret or `database.existingSecret`, ServiceAccount, and an optional HPA, Ingress and Gateway API HTTPRoute, all off by default). It renders exactly the variables `cmd/api` reads and refuses to render without `database.url` or `database.existingSecret`, `kafka.brokers` and `config.consumerGroup`, with a `*_MODE` or a non-`kafka` `EVENT_PUBLISHER` in `extraEnv`, or with a `terminationGracePeriodSeconds` that does not exceed the shutdown bounds. See [`docs/runtime.md`](docs/runtime.md#deployment-helm-chart).

```
helm template delivery-promise charts/delivery-promise \
  --set database.existingSecret=delivery-promise-db \
  --set kafka.brokers=kafka:9092 \
  --set config.consumerGroup=delivery-promise-consumer
bash charts/delivery-promise/tests/render_test.sh
python3 charts/delivery-promise/tests/test_service_selectors.py
```

The CI `helm-lint` job runs `ct lint` (pull requests); `chart-tests` runs the
render and selector tests.

### Running the MCP server in Kubernetes

The chart deploys the MCP server with `mcp.enabled=true` (plus `mcp.route.enabled=true` for the Kong route `/api/mcp/delivery-promise` -> `/mcp`) and the A2A agent with `a2a.enabled=true` (it defaults `A2A_AGENT_URL` to the release's in-cluster Service DNS; `a2a.agentUrl` overrides it; `a2a.route.enabled=true` adds the Kong route `/api/a2/delivery-promise` -> `/`). Both are read-only and unauthenticated by fleet decision — the in-cluster ClusterIP boundary is the access control.

## Operator micro-frontend (web/)

`web/` (ADR 0008) is the `delivery_promise_mfe` Module Federation remote for the warehouse console: four read-only screens (Promises of an order, one Offer, a node's capacity Books, the current Policy) over the REST GETs above. Dev/preview port **4013**, production base `/mfes/delivery-promise/` behind the Nginx web gateway, API base from the shell's `/config.json` (`apiOrigin` + `/api/delivery-promise`). `npm` runs only inside `web/`; `@warehouse/ui-kit` is a sibling checkout:

```
cd web && npm install && npm run dev     # :4013, proxies nothing; run cmd/api for data
npm run lint && npx tsc -b && npm test && npm run build
```

## Local development / quality gate

```
make check          # format, vet, build, lint, unit + handler + BDD tests (the pre-push gate)
make check-fast     # fmt-check, vet, arch-test + changed-package tests (Stop hook)
make coverage       # >= 90% over internal/domain and internal/application
make mutation       # gremlins, one run per internal/domain package
make arch-test      # fitness tests
make integration    # testcontainers Postgres and Kafka (needs Docker)
make vuln           # govulncheck
```

lefthook (`lefthook install` once per clone) runs `fmt-check`, `vet`, `lint`
on pre-commit and `make check` on pre-push.

## Tests

- unit + handler tests: `make test` (`go test ./... -race`, CI job `test`,
  with the 90 % coverage gate in the same job)
- BDD acceptance: godog/Gherkin in `features/` (`make bdd`, CI job `bdd`)
- integration: testcontainers Postgres + Kafka (`make integration`, CI job
  `integration`; needs only Docker)
- mutation: gremlins per `internal/domain` package (CI job `mutation-fast`;
  the exhaustive `mutation` job is scheduled)
- architecture fitness: `internal/architecture` (CI job `arch-test`)
- API contract lint (CI job `api-lint`), spec/model drift (`drift`,
  scheduled), `lint`, `guide-lint`, `complexity`, `vuln`, `web` (the
  vitest/oxlint/tsc suite of `web/`), `helm-lint` + `chart-tests` (chart),
  `docker-publish` + `release` (main only)

## Architecture Decision Records

Index: [`docs/adr/index.md`](docs/adr/index.md) — 11 ADRs (0001–0011): the
bounded context (0001), the aggregates, invariants and numbered assumptions
(0002), consumed contracts and local read models (0003), the CloudEvents
catalogue (0004), the outbox relay (0005), the HTTP adapter, consumer,
sweeper and readiness (0006), the read-only MCP server (0007), the console
remote (0008), the fleet observability standard (0009), the A2A agent (0010),
the delivery window's local time zone (0011).

## License

[MIT](LICENSE).
