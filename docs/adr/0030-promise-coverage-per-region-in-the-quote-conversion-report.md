# ADR 0030: Promise coverage per region in the quote-conversion report

## Status

Accepted (2026-11-18). Additive: no published payload, topic, CloudEvents type, outbox row, OLTP table or REST route of
`cmd/api` changes; no new report route. Adds one analytical-database migration (`analytics/migrations/0002_offer_coverage`),
one optional property on the `QuoteConversionReport` response of `cmd/dp-reports`, and reads three fields of
`DeliveryOfferQuoted` the projector did not read before. KPI baseline v2, feature F5.

## Context

The plan asks one question per region: of the delivery requests the service answered, how many were answered with a
promise of N days or less. ADR 0018 reports quotes, promises and their keeping per tier; none of it is per region and
none says how far away the promised day is.

### What exists on the analytics topic

| source | carries a region | carries the promised day | published |
| --- | --- | --- | --- |
| `POST /delivery-estimates` (ADR 0019) | n/a | n/a | NOTHING: the operation is safe, it stores no row and writes no outbox event |
| `POST /delivery-quotes` outcome with no feasible offer (OfferConstraint only) | n/a | n/a | NOTHING |
| `DeliveryOfferQuoted` (analytics topic), one per persisted offer | `destinationPostalPrefix` | `deliverByEarliest`, `deliverByLatest`, `timeZone` (optional) | yes (ADR 0004, ADR 0018) |
| `PromiseMade` | none (no destination on the event) | its window | yes |

So an own fact with a region AND a promised window exists: `DeliveryOfferQuoted`. The projector read only `offerId` and
`tier` from it; the destination prefix and the window were on the payload all along (ADR 0018 said so: "every field the
KPIs need is already on one of them").

What does NOT exist is a request-level fact: nothing is published for an estimate, and a quote that finds no offer
leaves no event, so a denominator of "requests asked" cannot be projected from the stream today. Adding one would be a
new published type: an analytics-only event raised on the read path of `POST /delivery-estimates` (up to 50 items per
call, listing-page traffic), which ADR 0019 deliberately keeps free of any write. This ADR does not take that step.

## Decision

### 1. The unit counted is the offer quoted

The coverage is measured over the **offers quoted** (`DeliveryOfferQuoted`), the only own fact that carries both a
region and a promised window. In the wire names of the plan: `requests` = offers quoted in the region that day;
`answeredWithPromise` = those that carry a usable window (`deliverByLatest` present); `answeredWithinDays` = those
whose promised day count is within N.

Consequences, stated plainly so nobody reads more into the number than is there:

- One quote request for several tiers or shipment groups is counted once per offer, not once per request.
- A request answered with constraints only (no feasible offer) and every estimate request leave no event and are NOT in
  the denominator. Until a request-level fact exists, `requests` equals `answeredWithPromise` for any event that
  follows the contract (`deliverByLatest` is required on the payload); the two are separate fields so that adding the
  request-level fact later changes the numbers, not the shape.
- The fact that would be needed for a true request share: an analytics-only `DeliveryRequestAnswered`, raised once per
  quote or estimate request, with `destinationPostalPrefix` and the shortest promised `deliverByLatest` (omitted when
  the request got no promise). It is a fact this context decides, so it would be allowed (own prefix, optional
  fields, CloudEvents 1.0, listed in the ADR 0004 catalogue and `apis/asyncapi.yaml`). It is NOT added here because it
  would put an outbox write on the estimate read path against ADR 0019; if the plan needs the request-level share, it
  is a separate decision.

### 2. Region

`region` = the first two characters of `destinationPostalPrefix` (the whole prefix when it is one character), as sent,
never lowercased or padded. Only these two characters are stored and reported: not the full prefix, no address, no
customer. The projector computes the region when it applies the event (`report.RegionOf`), so the analytical database
never holds the third character. A destination without a prefix (an old or malformed payload) has region `''` and is
left out of the coverage; it is still counted by every other report.

Per UTC day the report keeps the **ten regions with most requests** (ties by region, byte order) and folds every other
region into one row whose region is `other` (a real region has at most two characters, so `other` cannot collide). The
`other` row is omitted when nothing is folded.

### 3. Promised days

`promisedDays` = the number of local calendar days from the day of the quote to the day of `deliverByLatest`, both read
in the window's `timeZone` (ADR 0011; UTC when the zone is absent or cannot be loaded, the same fallback as
`WindowOf`), never below 0. It is "days until the latest the shopper can be told", so a NEXT_DAY offer is 1, a same-day
offer is 0, a STANDARD offer is typically 3 to 5. The quote instant is the CloudEvents `time` of the event (ADR 0018,
section 2). It is derived by the pure package when the projector applies the event (`report.PromisedDays`) and stored
with the fact, so SQL only counts and groups. An offer without `deliverByLatest` has no promised days (`NULL`): it is a
request, not an answer with a promise.

### 4. The report

`GET /reports/quote-conversion?from=&to=` keeps its `from`, `to` and `days` (one row per UTC day and tier) exactly as
they are and gains one property, `promiseCoverage`: an array, one entry per UTC day that has at least one offer with a
region, ascending. The existing day objects are tier rows, so the per-day block is a sibling of `days`, not a field of
each tier row (it would repeat once per tier).

```json
"promiseCoverage": [
  {
    "day": "2026-11-10",
    "regions": [
      {
        "region": "78",
        "requests": 5,
        "answeredWithPromise": 5,
        "answeredWithinDays": [
          {"days": 1, "count": 2, "share": 0.4},
          {"days": 2, "count": 3, "share": 0.6},
          {"days": 3, "count": 4, "share": 0.8},
          {"days": 5, "count": 5, "share": 1}
        ]
      }
    ]
  }
]
```

- `answeredWithinDays` is cumulative over the documented constant list `[1, 2, 3, 5]` (`report.CoverageDays`): `count`
  is the offers with `promisedDays <= days`; `share` = `count` / `requests`, `null` when `requests` is 0.
- Wire fields are camelCase like every other report (ADR 0012); the plan's snake_case names (`promise_coverage`,
  `answered_with_promise`, `answered_within_n_days`) map to `promiseCoverage`, `answeredWithPromise`,
  `answeredWithinDays`.
- Days without data are omitted like in every report, so an empty range gives `"promiseCoverage": []`, never `null`.
- Bucketing, the top-10 bound, the fold into `other`, the cumulative counts and the shares live in
  `internal/analytics/report` (pure, in `make mutation`); SQL only counts per day, region and promised days.

### 5. Storage and rollout

`analytics/migrations/0002_offer_coverage` adds two columns to `offers_quoted`: `region TEXT NOT NULL DEFAULT ''`
(at most two characters) and `promised_days INTEGER NULL` (>= 0). Nothing is dropped or rewritten; `0001` is untouched
and the down step only drops the two columns. A rolling deploy is safe in any order:

- The reports binary and the report spec only gain a property; consumers decode tolerantly, and `from`, `to`, `days`
  are unchanged. The consumers that pin `apis/openapi.yaml` (customer-ordering, shopping-cart, checkout,
  storefront-bff) pin the quote and estimate routes, not the reports: they refresh their pin after merge so the
  pinned-contract gate stays green.
- Offers projected before the migration have region `''` and no promised days, so they are not in the coverage. The
  model is a projection of the topics: a fresh consumer group (or a rebuilt analytical database) re-reads the retained
  history and fills the new columns; there is no backfill job.
- No topic, consumer group, DLQ, event type or env key changes.

## Consequences

- Promise coverage per region is available per UTC day with no new event, no call to a sibling and no PII beyond a
  two-character postal prefix.
- The share is a share of OFFERS QUOTED, not of requests asked (section 1). It cannot see requests answered with no
  promise at all.
- Coverage starts at the first offer projected after the migration unless the projection is rebuilt.
- The report's response grows by up to 11 region rows per day.

## Alternatives considered and rejected

- **A new analytics event per estimate or quote request**: gives the true denominator, but puts an outbox write on the
  safe estimate read path (ADR 0019) and adds a published type to catalogue, AsyncAPI and consumers. Recorded in
  section 1 as the fact that would be needed.
- **A new route `/reports/promise-coverage`**: additive too, but the plan asks for the block on the existing report,
  and a new route needs its own spec, docs page and chart wiring for one block.
- **A per-tier-row field**: the block is per region per day, so it would repeat on every tier row of the day.
- **Store the full destination prefix and cut it at read time**: keeps one more character than the report needs.
- **Read the promised days from `PromiseMade`**: it carries no destination, and a promise exists only for the offers
  the shopper accepted, so it would measure conversion, not coverage.
