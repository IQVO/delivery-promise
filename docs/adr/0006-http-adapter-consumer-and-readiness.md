# ADR 0006: the HTTP adapter, the consumer, the sweeper and readiness

## Status

Accepted (2026-10-23).

## Context

`cmd/api` has to serve the shopper's critical path (quote, promise) from local state only, keep that state current from eight producer topics, drive the clock-based parts of the model and say honestly when it cannot do its job.

## Decision

1. **REST.** A chi router (`internal/adapters/inbound/http`) serves the operations of `apis/openapi.yaml`: `POST /delivery-offers:quote`, `GET /delivery-offers/{offerId}`, `POST /promises`, `GET /promises?orderId=`, `GET /promises/{promiseId}`, `POST /promises/{promiseId}:confirm`, `POST /promises/{promiseId}:cancel`, `GET /promise-books?nodeId=&date=`, `POST /promise-policies`, `GET /promise-policies/current`. Errors are `application/problem+json`; the slug of a domain error is its kebab-cased name without `Err` (ADR 0002 A22). Handlers only decode, call one use case and encode.
2. **Idempotency.** `RequireIdempotencyKey` is applied route by route to the state-changing POSTs; the key is `(route, key)` and the handler runs in a savepoint of the key's transaction (aggregates, outbox rows and key commit together; a 4xx rolls the writes back but is replayed; a 5xx stores nothing). The quote takes the key optionally (ADR 0002 A23). Rows older than 24 hours are purged on the next keyed write.
3. **Consumer.** One consumer, one group (`DELIVERY_PROMISE_CONSUMER_GROUP`), eight topics (ADR 0003). The offset is committed after the handler returned; a handler claims the CloudEvents `id` in `processed_events` in its own transaction. A transient failure is retried 5 times with a capped backoff and then published to `<topic>.dlq`; a deterministic problem is dead-lettered at once when a sink exists, otherwise logged and skipped.
4. **Sweeper.** `SWEEP_INTERVAL` (default 15 s) runs `ExpireOffers`, `ReleaseExpiredHolds`, `CloseBooks` and `RepriceAtRisk` in batches of 100. A failing pass never stops the others.
5. **Readiness.** `/healthz` is liveness only. `/readyz` is ready only if Postgres answers a ping and a Kafka broker answers a metadata request that names no topics; verdicts are cached 3 s and a round is bounded to 2 s. While draining it answers 503 `not_ready`; a dependency outage is 503 `dependency-unavailable`. The real-wiring test (`cmd/api/e2e_integration_test.go`, tag `integration`) boots `run(ctx)` on Postgres and Kafka testcontainers, drives the API, stops Kafka and expects the 503.
6. **Shutdown order.** `/readyz` 503, drain delay, HTTP drain, consumer and sweeper, then the relay last, then the pool. A worker that returns on its own makes the process exit non-zero.

## Consequences

- A cold or partitioned service answers fewer offers, never a false one, and says it is not ready when Kafka is gone.
- Chart values: `terminationGracePeriodSeconds` > 35 s, a stable consumer group, and `MIGRATIONS_DATABASE_URL` pointing at a direct connection when Postgres sits behind a transaction pooler.
