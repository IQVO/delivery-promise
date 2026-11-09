# ADR 0018: Analytics read side

## Status

Accepted (2026-11-09). Additive: no published payload, topic, OLTP table, outbox row or REST route of `cmd/api`
changes. Adds two binaries (`cmd/dp-projector`, `cmd/dp-reports`), one analytical-database migration set
(`analytics/migrations`), four read-only report routes, two default-off chart components and a second, separate
database. Replicates the shape of the shipping-and-tracking pilot (its ADR 0012); roadmap F9.

## Context

Delivery Promise is the only context that knows what was promised and how each promise ended: it quotes the offers
(`DeliveryOfferQuoted`), makes the promise (`PromiseMade`), re-promises it (`PromiseRevised`), gives up on it
(`PromiseUnkeepable`) and judges it (`PromiseKept`, `PromiseBroken`). Three network questions can only be answered
here, as a trend: how reliable the promises are (kept rate per tier and basis), how precise they are (how wide the
window is, how often it is a single local day, how often it had to move) and how many quotes become promises. Nothing
answers them today: the facts leave as CloudEvents and the OLTP tables hold the current state only.

Unlike the pilot, this context publishes on BOTH of its topics (ADR 0004): six integration events on
`retailnetwork.delivery-promise.events` (PromiseAtRisk, PromiseRevised, PromiseKept, PromiseBroken, PromiseUnkeepable,
PromiseBookExhausted) and six analytics-only events on `retailnetwork.delivery-promise.analytics` (PromiseMade,
PromiseConfirmed, PromiseReleased, PromiseCancelled, DeliveryOfferQuoted, PromisePolicyPublished). Every field the
reports need is already on one of them, so no field is added and nothing is fanned out.

### The contract (as given)

- `cmd/dp-projector` (admin `:8091`) and `cmd/dp-reports` (`:8092`); consumer group `delivery-promise-analytics`;
  DLQ `retailnetwork.delivery-promise.analytics.dlq`; env keys identical to the pilot; Kong PathPrefix
  `/api/delivery-promise/reports` rewritten to `/reports`.
- The projector consumes ONLY `retailnetwork.delivery-promise.events` and/or `.analytics`.
- `GET /reports/promise-reliability?from=&to=` (UTC day of judgement): per day, tier and basis
  (SCHEDULED/MEASURED/NOMINAL): judged = kept + broken; keptRate = kept / judged; broken split by cause (the as-built
  PromiseBroken cause vocabulary); lateBy p50/p90 in minutes over the broken LATE ones.
- `GET /reports/promise-precision?from=&to=` (UTC day of PromiseMade): per day and tier: promisesMade; window width
  in minutes, mean/p50/p90; exactDayShare = promises whose window starts and ends on the same LOCAL calendar day in
  the window's timeZone (ADR 0011). If timeZone is absent, use UTC and count it in a separate `zoneUnknown` count;
  revisedShare = promises with at least one PromiseRevised / promisesMade; unkeepableCount.
- `GET /reports/quote-conversion?from=&to=`: per day and tier, offersQuoted (DeliveryOfferQuoted, if published with
  a tier; else only the per-day total) against promisesMade, plus conversion = promisesMade / offersQuoted. Drop it,
  and say so in the ADR, if DeliveryOfferQuoted is not published.
- `GET /reports/freshness`.
- If a needed field is not on any event of this context, do NOT consume a sibling topic: add the field additively to
  an analytics-only event, fan the fact onto the analytics stream, or drop the metric, and document the choice.

## Decision

### 1. What is projected

The projector reads ONLY this context's own two topics, `retailnetwork.delivery-promise.events` and
`retailnetwork.delivery-promise.analytics`, in ONE consumer group. It never reads a sibling's topic. Every field the
KPIs need is on these events, so no field is added, no fact is fanned out (`FanoutEncoder` is not needed) and the
outbox (`outbox_events.event_id UNIQUE`) is unchanged. `DeliveryOfferQuoted` IS published, with a `tier`, so the
quote-conversion report is kept, per day AND tier.

Six types are projected, dispatched on the FULL CloudEvents `type`:

| type | topic | payload fields read | fact |
| --- | --- | --- | --- |
| `com.retailnetwork.commerce.delivery-promise.deliveryoffer.DeliveryOfferQuoted` | analytics | `offerId` (= subject), `tier` | `offers_quoted` |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseMade` | analytics | `promiseId` (= subject), `offerId`, `tier`, `basis`, `deliverByEarliest`, `deliverByLatest`, `timeZone` (optional) | `promises_made` |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseRevised` | events | `promiseId` (= subject), `revisionNo` | `promise_revisions` |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseUnkeepable` | events | `promiseId` (= subject) | `promise_unkeepable` |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseKept` | events | `promiseId` (= subject) | `promise_judgements` (`KEPT`) |
| `com.retailnetwork.commerce.delivery-promise.promise.PromiseBroken` | events | `promiseId` (= subject), `cause`, `lateByMinutes` | `promise_judgements` (`BROKEN`) |

The other six published types (`com.retailnetwork.commerce.delivery-promise.promise.PromiseConfirmed`,
`com.retailnetwork.commerce.delivery-promise.promise.PromiseAtRisk`,
`com.retailnetwork.commerce.delivery-promise.promise.PromiseCancelled`,
`com.retailnetwork.commerce.delivery-promise.promise.PromiseReleased`,
`com.retailnetwork.commerce.delivery-promise.promisebook.PromiseBookExhausted`,
`com.retailnetwork.commerce.delivery-promise.promisepolicy.PromisePolicyPublished`) are acknowledged and not
projected: no report needs them.

Tables of the analytical database (`analytics/migrations/0001_promise_facts`):

- `analytics_processed_events (event_id PK, event_type, occurred_at, applied_at)`: the dedupe set; `occurred_at` is
  the CloudEvents `time`, the basis of the freshness lag.
- `offers_quoted (offer_id PK, tier, quoted_at, event_id)`.
- `promises_made (promise_id PK, offer_id, tier, basis, made_at, deliver_by_earliest, deliver_by_latest, time_zone,
  window_minutes, exact_day, zone_known, event_id)`.
- `promise_revisions (promise_id, revision_no, revised_at, event_id; PK (promise_id, revision_no))`.
- `promise_unkeepable (promise_id PK, unkeepable_at, event_id)`: raised at most once per promise (the domain's
  `unkeepable` flag).
- `promise_judgements (promise_id PK, verdict IN (KEPT, BROKEN), cause IN ('', LATE, LOST, RETURNED),
  late_by_minutes, judged_at, event_id)`: a promise is judged exactly once.

`PromiseMade` and `DeliveryOfferQuoted` travel on `.analytics`, the revisions and judgements on `.events`, so they
arrive in any order. The model is order-independent: it stores facts and the reports join them at query time. A
judgement whose `PromiseMade` was not (yet) projected reports under tier `UNKNOWN` and basis `UNKNOWN`.

### 2. KPI definitions

**Days.** Days are UTC calendar days of the CloudEvents `time` of the counted event. Here that IS the business
instant: every one of these events is raised by its use case at the moment of the fact (`header(now)`: the quote, the
promise, the judgement), and no payload carries a separate instant for it (`PromiseBroken` LOST/RETURNED has no
`deliveredAt`). "Day of judgement" is the `time` of `PromiseKept`/`PromiseBroken`; "day of PromiseMade" is the `time`
of `PromiseMade`; the quote day is the `time` of `DeliveryOfferQuoted`.

**Tier and basis are as made.** They come from `PromiseMade`. A re-promise may move a promise to a slower tier, but
`PromiseRevised` carries neither tier nor basis, so a promise is reported under the tier and basis it was made with.

`GET /reports/promise-reliability?from=&to=`, per UTC day of judgement, tier and basis:

- `kept`, `broken` = judgements of that day; `judged` = `kept` + `broken`; `keptRate` = `kept` / `judged`;
- `brokenByCause` = `{late, lost, returned}`, the as-built `PromiseBroken.cause` vocabulary (`LATE`, `LOST`,
  `RETURNED`); a cause outside it is poison;
- `lateByMinutes` = `{p50, p90}`, the continuous percentiles (`percentile_cont`) 0.5 and 0.9 of `lateByMinutes` over
  the broken `LATE` judgements, `null` when there is none.

`GET /reports/promise-precision?from=&to=`, per UTC day of PromiseMade and tier:

- `promisesMade`;
- `windowMinutes` = `{mean, p50, p90}` of the window width `deliverByLatest - deliverByEarliest` in minutes;
- `exactDayShare` = promises whose `deliverByEarliest` and `deliverByLatest` fall on the same LOCAL calendar day in the
  window's `timeZone` (ADR 0011) / `promisesMade`. When `timeZone` is absent, or is not a zone the projector can load,
  the window is read in UTC and the promise is ALSO counted in `zoneUnknown`;
- `revisedShare` = promises with at least one projected `PromiseRevised` (at any time) / `promisesMade`;
- `unkeepableCount` = promises with a projected `PromiseUnkeepable` (at any time).

The width, the exact-day flag and the zone-known flag are derived by the pure package when the projector applies
`PromiseMade` and stored with the fact, so SQL only counts and sums them.

`GET /reports/quote-conversion?from=&to=`, per UTC day and tier:

- `offersQuoted` = `DeliveryOfferQuoted` of that day and tier; `promisesMade` = `PromiseMade` of that day and tier;
- `conversion` = `promisesMade` / `offersQuoted`, `null` when no offer was quoted that day and tier (a promise made
  just after midnight from an offer quoted the evening before). Same-day counts, not an offer-to-promise cohort.

`GET /reports/freshness`: `asOf` = the newest CloudEvents `time` the projection applied, `lagSeconds` = now - `asOf`
(never negative); both `null` until the first event.

Range rules (as the pilot): `from` and `to` are UTC dates (`YYYY-MM-DD`), both inclusive; both omitted = the 30 days
ending today; only `to` = the 30 days ending on `to`; only `from` = `from` .. today; `from` after `to` or a range over
366 days is a 400 problem `invalid-report-range`. Days without data are omitted; empty results are empty arrays. Wire
fields are camelCase (ADR 0012, the REST guideline); the routes are collection-shaped reports,
`x-pagination-exempt: report`.

SQL only counts, sums and takes `percentile_cont`. The cause vocabulary, the percentile ranks, the window width, the
local-day rule, the means, rates and shares, the range rules and the freshness lag live in `internal/analytics/report`,
a pure package that imports no other internal package (arch test), is in the coverage scope and in `make mutation`.
ONE contract suite runs against the in-memory twin and against real Postgres.

### 3. Delivery policy (the projector)

- Consumer group `ANALYTICS_CONSUMER_GROUP` (default `delivery-promise-analytics`), fixed and stable, over both own
  topics; at-least-once, the offset is committed only after the message was handled. A brand-new group starts at the
  earliest offset, so the model rebuilds from retained history.
- **Dedupe and projection in ONE analytical-database transaction**: `INSERT ... ON CONFLICT DO NOTHING` into
  `analytics_processed_events`; the fact is written only if that insert claimed the id. A failure leaves neither; a
  replay of an applied id is a no-op.
- Not a CloudEvents 1.0 message, or a CloudEvent of a type that is neither projected nor one of the six acknowledged
  types: skipped and committed past with a **sampled WARN** (the first, then at most one per minute with the count
  suppressed).
- **Dead-letter ONLY deterministic poison**: an undecodable payload, a subject that is not the aggregate id, a missing
  required field or timestamp, a window that ends before it starts, a `cause` outside the vocabulary, or a store
  rejection in Postgres class 22 (data exception) or 23 (integrity violation). The raw message goes to
  `retailnetwork.delivery-promise.analytics.dlq` with `x-dlq-*` headers, then the offset is committed. A failed DLQ
  write is retried, so poison is never lost.
- **Transient failures** (database down, timeout, any other error) are retried forever with capped exponential backoff
  (200ms doubling to 5s) and never dead-lettered. The cost is a blocked partition during an outage, visible in the logs
  and in `/reports/freshness`.

### 4. The binaries

- `cmd/dp-projector`: the ONLY writer of the analytical database. Env: `ANALYTICS_DATABASE_URL` (required, a direct
  DSN: it runs the embedded migrations), `KAFKA_BROKERS` (required), `ANALYTICS_CONSUMER_GROUP`, `ADMIN_ADDR` (default
  `:8091`, `/healthz` and `/readyz`), `LOG_LEVEL`, `OTEL_SERVICE_NAME` (default `delivery-promise-projector`),
  `OTEL_EXPORTER_OTLP_ENDPOINT`, `SERVICE_VERSION`, `ENVIRONMENT`. It never opens the OLTP database. The migrations are
  embedded (`analytics/migrations.go`), so there is no `ANALYTICS_MIGRATIONS_PATH`.
- `cmd/dp-reports`: read-only HTTP on `HTTP_ADDR` (default `:8092`): `GET /reports/promise-reliability`,
  `GET /reports/promise-precision`, `GET /reports/quote-conversion`, `GET /reports/freshness`, `/healthz`, `/readyz`
  (analytical-database ping). It reads `ANALYTICS_READER_DATABASE_URL`, falling back to `ANALYTICS_DATABASE_URL` for
  single-role local runs, through a pool whose sessions default to read-only transactions with a 15s statement
  timeout. No Kafka, no OLTP database, no call to any sibling, no auth (fleet rule). Errors are problem+json. The routes
  live in `apis/openapi.yaml` under the tag `reports` with a per-path server on `:8092`.
- Both ship in the one image (`/app/dp-projector`, `/app/dp-reports`).

### 5. Why a separate analytical database

The projection is a derived copy that can be dropped and rebuilt from the topics. Keeping it out of the OLTP database
means a heavy report cannot take OLTP connections, the two schemas evolve independently, the reports role can be
read-only on a database that holds nothing transactional, and the projector holds no OLTP credentials. Both DSNs are
direct (PgBouncer is not involved: the projector runs migrations).

### 6. Packaging

The chart gains an opt-in `analytics:` block (default `enabled: false`; the default render is byte-identical):
components `analytics-projector` (Deployment, no Service) and `analytics-reports` (Deployment + ClusterIP Service, an
opt-in Kong HTTPRoute `/api/delivery-promise/reports` rewritten to `/reports`), an `analytics` Secret or
`analytics.database.existingSecret`. Render-time guards refuse `analytics.enabled` without a DSN source, without
`kafka.brokers` or without a consumer group. The projector pod never gets the OLTP `DATABASE_URL`; the reports pod gets
neither Kafka nor any DSN but the reader's.

## Consequences

- Both own topics gain one service-owned consumer group (`delivery-promise-analytics`); their contracts are unchanged.
  The reports are only as fresh as the projector, which `/reports/freshness` makes observable.
- The DLQ name `retailnetwork.delivery-promise.analytics.dlq` is also what the fleet's `<topic>.dlq` rule would give a
  sibling consumer of `.analytics`. The analytics topic has no other consumer by definition (ADR 0004), so the
  projector owns that DLQ alone; a future consumer of `.analytics` must pick another DLQ.
- The model starts at the earliest retained offset of each topic. A promise whose `PromiseMade` aged out before the
  projector first ran reports its judgement under `UNKNOWN`/`UNKNOWN`, and its revisions and unkeepability are not in
  any precision cohort.
- Tier and basis are as made: a promise re-promised to a slower tier stays under its original tier.
- Quote conversion compares same-day counts; it is not bounded by 1 on a day where promises from the previous day's
  offers dominate.
- A deploy needs a second database, two roles (projector read-write, reports read-only), the DLQ topic and the
  consumer group above (`warehouse-infra`).

## Alternatives considered and rejected

- **Project into the OLTP database**: no isolation of load, schema or credentials.
- **Read PackageDelivered/Lost/Returned from shipping-and-tracking**: a sibling's topic; the judgements this context
  publishes already carry the verdict, the cause and the lateness.
- **Add a `judgedAt` or `tier` to PromiseKept/PromiseBroken, or fan them onto `.analytics`**: the CloudEvents `time`
  is the judgement instant and `PromiseMade` carries the tier and basis, so a contract change would add no information.
- **Offer-to-promise cohort for conversion (join on `offerId`)**: the contract asks for offers quoted against
  promises made per day; a cohort would also need the offer retention to outlive the hold.
- **Dead-letter after N attempts for every failure**: drops analytics for events that failed only because the database
  was briefly down.
