---
paths:
  - "cmd/dp-projector/**"
  - "cmd/dp-reports/**"
  - "analytics/**"
  - "internal/analytics/**"
  - "internal/adapters/outbound/analyticsstore/**"
  - "internal/adapters/inbound/kafka/analytics_*.go"
  - "internal/adapters/inbound/reportshttp/**"
---

# Analytics read side (projector + reports)

Record: `docs/adr/0018-analytics-read-side.md`; promise coverage per region (the `promiseCoverage` block of
`/reports/quote-conversion`, `report.PromiseCoverage`, migration `0002_offer_coverage`): `docs/adr/0030-promise-coverage-per-region-in-the-quote-conversion-report.md`; request-level fields of the same block (`requestsAnswered`, `requestsWithPromise`, `requestsWithinDays`, `requestShare`, the best-effort `DeliveryRequestAnswered` fact, migration `0003_request_facts`): `docs/adr/0032-request-level-promise-coverage-from-a-best-effort-fact.md`. The read side consumes the facts this context already publishes; do not
change the encoder, the outbox or the `.events`/`.analytics` contracts from here.

- Source: ONLY this context's own two topics, `retailnetwork.delivery-promise.events` and
  `retailnetwork.delivery-promise.analytics` (`OwnTopics()`), never a sibling's topic. Seven projected types, dispatched on
  the FULL CloudEvents `type` (`internal/adapters/inbound/kafka/analytics_decode.go`): DeliveryOfferQuoted, PromiseMade,
  PromiseRevised, PromiseUnkeepable, PromiseKept, PromiseBroken, DeliveryRequestAnswered (a best-effort fact: a request-based share rests on the requests recorded, ADR 0032). The other six own types are acknowledged silently;
  anything else (and non-CloudEvents) is skipped with a sampled WARN.
- `cmd/dp-projector` is the ONLY writer of the SEPARATE analytical database (`ANALYTICS_DATABASE_URL`, `KAFKA_BROKERS`,
  `ANALYTICS_CONSUMER_GROUP` default `delivery-promise-analytics`, `ADMIN_ADDR` `:8091`). It never opens the OLTP
  database. `cmd/dp-reports` (`HTTP_ADDR` `:8092`) reads through a read-only pool (`ANALYTICS_READER_DATABASE_URL`,
  falling back to `ANALYTICS_DATABASE_URL`).
- Dedupe on the CloudEvents `id` and the fact write are ONE analytical-database transaction (`Projection.Apply`); test
  atomicity with a trigger that RAISEs (`postgres_integration_test.go`).
- Delivery policy: dead-letter ONLY deterministic poison (bad payload, subject mismatch, missing field, a cause outside
  LATE/LOST/RETURNED, Postgres class 22/23) to `retailnetwork.delivery-promise.analytics.dlq`; retry transient failures
  forever with capped backoff.
- Days are UTC days of the CloudEvents `time`; tier and basis are those of the PromiseMade (UNKNOWN until projected).
  PromiseMade and the judgements travel on different topics: keep the model order-independent (join at query time).
- `internal/analytics/report` is pure and imports no other internal package (arch test). SQL only counts, sums and
  `percentile_cont`; the cause vocabulary, ranks, window width and local-day rule (`WindowOf`, applied at projection
  time), means, rates, shares, range rules and the freshness lag live there, in `make mutation` and in the coverage
  scope. One contract suite (`analyticsstore/contract_test.go`) runs against `MemoryStore` and Postgres: change both
  twins together. Tiers sort byte-wise (`COLLATE "C"`) to match the twin.
- `analytics/migrations` is embedded (`analytics/migrations.go`), additive, with down files; never edit an applied
  migration, add the next number.
- Reports are unauthenticated, read-only GETs under `/reports/...` (plus `/healthz`, `/readyz`), camelCase, UTC days,
  `from`/`to` inclusive dates, problem `type` `https://retailnetwork.example/problems/<slug>`. Any route change edits
  `apis/openapi.yaml` (tag `reports`) and regenerates the REST reference in the same commit (`reportshttp/spec_test.go`
  pins paths and wire fields).
- Chart: the `analytics:` block is opt-in; keep `tests/render_test.sh` and `tests/test_service_selectors.py` green.
