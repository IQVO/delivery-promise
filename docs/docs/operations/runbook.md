---
id: runbook
title: Runbook
sidebar_label: Runbook
description: "Deploying and operating Delivery Promise: Helm chart, probes, migrations, Kafka topics and consumer group, outbox relay, DLQ, sweeper and routine procedures."
---

# Delivery Promise runbook

Operational runbook for Delivery Promise (a domain-driven design study; all names are synthetic, see the disclaimer in the [README](https://github.com/IQVO/delivery-promise/blob/develop/README.md)). The service tells a shopper, before and at purchase, the date a package will arrive, and makes that date true by construction: a delivery offer is quoted only from a feasible witness path (node cutoff, scheduled departures, delivery wave) that has remaining capacity, a promise holds that capacity until the order is placed or the hold expires, and every promise is judged kept or broken.

Companion pages: [Configuration](/docs/operations/configuration), [Troubleshooting](/docs/operations/troubleshooting), [Observability](/docs/operations/observability), [Runtime details](/docs/overview/runtime), [Before the first release](/docs/operations/before-first-release).

## Runtime shape

Three binaries ship in one image (`cmd/api`, `cmd/mcp`, `cmd/a2a`); the Helm chart `charts/delivery-promise` deploys them as separate Deployments.

| Component | Chart switch | Port | What runs |
| --- | --- | --- | --- |
| `api` | always on | `:8080` (Service port 80) | REST API, the Kafka consumer, the sweeper, the outbox relay |
| `mcp` | `mcp.enabled` (off by default) | `:8090` | read-only MCP server, Streamable HTTP on `/mcp`, `GET /healthz` |
| `a2a` | `a2a.enabled` (off by default) | `:8093` | read-only A2A agent, JSON-RPC on `/` and `/a2a`, Agent Card at `/.well-known/agent-card.json`, `GET /healthz` |
| `frontend` | `frontend.enabled` (off by default) | `:8080` (nginx) | the `web/` console remote (ADR 0008) |

In the local cluster (`warehouse-infra` `helm-values/delivery-promise.yaml`) all four are enabled. Kong exposes the API at `/api/delivery-promise` (prefix stripped), MCP at `/api/mcp/delivery-promise` (rewritten to `/mcp`) and A2A at `/api/a2a/delivery-promise` (rewritten to `/`). `cmd/mcp` and `cmd/a2a` never dial Kafka and run no workers.

The `api` pod runs four things side by side:

- **REST API** (chi, `application/problem+json`, no authentication): offers, promises, promise books, policy. Contracts in [API reference](/docs/api-reference).
- **Transactional-outbox relay** (`cmd/api/workers.go`, `internal/adapters/outbound/outbox/relay.go`): drains `outbox_events` to Kafka every `OUTBOX_RELAY_INTERVAL` (default 1 s), batches of 100, one message at a time in `id` order, `FOR UPDATE SKIP LOCKED` (safe with more than one replica).
- **One Kafka consumer** (`internal/adapters/inbound/kafka/`) over the eight integration topics under the stable group `DELIVERY_PROMISE_CONSUMER_GROUP` (`delivery-promise-consumer` in this cluster); handlers keep local read models (`rm_*` tables) and claim each CloudEvents id in `processed_events`.
- **Sweeper** (`cmd/api/workers.go`): every `SWEEP_INTERVAL` (default 15 s) runs `ExpireOffers`, `ReleaseExpiredHolds`, `CloseBooks`, `RepriceAtRisk` in batches of 100; a failing pass is logged (`sweep pass failed`) and never stops the others.

Shutdown is sequential (`/readyz` 503, drain delay, HTTP, consumer, sweeper, relay last, pool); any worker that returns on its own exits the process non-zero so Kubernetes restarts it (`cmd/api/main.go`).

## Deployment

Chart `charts/delivery-promise` (`helm template delivery-promise charts/delivery-promise --set database.existingSecret=delivery-promise-db --set kafka.brokers=kafka:9092 --set config.consumerGroup=delivery-promise-consumer`). It renders exactly the variables each binary reads and **refuses to render** without `database.url` or `database.existingSecret`, `kafka.brokers` and `config.consumerGroup`, with a `*_MODE` or a non-`kafka` `EVENT_PUBLISHER` in `extraEnv`, or with a `terminationGracePeriodSeconds` that does not exceed `SHUTDOWN_DRAIN_DELAY` + 40 s (default 60 s). The mapping from chart values to variables is in [Runtime details](/docs/overview/runtime#deployment-helm-chart).

- Image: `ghcr.io/iqvo/delivery-promise` (cluster override `warehouse/delivery-promise`); the tag defaults to the chart `appVersion`. The pod runs as uid 1000, read-only root filesystem, all capabilities dropped, `RuntimeDefault` seccomp.
- Defaults: one replica, resources 100m/128Mi requests and 500m/256Mi limits; optional HPA (`autoscaling.api`, off), Ingress and Gateway API HTTPRoute (off).
- Chart tests: `bash charts/delivery-promise/tests/render_test.sh` and `python3 charts/delivery-promise/tests/test_service_selectors.py` (CI jobs `chart-tests`, `helm-lint`).
- Open packaging items are tracked in [Before the first release](/docs/operations/before-first-release).

## Migrations

Migrations are embedded in the binary (`internal/adapters/outbound/postgres/migrations/`: `0001_delivery_promise_schema`, `0002_window_time_zone`) and run at boot by **every** binary through golang-migrate (idempotent; the advisory lock makes concurrent starts safe, so `cmd/mcp` and `cmd/a2a` can boot against a fresh database before `cmd/api`). They use `MIGRATIONS_DATABASE_URL`, a **direct** non-pooled DSN, because golang-migrate takes a session advisory lock that a transaction pooler such as PgBouncer cannot honour; the runtime pool uses `DATABASE_URL`. Both keys come from the Secret `delivery-promise-db`. Boot retries migrations and the first ping five times (1+2+4+8 s). There is no separate migration job and no rollback automation; the `.down.sql` files exist for manual use.

## Dependencies

- **Postgres**: the only state store; no in-memory mode. `DATABASE_URL` is the pooled DSN (PgBouncer in this cluster).
- **Kafka**: one broker platform-wide, `KAFKA_BROKERS`, comma-separated. Topics are auto-created by the first writer (`AllowAutoTopicCreation`); nothing pre-creates them.
- **customer-ordering** calls this service synchronously over REST (`deliveryPromiseUrl` there): `POST /promises/{id}/confirmations` inside checkout and `POST /promises/{id}/cancellations` as checkout compensation. A latency or outage here is felt in that checkout path.

### Kafka topics and consumer groups

| Direction | Topic | Notes |
| --- | --- | --- |
| Publishes | `retailnetwork.delivery-promise.events` | integration events: `PromiseAtRisk`, `PromiseRevised`, `PromiseKept`, `PromiseBroken`, `PromiseUnkeepable`, `PromiseBookExhausted` |
| Publishes | `retailnetwork.delivery-promise.analytics` | analytics-only events: `PromiseMade`, `PromiseConfirmed`, `PromiseReleased`, `PromiseCancelled`, `DeliveryOfferQuoted`, `PromisePolicyPublished` |
| Consumes | `retailnetwork.customer-ordering.events`, `retailnetwork.order-sourcing.events`, `retailnetwork.shipping-and-tracking.events`, `retailnetwork.linehaul-transportation.events`, `retailnetwork.last-mile-delivery.events`, `retailnetwork.node-performance.events`, `retailnetwork.node-topology.events`, `retailnetwork.offer-availability.events` | one consumer group: `DELIVERY_PROMISE_CONSUMER_GROUP` |
| Dead letters | `<consumed topic>.dlq` | `x-dlq-source-topic`, `x-dlq-source-partition`, `x-dlq-source-offset`, `x-dlq-error`, `x-dlq-failed-at` headers |

The consumer group id is a chart value and must be stable: a rescheduled pod resumes from its committed offsets, while a brand-new group re-reads each topic from the start (the quickstart relies on that to fill the read models). Event contracts: [Events](/docs/api-reference/events).

## Health

- `GET /healthz`: liveness. `200 {"status":"ok"}` while the process serves, even while draining or with dependencies down. Also served by `cmd/mcp` and `cmd/a2a`.
- `GET /readyz` (api only): readiness. `200 {"status":"ready"}` only if the drain gate is open, Postgres answers a ping **and** a Kafka broker answers a metadata request that names no topic (so it never creates one). While draining: 503 `{"status":"not_ready"}`; with a dependency down: 503 `application/problem+json`, type `dependency-unavailable`, naming it. Verdicts are cached 3 s and one round is bounded to 2 s (ADR 0006).
- Chart probes: startup and liveness on `/healthz` (the startup probe allows about a minute for the bounded boot retry), readiness on `/readyz`.
- **Are writes still accepted when a dependency is down?** With **Kafka** down: yes (verified by `cmd/api/e2e_integration_test.go`: `/readyz` 503 naming kafka, `/healthz` 200, a write still succeeds); events queue in `outbox_events` and publish when the broker returns. With **Postgres** down: no, every write is a Postgres transaction, so writes fail 5xx and `/readyz` names postgres.

## Alerts and dashboards

- Grafana dashboard uid `warehouse-delivery-promise`: Kong RED, service HTTP RED, the business counter `promising_promises_made_total` by outcome, Go runtime and Loki logs.
- Alerts (`warehouse-infra`): Kong 5xx above 5 %, container restarts, and availability/latency SLO burn (fast and slow). A wedged consumer or relay surfaces as restarts because the process exits on any worker failure.
- Logs: Grafana Loki, `app=delivery-promise` (JSON slog on stdout).
- No metric exists for outbox depth, consumer lag or dead letters; use the SQL below. Details: [Observability](/docs/operations/observability).

## Failure modes

Short form; the symptom-first catalogue is [Troubleshooting](/docs/operations/troubleshooting).

**Pod crash-loop.** `kubectl -n warehouse-systems logs deploy/delivery-promise`: boot errors name the missing or invalid variable (`DATABASE_URL`, `KAFKA_BROKERS`, `DELIVERY_PROMISE_CONSUMER_GROUP`, `EVENT_PUBLISHER=<x> is not supported`) before the listener binds; a worker that returned on its own logs `worker stopped`. Fix the env or Secret (chart value), then `kubectl -n warehouse-systems rollout restart deploy/delivery-promise`. Postgres unreachable at boot: five retries (about 15 s) then exit, check the database first.

**Postgres unreachable.** `/readyz` 503 naming postgres, writes 5xx, `sweep pass failed`, `outbox relay pass failed`. `/healthz` stays 200. Restore the database; no restart is needed, everything retries on its interval and `/readyz` recovers within its 3 s cache. Queued outbox rows are untouched.

**Kafka unreachable.** `/readyz` 503 naming kafka; `outbox relay pass failed` on every pass; consumer fetches retry (a worker fails after 10 consecutive stalled fetches, then the pod restarts). REST writes are still accepted (2xx); events accumulate in `outbox_events` (`published_at IS NULL`) and publish in `id` order once the broker is back. Monitor depth with `SELECT count(*) FROM outbox_events WHERE published_at IS NULL;`.

**Outbox row parked after the attempt cap.** ERROR `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause`, with `outbox_id`, `topic`, `event_type`; later rows overtake the parked one. Inspect:
`SELECT id, event_type, topic, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20;`
(the table and its `published_at`, `attempts`, `last_error` columns are defined in `0001_delivery_promise_schema.up.sql`). A common cause is the first write to a topic auto-creating it (`UnknownTopicOrPartition`, ordinary, self-heals). After fixing the cause re-arm exactly the parked rows: `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`. The next pass publishes them with the same CloudEvents id (at-least-once).

**Consumer retries and dead letters.** A consumed topic whose producer has not written yet does not exist (topics are auto-created by the first writer), so the consumer only logs retries until that producer starts; confirm with the producer before debugging this side. A transient failure (Postgres down mid-handler) is retried up to 5 attempts with backoff 200 ms doubling to 5 s, then published to `<topic>.dlq`. A payload that can never be applied (a known event type whose `data` cannot be decoded or lacks a required id) is published to the `.dlq` topic **at once** and the offset committed past it (`internal/adapters/inbound/kafka/consumer.go`). A message that is not a CloudEvents 1.0 event, or whose type has no handler, is skipped with a WARN and committed. Redelivering an event id is a no-op thanks to `processed_events`.

## Routine procedures

- **Restart:** `kubectl -n warehouse-systems rollout restart deploy/delivery-promise` (graceful: up to about 45 s drain, 60 s grace period). The `-mcp` and `-a2a` Deployments restart the same way.
- **Logs:** Grafana Loki `app=delivery-promise`, or `kubectl -n warehouse-systems logs deploy/delivery-promise`.
- **Probe from inside the cluster:** `kubectl -n warehouse-systems port-forward svc/delivery-promise 8080:80`, then `curl localhost:8080/healthz` and `/readyz`.
- **Outbox depth and parked rows:** the SQL above, against the Postgres behind PgBouncer.
- **Dead letters:** consume `<topic>.dlq` and read the `x-dlq-*` headers; fix the producer, nothing to replay inside this service.
- **Publish a new promise policy:** `POST /promise-policies` with an `Idempotency-Key` (version is strictly increasing; a concurrent publish answers 409 `concurrent-update`). Offers and promises made afterwards record the new `policyVersion`.
- **Scale:** `replicaCount` or `autoscaling.api` in the chart. More than one replica is safe for the relay (`SKIP LOCKED`), the version-guarded writes and the consumer (stable group plus inbox dedupe); replays are no-ops (stable CloudEvents ids). It has not been load-tested.
- **Refresh the read models from scratch:** point the consumer at a new group id (re-reads each topic from the start); the inbox makes it safe to replay events already applied.
