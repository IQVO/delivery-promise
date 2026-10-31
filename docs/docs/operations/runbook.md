# Delivery Promise runbook

Operational runbook for the Delivery Promise service (a domain-driven design
study; all names are synthetic — see the disclaimer in `README.md`). The
service tells a shopper, before and at purchase, the date a package will
arrive, and makes that date true by construction: a delivery offer is quoted
only from a feasible witness path (node cutoff, scheduled departures, delivery
wave) that has remaining capacity, a promise holds that capacity until the
order is placed or the hold expires, and every promise is judged kept or
broken (`README.md`, ADRs in `docs/adr/`).

## Runtime shape

One `api` Deployment (`charts/delivery-promise`, single `cmd/api` process,
one replica by default, optional HPA off) runs four things side by side:

- **REST API** on `:8080` (chi, problem+json, no auth): quote/read offers,
  make/confirm/cancel/read promises, promise books, policy — routes in
  `README.md`, contracts in `apis/openapi.yaml`. In the local cluster it is
  reached through the Kong route `/api/delivery-promise` with the prefix
  stripped.
- **Transactional-outbox relay** (`cmd/api/workers.go`,
  `internal/adapters/outbound/outbox/relay.go`): drains `outbox_events` to
  Kafka every `OUTBOX_RELAY_INTERVAL` (default 1s), batches of 100, one
  message at a time in `id` order, `FOR UPDATE SKIP LOCKED` (safe with >1
  replica).
- **One Kafka consumer** (`internal/adapters/inbound/kafka/`) over the eight
  integration topics under the stable group `DELIVERY_PROMISE_CONSUMER_GROUP`
  (`delivery-promise-consumer` in this cluster); handlers keep local read
  models (`rm_*` tables) via the inbox table `processed_events`.
- **Sweeper** (`cmd/api/workers.go`): every `SWEEP_INTERVAL` (default 15s)
  runs `ExpireOffers`, `ReleaseExpiredHolds`, `CloseBooks`, `RepriceAtRisk`
  in batches of 100; a failing pass is logged (`sweep pass failed`) and never
  stops the others.

Shutdown is sequential (`/readyz` 503 → drain delay → HTTP → consumer →
sweeper → relay last → pool); any worker that returns on its own exits the
process non-zero so Kubernetes restarts it (`cmd/api/main.go`).

## Dependencies

- **Postgres** — the only state store; no in-memory mode. `DATABASE_URL` is
  the pooled DSN (PgBouncer in this cluster). Migrations run at boot over
  `MIGRATIONS_DATABASE_URL`, a **direct** (non-pooled) DSN, because
  golang-migrate takes a session advisory lock a transaction pooler cannot
  honour (`docs/runtime.md`); both keys come from the Secret
  `delivery-promise-db`. Boot retries Postgres 5 times (1+2+4+8s).
- **Kafka** — `KAFKA_BROKERS`, comma-separated. Publishes
  `retailnetwork.delivery-promise.events` (integration events) and
  `retailnetwork.delivery-promise.analytics`
  (`internal/adapters/outbound/kafka/encoder.go`); consumes the eight
  `retailnetwork.<producer>.events` topics
  (`internal/adapters/inbound/kafka/dispatch.go`). Topics are auto-created by
  the first writer (`AllowAutoTopicCreation`); nothing pre-creates them.
- **customer-ordering** calls this service synchronously (`deliveryPromiseUrl`
  there): `POST /promises/{id}:confirm` inside checkout, and
  `POST /promises/{id}:cancel` as checkout compensation. A latency or outage
  here is felt in that checkout path.

## Health

- `GET /healthz` — liveness: `200 {"status":"ok"}` while the process serves,
  even while draining or with dependencies down (`cmd/api/main.go`).
- `GET /readyz` — readiness: `200 {"status":"ready"}` only if the drain gate
  is open, Postgres answers a ping and a Kafka broker answers a metadata
  request naming no topic. 503 `not_ready` while draining; 503
  `application/problem+json` type `dependency-unavailable` naming the dead
  dependency. Verdicts cached 3s, one round bounded to 2s (ADR 0006).
- **Are writes still accepted when a dependency is down?** With **Kafka**
  down: yes — verified by `cmd/api/e2e_integration_test.go` (stops Kafka:
  `/readyz` 503 naming kafka, `/healthz` 200, a write still succeeds); events
  queue in `outbox_events` and publish when the broker returns. With
  **Postgres** down: no — every write is a Postgres transaction, so writes
  fail 5xx and `/readyz` flips to 503 naming postgres.

## Configuration

Every variable the binary reads is in `cmd/api/config.go` / `workers.go`; the
chart renders exactly these (`charts/delivery-promise/values.yaml`). Boot
errors name the missing variable and happen before the listener binds.

| Variable | Default | Set by chart value |
| --- | --- | --- |
| `DATABASE_URL` | required | `database.url` or Secret key `DATABASE_URL` (`database.existingSecret` = `delivery-promise-db` here) |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | `database.migrationsExistingSecretKey` (key `MIGRATIONS_DATABASE_URL`, same Secret, `optional: true`) |
| `KAFKA_BROKERS` | required | `kafka.brokers` |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | required | `config.consumerGroup` (stable, service-prefixed, never the pod name) |
| `EVENT_PUBLISHER` | `kafka` (only value) | `config.eventPublisher` |
| `HTTP_ADDR` | `:8080` | `config.httpAddr` (keep `service.targetPort` in step) |
| `LOG_LEVEL` | `info` | `config.logLevel` |
| `SWEEP_INTERVAL` | `15s` | `config.sweepInterval` |
| `OUTBOX_RELAY_INTERVAL` | `1s` | `config.outboxRelayInterval` |
| `OUTBOX_MAX_ATTEMPTS` | `20` (`0` = unlimited) | `config.outboxMaxAttempts` |
| `SHUTDOWN_DRAIN_DELAY` | `5s` (`0` disables) | `config.shutdownDrainDelay` |

Durations accept Go form (`"500ms"`, `"2s"`) or plain seconds; malformed or
non-positive values warn and use the default. The chart refuses a
`terminationGracePeriodSeconds` that does not exceed drain delay + 40s
(default 60s) — a shorter grace SIGKILLs the drain mid-way.

## Alerts and dashboards

- Grafana dashboard uid **`warehouse-delivery-promise`**.
- Pod-restart alerts exist for this Deployment; the process exits non-zero on
  any worker failure, so a wedged consumer or relay surfaces as restarts.
- Logs: Grafana Loki, `app=delivery-promise` (JSON slog on stdout).
- **Honest state:** no business metrics are instrumented yet — there are no
  Prometheus counters/histograms for quotes, promises, outbox depth or
  consumer lag in the code. Business-metric panels on the dashboard stay
  empty until the service instruments itself; until then, operate from logs,
  probes and the pod-restart alert.

## Failure modes

**Pod crash-loop**
Symptom: restarts, pod not ready. Diagnosis: `kubectl -n warehouse-systems
logs deploy/delivery-promise` — boot errors name the missing/invalid variable
(`DATABASE_URL`, `KAFKA_BROKERS`, `DELIVERY_PROMISE_CONSUMER_GROUP`,
`EVENT_PUBLISHER=<x> is not supported`) before the listener binds; a worker
that returned on its own logs `worker stopped`. Action: fix the env/Secret
(chart value), then `kubectl -n warehouse-systems rollout restart
deploy/delivery-promise`. If Postgres is unreachable at boot, migrations
retry 5 times (~15s) then exit — check the database first.

**Postgres unreachable**
Symptom: `/readyz` 503 `dependency-unavailable` naming postgres; writes 5xx;
sweeper logs `sweep pass failed`; relay logs `outbox relay pass failed`.
`/healthz` stays 200. Diagnosis: check PgBouncer and Postgres; `psql` the DSN
from Secret `delivery-promise-db`. Action: restore the database — the service
needs no restart; everything retries on its interval and `/readyz` recovers
within its 3s verdict cache. Queued outbox rows are untouched.

**Kafka unreachable**
Symptom: `/readyz` 503 naming kafka; relay logs `outbox relay pass failed` on
each pass; consumer fetches retry. Diagnosis: broker reachability from
`KAFKA_BROKERS`. Action: restore Kafka. **REST writes are still accepted and
2xx** (verified in `cmd/api/e2e_integration_test.go`): events accumulate in
`outbox_events` (`published_at IS NULL`) and publish in `id` order once the
broker is back; monitor depth with `SELECT count(*) FROM outbox_events WHERE
published_at IS NULL;`.

**Outbox relay stuck / row parked after the attempt cap**
Symptom: ERROR log `outbox relay: row reached the attempt cap and is excluded
from future claims; re-arm it with attempts = 0 after fixing the cause` with
`outbox_id`, `topic`, `event_type`; later rows overtake the parked one.
Diagnosis: inspect the parked row and its recorded cause:
`SELECT id, event_type, topic, attempts, last_error FROM outbox_events WHERE
published_at IS NULL AND attempts >= 20;` (the `outbox_events` table and its
`published_at`, `attempts`, `last_error` columns are defined in
`internal/adapters/outbound/postgres/migrations/0001_delivery_promise_schema.up.sql`).
Common cause: a first write to a topic auto-creating it
(`UnknownTopicOrPartition` — ordinary, self-heals). Action: fix the cause,
then re-arm exactly the parked rows:
`UPDATE outbox_events SET attempts = 0 WHERE id = <id>;` — the next relay
pass claims and publishes them with the same CloudEvents id (at-least-once).

**Consumer lag or retry logs**
Symptom: repeated handler retries on one topic/partition; lag grows.
Diagnosis: a consumed topic whose producer has not written yet does not exist
(topics are auto-created by the first writer), so the consumer only logs
retries until that producer starts — confirm with the producer before
debugging this side. A transient failure (Postgres down mid-handler) is
retried up to 5 attempts then published to `<topic>.dlq` with `x-dlq-*`
headers (`internal/adapters/inbound/kafka/deadletter.go`); a deterministic
problem (not CloudEvents 1.0, unknown type, malformed payload) never
dead-letters — it is skipped and the offset committed past it. Action: check
`{topic}.dlq` for poison messages, fix upstream payloads, and rely on
`processed_events` idempotency: redelivering an event id is a no-op.

## Operational actions

- Restart: `kubectl -n warehouse-systems rollout restart
  deploy/delivery-promise` (graceful: up to ~45s drain, 60s grace period).
- Logs: Grafana Loki `app=delivery-promise`, or `kubectl -n
  warehouse-systems logs deploy/delivery-promise`.
- Probes from inside the cluster: `kubectl -n warehouse-systems port-forward
  svc/delivery-promise 8080:80`, then `curl localhost:8080/healthz` and
  `/readyz`.
- Check outbox depth and parked rows (SQL above) against the Postgres behind
  PgBouncer.
- Scale: `replicaCount` / `autoscaling.api` in the chart — safe for the relay
  (`SKIP LOCKED`), writes and consumer (stable group + inbox dedupe); replays
  are no-ops (stable CloudEvents ids; `processed_events` dedupes).
