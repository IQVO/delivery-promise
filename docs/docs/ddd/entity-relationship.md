---
id: entity-relationship
title: Entity relationship
sidebar_label: Entity relationship
description: "The Postgres schemas of Delivery Promise: aggregate tables, event-fed read models, outbox, inbox and idempotency keys in the OLTP database, and the analytical fact tables."
---

# Entity relationship

The service has two Postgres databases, each with its own embedded migrations. The OLTP schema is created by five migrations in `internal/adapters/outbound/postgres/migrations/` (`0001_delivery_promise_schema`, `0002_window_time_zone`, `0003_idempotency_global_key`, `0004_transit_actuals`, `0005_listing_owner`). The analytical schema is created by `analytics/migrations/0001_promise_facts.up.sql` and is covered in [Analytical database](#analytical-database). The rest of this page up to that section is the OLTP database. Aggregates are stored as a `jsonb` snapshot that `Restore()` validates on read, with columns beside it for the queries and constraints the use cases need. There are almost no foreign keys: relations between aggregates are by id, and the read models are independent copies of other contexts' facts.

## Aggregates, outbox and inbox

```mermaid
erDiagram
  delivery_offers {
    text id PK
    text state "QUOTED, EXPIRED, CONSUMED"
    timestamptz expires_at
    bigint version
    jsonb snapshot
  }
  promises {
    text id PK
    text offer_id UK
    text order_id
    int group_no
    text state "7 states"
    timestamptz hold_expires_at
    timestamptz made_at
    text node_id
    text pickup_departure_id
    text_array leg_ids
    text wave_id
    bigint version
    jsonb snapshot
  }
  promise_books {
    text id PK "nodeId:pickupDepartureId"
    text node_id
    text pickup_departure_id
    timestamptz cutoff_at
    text state "OPEN, CLOSED"
    bigint version
    jsonb snapshot
  }
  promise_policies {
    int version PK
    jsonb snapshot
  }
  outbox_events {
    bigserial id PK
    text event_id UK
    text topic
    text event_type
    text subject
    bytea key
    text dataschema
    bytea value
    jsonb headers
    timestamptz created_at
    timestamptz published_at
    int attempts
    text last_error
  }
  processed_events {
    text event_id PK
    text event_type
    timestamptz processed_at
  }
  idempotency_keys {
    text route "METHOD and concrete path for new rows"
    text key PK "global since 0003"
    text request_hash "method + path + sha256 of the body"
    int status_code
    bytea response_body
    jsonb response_headers
    timestamptz created_at
    timestamptz completed_at
  }
  delivery_offers ||--o| promises : "backs (offer_id)"
  promise_books ||--o{ promises : "holds capacity (book id in snapshot)"
  promise_policies ||--o{ delivery_offers : "policyVersion in snapshot"
```

Source: `internal/adapters/outbound/postgres/migrations/0001_delivery_promise_schema.up.sql`, `0003_idempotency_global_key.up.sql`, `internal/adapters/outbound/postgres/` (repositories).

- `promises.offer_id` is `UNIQUE`: an offer backs at most one promise. `promises_order_group` is a partial unique index on `(order_id, group_no)` for promises that are not `RELEASED` or `CANCELLED`, so one live promise per order group.
- Partial indexes serve the sweeper and the reactive policies: `promises_hold_expiry` (PROVISIONAL by hold expiry), `promises_at_risk`, `promises_pickup` and `promises_wave` (CONFIRMED or AT_RISK by pickup departure and wave), a GIN index on `leg_ids`, `delivery_offers_quoted_expiry`, `promise_books_open_due`, `outbox_events_unpublished` (the relay's work list).
- `version` implements optimistic concurrency: the repository updates the row only when the version is the next one; a lost race is `ErrConcurrentUpdate`, retried by the use case up to three times.
- `outbox_events.id` (a `bigserial`) is the publication order; `event_id` is the stable CloudEvents id and is `UNIQUE`; `attempts` and `last_error` record relay failures; `published_at IS NULL` means not yet published.
- `idempotency_keys` rows are inserted by the request's own transaction and their outcome written just before it commits; rows older than 24 hours are purged on the next keyed write (`claimKey` in `internal/adapters/inbound/http/idempotency.go`). Migration `0003_idempotency_global_key` made the key global: it deleted all but the newest row of any key present on two routes and replaced the primary key `(route, key)` with `key` alone, so the same key on another operation is a 409 `idempotency-key-reused`. `route` stays as a column and holds `METHOD /concrete/path` for new rows; rows written before `0003` keep their route pattern.
- `0002_window_time_zone` changes no column: the window's `TimeZone` lives in the jsonb snapshot and the migration backfills old offers and promises from `rm_areas` and `rm_nodes`.
- Each line of an offer and of a promise snapshot carries `OwnerRef`, the owner whose stock covers it (`""` = operator-owned, ADR 0017; a line persisted before it existed reads as operator-owned). The hold queries group by `(node_id, item, OwnerRef)`.

## Local read models

One jsonb row per entity, written by the consumer's projections (`internal/application/usecases/projections.go`). They are not related to the aggregates by foreign keys: the search joins them in memory.

```mermaid
erDiagram
  rm_nodes {
    text id PK
    jsonb data
  }
  rm_regions {
    text id PK
    jsonb data
  }
  rm_areas {
    text postal_prefix PK
    jsonb data "StationID, region"
  }
  rm_lanes {
    text id PK
    jsonb data
  }
  rm_items {
    text id PK
    jsonb data
  }
  rm_listings {
    text id PK
    text item_id
    text node_id
    text owner_ref "'' = operator-owned"
    jsonb data
  }
  rm_departures {
    text id PK
    timestamptz cutoff_at
    bool scheduled
    jsonb data
  }
  rm_waves {
    text id PK
    text station_id
    timestamptz window_end
    bool scheduled
    jsonb data
  }
  rm_scorecards {
    text node_id PK
    jsonb data
  }
  rm_assignments {
    text id PK
    text promise_id
    jsonb data
  }
  rm_shipments {
    text assignment_id PK
    text_array tracking_numbers
  }
  rm_trackings {
    text tracking_number PK
    text assignment_id PK
  }
  rm_outcomes {
    text tracking_number PK
    jsonb data
  }
  rm_load_transit_open {
    text load_id PK
    text origin_node_id
    timestamptz departed_at
  }
  rm_transit_actuals {
    text origin_node_id PK
    text dest_node_id PK
    jsonb observations
  }
  rm_promise_calibration {
    text tier PK
    text basis PK
    text node_grade PK
    jsonb observations
  }
  rm_departure_arrival {
    text departure_id PK
    timestamptz arrived_at
  }
  rm_nodes ||--o{ rm_listings : "node_id"
  rm_items ||--o{ rm_listings : "item_id"
  rm_nodes ||--o{ rm_scorecards : "node_id"
  rm_nodes ||--o{ rm_waves : "station_id"
  rm_areas }o--|| rm_nodes : "StationID in data"
  rm_assignments ||--o| rm_shipments : "assignment_id"
  rm_shipments ||--o{ rm_trackings : "tracking numbers"
  rm_trackings ||--o| rm_outcomes : "tracking_number"
```

Source: `0001_delivery_promise_schema.up.sql` (lines 55 to 73), `0004_transit_actuals.up.sql`, `0005_listing_owner.up.sql`, `0006_promise_calibration.up.sql`, `0007_node_tier_eligibility.up.sql`, `0008_departure_arrival.up.sql`, `0009_tracking_many_assignments.up.sql`, `internal/application/readmodel`.

| Table | Fed by | Used by |
| --- | --- | --- |
| `rm_nodes`, `rm_regions`, `rm_areas`, `rm_lanes` | node-topology events | the search (candidate nodes, service area to delivery station, lane transit for `NOMINAL`), `ListPromiseBooks` (node time zone) |
| `rm_items`, `rm_listings` | offer-availability events | shipment grouping and availability per (node, item, owner): `rm_listings` is keyed by `(item_id, node_id, owner_ref)` (index `rm_listings_node_item_owner`, migration 0005) and a line is covered by ONE owner, never a sum (ADR 0017) |
| `rm_departures`, `rm_waves` | linehaul and last-mile events | path search, `MakePromise` revalidation, cancellation risk |
| `rm_scorecards` | node-performance events | the `ScorecardHandicap` |
| `rm_node_tier_eligibility` (`node_id`, `tier`, `eligible`, `changed_at`; PK `(node_id, tier)`; migration 0007) | node-performance `NodeTierEligibilityChanged` | removing ineligible nodes from `SAME_DAY` and `NEXT_DAY` candidates (ADR 0022); a missing row means eligible |
| `rm_assignments`, `rm_shipments`, `rm_trackings`, `rm_outcomes` | order-sourcing and shipping-and-tracking events | finding a promise's assignment and judging it. `rm_trackings` is keyed by `(tracking_number, assignment_id)` (migration 0009): a merged package is shared by several assignments and one delivery, loss or return judges every sharing promise (ADR 0025) |
| `rm_load_transit_open`, `rm_transit_actuals` | linehaul `LoadDeparted` and `LoadArrived` | the measured transit behind a `MEASURED` basis (ADR 0014) |
| `rm_promise_calibration` | this service's own `PromiseKept` and late `PromiseBroken` judgements | the calibration buffer and the `confidence` of a quote (ADR 0020) |
| `rm_departure_arrival` | linehaul `LoadArrived` that carries a `departureId` | the late-risk sweep: a witness departure past its expected arrival with no row is flagged (ADR 0021); pruned past the transit window |

## Analytical database

A second, separate database (ADR 0018) written only by `cmd/dp-projector` and read through a read-only pool by `cmd/dp-reports`. It is a projection of this context's own `retailnetwork.delivery-promise.events` and `.analytics` topics, not a source of truth, and can be dropped and rebuilt from them. It has no foreign keys and no link to the OLTP tables.

```mermaid
erDiagram
  analytics_processed_events {
    text event_id PK
    text event_type
    timestamptz occurred_at "CloudEvents time, basis of /reports/freshness"
    timestamptz applied_at
  }
  offers_quoted {
    text offer_id PK
    text tier
    timestamptz quoted_at
    text event_id
  }
  promises_made {
    text promise_id PK
    text offer_id
    text tier
    text basis
    timestamptz made_at
    timestamptz deliver_by_earliest
    timestamptz deliver_by_latest
    text time_zone "empty when absent"
    double_precision window_minutes "latest minus earliest, CHECK >= 0"
    boolean exact_day "window within one local calendar day"
    boolean zone_known "time_zone present and loadable"
    text event_id
  }
  promise_revisions {
    text promise_id PK
    int revision_no PK "CHECK >= 1"
    timestamptz revised_at
    text event_id
  }
  promise_unkeepable {
    text promise_id PK "raised at most once per promise"
    timestamptz unkeepable_at
    text event_id
  }
  promise_judgements {
    text promise_id PK "judged exactly once"
    text verdict "KEPT or BROKEN"
    text cause "empty for KEPT; LATE, LOST or RETURNED for BROKEN"
    int late_by_minutes "CHECK >= 0"
    timestamptz judged_at
    text event_id
  }
```

Source: `analytics/migrations/0001_promise_facts.up.sql`, `internal/adapters/outbound/analyticsstore/projection.go`.

- `analytics_processed_events` is the inbox of this database: every applied CloudEvents id is recorded once, in the same transaction as its effect on a fact table (`Apply` claims the id with `ON CONFLICT DO NOTHING` and skips the fact when the id was already there). A failure leaves neither the id nor the fact behind.
- Each fact table carries the `event_id` of the event that wrote the row. The tables of promises made, offers quoted and promise judgements are upserts keyed by their id (`ON CONFLICT ... DO UPDATE`); `promise_revisions` (keyed by `promise_id, revision_no`, from `PromiseRevised`) and `promise_unkeepable` (from `PromiseUnkeepable`) are insert-or-ignore.
- `promise_judgements` takes the verdict from `PromiseKept` or `PromiseBroken`. A CHECK ties the two columns: `cause` is empty exactly when the verdict is `KEPT`.
- The three derived columns of `promises_made` (`window_minutes`, `exact_day`, `zone_known`) are computed when the fact is applied, so the reports only count and sum them.
- Indexes: `idx_offers_quoted_quoted_at`, `idx_promises_made_made_at`, `idx_promise_judgements_judged_at` and `idx_analytics_processed_events_occurred_at`, because the reports filter each fact on its own instant.
