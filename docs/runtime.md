# Runtime: configuration, probes, shutdown, workers and the outbox

`cmd/api` is the only binary. It runs on real PostgreSQL and real Kafka; there is no
alternative runtime and no log sink. Next to the REST API it runs one Kafka consumer
(eight topics, one group), a clock sweeper and the outbox relay.

## Environment

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | required | Postgres DSN of the service's pool. A boot error names it when unset. |
| `MIGRATIONS_DATABASE_URL` | `DATABASE_URL` | Direct DSN for the boot migrations (golang-migrate holds a session advisory lock, which breaks behind a transaction-pooling proxy). |
| `KAFKA_BROKERS` | required | Comma-separated `host:port` list (consumer, relay and `/readyz`). A boot error names it when unset or blank. |
| `DELIVERY_PROMISE_CONSUMER_GROUP` | required | Stable group id of the consumer of the eight producer topics (a chart value, never derived from the pod name). |
| `EVENT_PUBLISHER` | `kafka` | Only `kafka` is accepted; any other value is a boot error. |
| `HTTP_ADDR` | `:8080` | Listen address. |
| `SWEEP_INTERVAL` | `15s` | How often the sweeper runs its four passes (Go duration or seconds). |
| `OUTBOX_RELAY_INTERVAL` | `1s` | Poll interval of the relay (Go duration or seconds). |
| `OUTBOX_MAX_ATTEMPTS` | `20` | Failures before the relay excludes a row; `0` = unlimited (ADR 0005). |
| `SHUTDOWN_DRAIN_DELAY` | `5s` | Wait between flipping `/readyz` to 503 and closing the listener; `0` disables. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |

Configuration errors are raised before anything is opened or bound. An invalid optional
duration logs a warning and uses the default.

## Boot and shutdown

1. Validate the environment. 2. Run the embedded migrations and ping Postgres, both with
exponential backoff (5 attempts, 1 + 2 + 4 + 8 s). 3. Start the consumer, the sweeper and
the outbox relay (none dials at construction; a broker that is down only makes fetches
and sends fail until it is back). 4. Serve.

On SIGINT/SIGTERM: `/readyz` flips to 503 `not_ready`, wait `SHUTDOWN_DRAIN_DELAY`, drain
HTTP (10 s), stop and await the consumer and the sweeper (10 s), stop and await the
relay LAST (10 s) so nothing they committed is stranded, close the pool. If any worker
returns on its own the process exits non-zero so the orchestrator restarts it. The sum
of the bounds is 5 + 10 + 10 + 10 = 35 s: the chart's `terminationGracePeriodSeconds`
must exceed it.

## Probes

- `GET /healthz` is liveness: 200 `{"status":"ok"}` while the process serves, even while
  draining or while a dependency is down.
- `GET /readyz` is 200 `{"status":"ready"}` only if the drain gate is open, Postgres
  answers a ping and a Kafka broker answers a metadata request. The request names no
  topics, so it never creates a topic; a fresh connection is dialled on every check.
  While draining it answers 503 `{"status":"not_ready"}`; while a dependency is down, 503
  `application/problem+json` of type `dependency-unavailable` naming it. Verdicts are
  cached 3 s, one round of checks is bounded to 2 s. The real-wiring test in
  `cmd/api/e2e_integration_test.go` stops Kafka and expects the 503.

## Consumer

- One consumer reads the eight producer topics (`retailnetwork.<producer>.events`: customer-ordering,
  order-sourcing, shipping-and-tracking, linehaul-transportation, last-mile-delivery,
  node-performance, node-topology, offer-availability) under `DELIVERY_PROMISE_CONSUMER_GROUP`.
  It fetches one message at a time and commits its offset only after the handler returned.
- Dispatch is on the FULL CloudEvents type; an unhandled type or a message that is not a
  CloudEvents 1.0 event is skipped. Decoding is tolerant: unknown fields are ignored.
- Every handler claims the CloudEvents `id` in `processed_events` in the same transaction as
  its effect, so a redelivery changes nothing.
- A transient failure (database down) is retried up to 5 attempts with a capped backoff and
  then published to `<topic>.dlq` with `x-dlq-*` headers; a payload that can never be applied
  is published there at once.

## The sweeper

Every `SWEEP_INTERVAL` it runs, in order, batches of 100 of: `ExpireOffers` (QUOTED offers
past `expiresAt`), `ReleaseExpiredHolds` (PROVISIONAL promises past `holdExpiresAt`, which
release their book capacity and raise `PromiseReleased`), `CloseBooks` (OPEN books at their
cutoff, ADR 0002 A13) and `RepriceAtRisk` (AT_RISK promises that could not be revised
when they were flagged, ADR 0002 A17/A19). A failing pass is logged and never stops the
others; the sweeper is the only place the clock drives the domain.

## The outbox in operation

- Events are written in the same transaction as the aggregates and published later, so a
  write is accepted while Kafka is down; `outbox_events.published_at IS NULL` rows wait and
  are published, in id order, when the broker is back.
- Integration events go to `retailnetwork.delivery-promise.events`, analytics-only events to
  `retailnetwork.delivery-promise.analytics`. Delivery is at-least-once with a stable
  CloudEvents `id`; consumers deduplicate.
- A row that failed `OUTBOX_MAX_ATTEMPTS` times is skipped (logged once at ERROR with id,
  topic, type and key) and stays in the table with `attempts` and `last_error`. Fix the
  cause, then re-arm it: `UPDATE outbox_events SET attempts = 0 WHERE id = <id>;`.

## Idempotency of writes

`Idempotency-Key` is required on every state-changing POST except the quote, where it is
optional (the middleware only runs when the caller sends a key). The key is scoped to
(route, key) and stored with a hash of the request; a repeat with the same body replays the
stored status and body (`Idempotency-Replayed` header), with another body it is `422
idempotency-key-reused`. The handler runs under a savepoint inside the key's transaction: a
2xx commits the aggregates, their outbox rows and the key together; a 4xx rolls the writes
back but stores and replays the problem; a 5xx or a panic rolls everything back and stores
nothing, so the same key can retry. Keys older than 24 hours are purged on the next keyed
write.
