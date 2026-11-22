# ADR 0032: Request-level promise coverage from a best-effort fact

## Status

Accepted (2026-11-19). Refines ADR 0019 (the estimate read path stays free of any write that can fail or slow a response) and
completes ADR 0030 (section 1 named this fact and left it as a separate decision). Additive: one new analytics-only
CloudEvents type, one new analytical-database migration (`analytics/migrations/0003_request_facts`), new optional fields on
an existing report block, one new optional env switch that is OFF by default. No existing event, topic, REST route, outbox
row, OLTP table or required chart value changes. The bodies of ADR 0019 and ADR 0030 are not edited.

## Context

ADR 0030 reports, per UTC day and region, how many offers quoted carry a promise within 1, 2, 3 and 5 days. Its unit is the
offer quoted (`DeliveryOfferQuoted`), the only own fact with both a region and a promised window. It states the cost: a
request answered with no offer, and every estimate, leave no event, so the report cannot say what share of the delivery
requests the service answered got a promise. It names the fact that would close the gap, an analytics-only
`DeliveryRequestAnswered`, and does not add it because it would put an outbox write on the estimate read path.

ADR 0019 keeps `POST /delivery-estimates` safe: it stores nothing, writes no outbox event and needs no `Idempotency-Key`.
It serves listing-page traffic, up to 50 items per call. A synchronous transactional write on that path would make the
response depend on Postgres write latency and failure, and would turn a read into a write that competes with quoting. That
is the reason ADR 0030 declined, and it still holds for a synchronous write.

What the plan needs is a request-level share (requests answered with a promise of N days or less, over requests answered),
and it tolerates a small, counted loss in a coverage ratio. That tolerance is what makes a non-transactional hand-off
acceptable.

## Decision

### 1. A new analytics-only fact

`DeliveryRequestAnswered` (CloudEvents type
`com.retailnetwork.commerce.delivery-promise.deliveryrequest.DeliveryRequestAnswered`, stream `analytics`, subject = a
fresh opaque `requestId`) is raised once per quote (`POST /delivery-quotes`) or estimate (`POST /delivery-estimates`)
request answered with 200, whether or not a promise came back. Payload (camelCase, optional fields omitted):
`requestId`, `kind` (`QUOTE` | `ESTIMATE`), `destinationRegion`, `itemCount`, `answered` (at least one promised option) and
`shortestPromisedDays?` (omitted without a promise). It is listed in the ADR 0004 catalogue and in `apis/asyncapi.yaml`
(additive). The type is analytics-only: nothing outside this context consumes it, and only
`cmd/dp-projector` reads it.

It is a pure domain fact (`internal/domain/requestfact`): the region and the shortest promised days are computed there, not
in SQL or in the adapter. A request with no known region (a pickup-point quote that got no offer) leaves no fact.

No PII: the only part of the destination that is carried or stored is the region, the first two characters of the postal
prefix (as in ADR 0030). No full prefix, address, shopper, order or item id is on the event or in the table.

### 2. Hand-off: after the response, bounded, best effort

The REST adapter hands the fact over only after the whole response was written, and only for a 200 (a refusal, a failure,
a replayed idempotency key or a failed commit leaves no fact). The hand-off goes to a **bounded in-memory queue**
(`internal/application/requestfacts`, `Queue`), drained by **a single background worker** (`Queue.Run`, started by
`cmd/api` next to the relay and the sweeper).

- `Offer` never blocks, never returns an error and never panics. When the queue is full the NEWEST fact is dropped and
  counted (`reason=queue_full`).
- The worker wakes every flush interval, writes the facts queued at that moment one by one, each as **one analytics-only
  outbox row in its own short transaction** (`OutboxWriter`, the same unit of work and outbox as every other published
  event, so the existing relay publishes it with the same CloudEvents envelope), bounded by a 2 s write timeout.
- A write that fails, panics or times out drops that fact and counts it (`reason=write_failed`); there is no retry and no
  parking. A panic anywhere in the hand-off is recovered (`reason=internal_error`). Facts still queued when the final drain
  (5 s) after shutdown ends are counted (`reason=shutdown`).
- Drops are logged at WARN at most once every 10 s with the number dropped since the last log.
- The worker follows the policy of the other workers: if it returns on its own, the process exits non-zero and Kubernetes
  restarts it.
- No response is ever changed by the fact: status, headers and body are byte for byte the same with the feature on, off, with
  a full queue, with an erroring outbox and with a blocking outbox (proved by tests over every kind of writer).

Settings (all optional; only the first changes behaviour):

| Env | Default | Meaning |
| --- | --- | --- |
| `DELIVERY_REQUEST_FACTS_ENABLED` | `false` | Switches the fact on. Unset, empty or not a boolean (a typo warns) is OFF. |
| `DELIVERY_REQUEST_FACTS_QUEUE_SIZE` | `1000` | Capacity of the queue, 1 to 100000; out of range warns and takes the default. |
| `DELIVERY_REQUEST_FACTS_FLUSH_INTERVAL` | `1s` | How often the worker drains (Go duration or seconds). |

Two counters, `deliverypromise_request_facts_written_total` and `deliverypromise_request_facts_dropped_total{reason}`,
make the loss visible.

The Helm chart renders the three variables from `config.requestFactsEnabled` (default `false`),
`config.requestFactsQueueSize` and `config.requestFactsFlushInterval`; none is required, `ci/default-values.yaml` is
unchanged, and the chart refuses an out-of-range value at render.

### 3. Best-effort loss is by design

Losing a fact (a full queue, a failed write, a pod killed with facts queued) lowers the count of the request-based fields
and nothing else: no promise, quote or estimate is affected, and nothing is retried or reconciled. A request-level share
therefore rests on "the requests the service recorded", not an exact "requests asked". A sustained non-zero
`request_facts_dropped_total` rate means it rests on noticeably fewer. If an exact count is ever needed it needs a different
design (a transactional write on the request path, or request logs), which is a separate decision.

### 4. The report: offers-based and request-based fields side by side

`GET /reports/quote-conversion` keeps `from`, `to`, `days` and the `promiseCoverage` block of ADR 0030 exactly as they are.
Each region row of `promiseCoverage` gains four fields. Nothing is renamed or removed.

| Family | Fields | Unit | Source |
| --- | --- | --- | --- |
| offers-based (ADR 0030) | `requests`, `answeredWithPromise`, `answeredWithinDays[]` | the offer quoted | `DeliveryOfferQuoted` |
| request-based (this ADR) | `requestsAnswered`, `requestsWithPromise`, `requestsWithinDays[]`, `requestShare` | the quote or estimate request answered | `DeliveryRequestAnswered` |

- `requestsAnswered` counts the requests recorded for the region that UTC day, with or without a promise;
  `requestsWithPromise` those with at least one promised option; `requestsWithinDays` is cumulative over the documented day
  list `[1, 2, 3, 5]` (`report.CoverageDays`) of the requests whose SHORTEST promised day count across the answered options
  is within N, each with `count` and `share` = `count / requestsAnswered`; `requestShare` = `requestsWithPromise /
  requestsAnswered`. Both shares are `null` when `requestsAnswered` is 0.
- Per UTC day, the ten regions with most requests are listed and the rest fold into one row `other`, as in ADR 0030. A day's
  regions are ranked by `requestsAnswered`, then by `requests`, then by region (byte order), so a region with request facts
  and no offer is listed too. A day with neither offers nor request facts is omitted.
- **The request-based fields exist only from the day the flag is switched on.** Before that day, and while the flag is off, they
  are `0` and the shares are `null`; they are never backfilled (the fact did not exist). The offers-based fields are
  unaffected by the flag.
- Bucketing, the top-10 bound, the fold, the cumulative counts and the shares live in `internal/analytics/report` (pure, in
  mutation testing); SQL only counts per day, region, answered flag and shortest promised days.

### 5. Storage and rollout

`analytics/migrations/0003_request_facts` adds one table, `requests_answered` (one row per request id, the first fact of an
id wins, `ON CONFLICT DO NOTHING`). Nothing is dropped or rewritten; `0001` and `0002` are untouched.

Rollout order, each step safe on its own:

1. Deploy the **projector** first (`cmd/dp-projector`). It applies the migration and learns to project the new type. Until the
   fact is on, it sees none. A projector that does not know the type would skip it with a warning, which is why it goes first.
2. Deploy the **reports** binary (`cmd/dp-reports`). It adds the four fields (zeros and `null` until facts arrive).
3. Deploy `cmd/api` with the flag **off** (the default; the chart default is `false`, no new required value). Behaviour is
   unchanged.
4. Switch `config.requestFactsEnabled=true` when ready, and watch `deliverypromise_request_facts_dropped_total`.

Switching it off again stops the facts; rows already published stay and are projected normally. The consumers that pin
`apis/openapi.yaml` or `apis/asyncapi.yaml` only gain optional response properties and an analytics-only type; they refresh
their pin after merge so the pinned-contract gate stays green.

## Consequences

- Request-level promise coverage per region, per UTC day, including requests answered with no promise, with no call to a
  sibling context, no new topic and no PII beyond a two-character postal prefix.
- The request path pays one non-blocking channel send (and nothing at all with the flag off, where the routes are byte for byte
  the ones without the feature).
- A loss is possible and counted; the share is of requests RECORDED. It starts the day the flag is first switched on.
- `cmd/api` gains one worker and, with the flag on, a few Postgres writes per second under load, in short separate
  transactions, none of them on the request path.
- The analytics topic carries a seventh type; `requests_answered` grows by one row per answered request while the flag is on
  (a retention decision for the analytical database, as for every other fact).

## Alternatives considered and rejected

- **A synchronous outbox write in the request transaction**: exact, but it is the write ADR 0019 keeps off the estimate path,
  and it ties response latency and success to Postgres.
- **An outbox write on the request goroutine after the response, unbounded or without a queue**: one goroutine and one
  transaction per request, so a slow database multiplies into unbounded goroutines.
- **An unbounded queue**: converts an outage into memory growth in the API pod.
- **Retry or a durable local buffer for dropped facts**: reintroduces the coupling and the operational surface the best-effort
  decision avoids, for a ratio that tolerates a bounded loss.
- **Publishing straight to Kafka from the handler**: bypasses the transactional outbox, the one publishing path of ADR 0005.
- **Deriving requests from offers**: ADR 0030's offers-based numbers; they cannot see a request that got no offer.
