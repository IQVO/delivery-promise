---
id: troubleshooting
title: Troubleshooting
sidebar_label: Troubleshooting
description: "Symptom, cause, check and fix for the real failure modes of Delivery Promise: boot errors, readiness, outbox, consumer, idempotency, MCP and A2A."
---

# Troubleshooting

Each entry is a symptom you can observe, the cause in the code, a check you can run and the fix. For routine operations see the [Runbook](/docs/operations/runbook); for every setting see [Configuration](/docs/operations/configuration). Log lines quoted below are the real messages.

## Boot

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `cmd/api` exits at once with `DATABASE_URL is required: this service runs on real Postgres, there is no in-memory mode` | The variable is unset or empty. `cmd/mcp` and `cmd/a2a` fail the same way | Pod env or Secret `delivery-promise-db` key `DATABASE_URL` | Set it (chart `database.url` or `database.existingSecret`) |
| `KAFKA_BROKERS is required: this service runs on real Kafka (outbox relay, consumer, /readyz)` | Unset, or only blank entries such as `,` | `kubectl get deploy -o yaml`, the `KAFKA_BROKERS` env | Set `kafka.brokers`. Only `cmd/api` needs it; `cmd/mcp` and `cmd/a2a` never read it |
| `DELIVERY_PROMISE_CONSUMER_GROUP is required: the consumer of the eight producing contexts always runs on a stable group` | No group configured | env | Set chart `config.consumerGroup` (cluster value `delivery-promise-consumer`). Never derive it from the pod name |
| `EVENT_PUBLISHER=<x> is not supported: this service publishes to real Kafka` | Any value other than empty or `kafka` | env | Remove the variable or set `kafka` |
| `cmd/a2a`: `A2A_AGENT_URL is required: an absolute http(s) URL the Agent Card advertises ... (got "...")` | Missing, not http(s), or no host | env | Set an absolute URL such as `http://delivery-promise-a2a.<ns>.svc.cluster.local:8093/` (the chart defaults it from the release's Service DNS) |
| Log `retrying` with `op="run migrations"` or `op="ping postgres"`, then exit `run migrations (after 5 attempts): ...` | Postgres unreachable at boot (also: the first outbound dial of a pod with a fresh Istio sidecar can be reset). Backoff 1+2+4+8 s | `psql` the DSN from the Secret; check PgBouncer | Restore the database; the next restart succeeds. Transient resets recover on their own (`succeeded after retry`) |
| Migration hangs or fails at boot behind a pooler | `DATABASE_URL` points at PgBouncer in transaction mode and `MIGRATIONS_DATABASE_URL` is not set, so golang-migrate's session advisory lock cannot be honoured (`internal/adapters/outbound/postgres`, runtime notes) | Is `MIGRATIONS_DATABASE_URL` present in the pod env? | Provide the direct (non-pooled) DSN (chart `database.migrationsExistingSecretKey`) |
| `cmd/api` exits later with `consumer stopped unexpectedly: exiting so the orchestrator restarts the service` (or `outbox-relay`, `sweeper`) | A background worker returned on its own. For the consumer: a broker still unreachable after 10 consecutive fetch retries, or a non-transient fetch error (connection refused, group errors) | The preceding ERROR `worker stopped` with `worker` and `error` | Fix the cause (usually Kafka reachability); Kubernetes restarts the pod. Repeated restarts fire the container-restart alert |
| `helm template` refuses to render | The chart guards: missing `database.*`, `kafka.brokers`, `config.consumerGroup`; a `*_MODE` or non-`kafka` `EVENT_PUBLISHER` in `extraEnv`; `terminationGracePeriodSeconds` not above `SHUTDOWN_DRAIN_DELAY` + 40 s | The `fail` message names the value | Set the missing value; raise the grace period |

## Probes and readiness

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| `/readyz` answers 503 `application/problem+json`, type `dependency-unavailable`, naming `postgres` | The pool ping failed | `curl /readyz` body names the dependency; Postgres/PgBouncer health | Restore Postgres. Verdict cache is 3 s, so recovery is quick. Writes also fail 5xx meanwhile |
| `/readyz` 503 naming `kafka` while writes still succeed | The metadata request to the brokers failed or exceeded the 2 s bound. Writes are not blocked: events queue in `outbox_events` | `KAFKA_BROKERS` reachability from the pod | Restore Kafka; the relay then publishes the backlog in `id` order |
| `/readyz` 503 `{"status":"not_ready"}` and the process is still up | Draining after SIGTERM (`SHUTDOWN_DRAIN_DELAY`, default 5 s) | Log `shutdown: readiness flipped to not-ready; waiting for traffic to drain` | Nothing to fix: the pod is terminating |
| Pod `Running` but never `Ready`, `/healthz` 200 | Liveness only proves the process serves; readiness waits for Postgres **and** Kafka | `curl /readyz` | See the two rows above |
| Pod killed (SIGKILL) during a rollout, drain cut short | `terminationGracePeriodSeconds` below the sum of the shutdown bounds | Default is 60 s against drain 5 + 10 + 10 + 10 + 10 | Keep the chart default or more |

## Writes and idempotency

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| 400 `idempotency-key-required` | `POST /promises`, `:confirm`, `:cancel` and `/promise-policies` require the `Idempotency-Key` header (only the quote makes it optional) | Request headers | Send a unique key per logical operation |
| 422 `idempotency-key-reused` | Same key and route with a different body. **Also after a failed first try:** a 4xx outcome is stored under the key, so correcting the body and re-sending with the same key gives this error (seen on the quickstart run: a 422 `validation-failed` for missing `lines`, then 422 `idempotency-key-reused` on the fixed body) | Compare bodies; response lacks `Idempotency-Replayed` | Use a new key after changing the body. A 5xx or panic stores nothing, so the same key may retry. Keys are purged after 24 h |
| 422 `validation-failed: a group needs between 1 and 100 lines` on `POST /promises` | The body must repeat the offer's `lines` as well as `offerId` | `GET /delivery-offers/{id}` for the lines | Send `offerId` and the same `lines` |
| 409 `offer-not-quotable` | The offer is EXPIRED or CONSUMED (TTL is `offerTtlMinutes`, also capped by the pickup cutoff) | `GET /delivery-offers/{id}` state | Quote again |
| 409 `promise-capacity-exhausted` / `book-closed` | The witness PromiseBook has no capacity left, or its cutoff passed and the sweeper closed it | `GET /promise-books?nodeId=&date=` | Quote again (another departure or tier); `promising.promises.made{reason=...}` shows the trend |
| 409 `promise-hold-expired` on confirm | The PROVISIONAL hold passed `holdExpiresAt` (`holdTtlMinutes`); the sweeper releases such promises every `SWEEP_INTERVAL` | `GET /promises/{id}` state `RELEASED` | Make a new promise from a fresh quote; confirm earlier in checkout |
| 409 `illegal-transition` | e.g. cancelling a PROVISIONAL, KEPT, BROKEN or RELEASED promise (ADR 0002 A30) | promise state | Only CONFIRMED or AT_RISK promises can be cancelled |
| 409 `concurrent-update` | Another request changed the aggregate; the use case already retried 3 times (`usecases.MaxAttempts`). `POST /promise-policies` returns it too when another version won | Retry immediately | Re-fetch and retry; for policies read `GET /promise-policies/current` first |
| 400 `malformed-request`: `json: unknown field "x"` | Request bodies are decoded strictly (unknown fields refused) | The `detail` names the field (the quote's line field is `catalogItemId`, not `itemId`) | Use the names in the OpenAPI contract |
| Quote returns `unavailable` constraints only | `DESTINATION_NOT_SERVED`, `ITEM_QUANTITY_NOT_AVAILABLE`, `DELIVERY_SLA_UNAVAILABLE`, `NODE_CAPACITY_EXHAUSTED` or `PAST_ORDER_DEADLINE`. The local read models (`rm_*`) are empty or stale when the producers' events have not been consumed | `SELECT (SELECT count(*) FROM rm_nodes) nodes, (SELECT count(*) FROM rm_departures) departures, (SELECT count(*) FROM rm_waves) waves, (SELECT count(*) FROM rm_listings) listings;`; consumer logs | Wait for the consumer to catch up; check the group is reading (below) |
| 500 `internal-error` | An untyped error or a recovered panic; the response never leaks the cause | ERROR access-log line with `request_id`, then the same `trace_id` | Fix from the log; `promising.promises.made{reason="internal-error"}` rises |

## Outbox

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Events not appearing on `retailnetwork.delivery-promise.events` / `.analytics`, `outbox relay pass failed` every second | Kafka unreachable or the topic has no leader yet (first write auto-creates it, `UnknownTopicOrPartition`) | `SELECT count(*) FROM outbox_events WHERE published_at IS NULL;` grows | Restore Kafka. A first-write error self-heals. Rows publish in `id` order |
| ERROR `outbox relay: row reached the attempt cap and is excluded from future claims; re-arm it with attempts = 0 after fixing the cause` | A row failed `OUTBOX_MAX_ATTEMPTS` (20) times | `SELECT id, event_type, topic, attempts, last_error FROM outbox_events WHERE published_at IS NULL AND attempts >= 20;` | Fix the cause (`last_error`), then `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`. Later rows overtake the parked one meanwhile. `OUTBOX_MAX_ATTEMPTS=0` removes the cap (not recommended) |
| The same event arrives twice at a consumer | Delivery is at-least-once with a stable CloudEvents `id` | Compare ids | Consumers deduplicate on `id`; this service does so with `processed_events` |

## Consumer

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| Read models empty, no `consumer running` line | The consumer starts with `cmd/api` only; `cmd/mcp` and `cmd/a2a` never consume | api pod logs | Look at the api pod |
| Repeated retry logs on one topic (`handling failed; retrying the same message`, with `attempt`, `max_attempts`, `retry_in`) | A transient failure, usually Postgres down mid-handler. Backoff 200 ms doubling to 5 s, up to 5 attempts | `partition` and `offset` in the log | Restore Postgres. After 5 attempts the message goes to `<topic>.dlq` with `x-dlq-source-topic`, `-partition`, `-offset`, `x-dlq-error`, `x-dlq-failed-at` headers |
| WARN `dead-lettering an event that can never be applied` then ERROR `message can never be applied; dead-lettering it without retry` | A known event type whose `data` cannot be decoded or lacks a required id (`malformed event payload`) | The log names `type` and `event_id`; read the `.dlq` topic | Fix the producer. The message is committed past. |
| WARN `skipping a message that is not a valid CloudEvent` | The message is not CloudEvents 1.0 | `error` field | Nothing is retried or dead-lettered; fix the producer |
| WARN `... broker connection stalled or lost; fetching again after the backoff` | Idle TCP connection dropped (conntrack, sidecar). Recovers by redialling | `attempt` counter | Ignore a blip; after 10 consecutive failures the worker fails and the pod restarts |
| Consumer logs retries on a topic that does not exist yet | Topics are auto-created by the first writer; a producer that has not written yet leaves its topic missing | `kafka-topics --list` | Wait for the producer; this never makes `/readyz` fail |
| WARN `order placed with an unknown promise` | `CustomerOrderPlaced` referenced a promiseId this service never held (for example data from another environment) | `orderId`, `promiseId` in the log | Informational: the event is skipped |

## MCP and A2A

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| MCP `tools/list` fails without a session | Streamable HTTP needs `initialize` first; the `Mcp-Session-Id` response header must be sent on later calls, and `Accept: application/json, text/event-stream` | `curl -i` on `initialize` | See [Quickstart](/docs/overview/quickstart) |
| A2A error `-32009` `this version is not supported: requested "0.3", served "1.0"` (`VERSION_NOT_SUPPORTED`) | The client sent no `A2A-Version` header (treated as 0.3) or another version | Request headers | Send `A2A-Version: 1.0` |
| A2A answers a `problem` inside the message instead of a JSON-RPC error | By design: 400 request shape (including unknown fields and not exactly one of `orderId`/`promiseId`), 404 `not-found`, 422 `validation-failed`, 500 `internal-error` | The Data part `problem.status` | Fix the arguments |
| Agent Card advertises the wrong URL | `A2A_AGENT_URL` | `GET /.well-known/agent-card.json`, `supportedInterfaces[0].url` | Correct the variable and restart |
| `cmd/mcp` or `cmd/a2a` returns empty results while the api has data | Different `DATABASE_URL` (they read the database `cmd/api` owns) | env on each pod | Point all three at the same database |
| Calls get 401/403 | They do not: the surfaces carry no authentication; the in-cluster boundary is the access control. A 401 comes from something in front (a gateway, a client) | `curl` the Service directly | Look at the proxy |

## Telemetry

| Symptom | Cause | Check | Fix |
| --- | --- | --- | --- |
| No traces or metrics in Grafana | The Collector is absent or `OTEL_EXPORTER_OTLP_ENDPOINT` is wrong (default `localhost:4317`). Export never blocks, so nothing else breaks | `LOG_LEVEL=debug` shows `opentelemetry error` records; log `telemetry configured` shows the endpoint | Set the endpoint (chart `otel.endpoint`) |
| Log lines without `trace_id` | Written outside a span (boot, workers) or with the non-`Context` slog API | Expected | None |
