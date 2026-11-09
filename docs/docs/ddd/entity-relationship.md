---
id: entity-relationship
title: Entity relationship
sidebar_label: Entity relationship
description: "The Postgres schema of Delivery Promise: aggregate tables, event-fed read models, outbox, inbox and idempotency keys."
---

# Entity relationship

The schema is created by two embedded migrations (`internal/adapters/outbound/postgres/migrations/0001_delivery_promise_schema.up.sql`, `0002_window_time_zone.up.sql`). Aggregates are stored as a `jsonb` snapshot that `Restore()` validates on read, with columns beside it for the queries and constraints the use cases need. There are almost no foreign keys: relations between aggregates are by id, and the read models are independent copies of other contexts' facts.

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
    text route PK
    text key PK
    text request_hash
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

Source: `internal/adapters/outbound/postgres/migrations/0001_delivery_promise_schema.up.sql`, `internal/adapters/outbound/postgres/` (repositories).

- `promises.offer_id` is `UNIQUE`: an offer backs at most one promise. `promises_order_group` is a partial unique index on `(order_id, group_no)` for promises that are not `RELEASED` or `CANCELLED`, so one live promise per order group.
- Partial indexes serve the sweeper and the reactive policies: `promises_hold_expiry` (PROVISIONAL by hold expiry), `promises_at_risk`, `promises_pickup` and `promises_wave` (CONFIRMED or AT_RISK by pickup departure and wave), a GIN index on `leg_ids`, `delivery_offers_quoted_expiry`, `promise_books_open_due`, `outbox_events_unpublished` (the relay's work list).
- `version` implements optimistic concurrency: the repository updates the row only when the version is the next one; a lost race is `ErrConcurrentUpdate`, retried by the use case up to three times.
- `outbox_events.id` (a `bigserial`) is the publication order; `event_id` is the stable CloudEvents id and is `UNIQUE`; `attempts` and `last_error` record relay failures; `published_at IS NULL` means not yet published.
- `idempotency_keys` rows are inserted by the request's own transaction and their outcome written just before it commits; rows older than 24 hours are purged on the next keyed write.
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
    text assignment_id
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

Source: `0001_delivery_promise_schema.up.sql` (lines 55 to 73), `0004_transit_actuals.up.sql`, `0005_listing_owner.up.sql`, `0007_node_tier_eligibility.up.sql`, `0008_departure_arrival.up.sql`, `internal/application/readmodel`.

| Table | Fed by | Used by |
| --- | --- | --- |
| `rm_nodes`, `rm_regions`, `rm_areas`, `rm_lanes` | node-topology events | the search (candidate nodes, service area to delivery station, lane transit for `NOMINAL`), `ListPromiseBooks` (node time zone) |
| `rm_items`, `rm_listings` | offer-availability events | shipment grouping and availability per (node, item, owner): `rm_listings` is keyed by `(item_id, node_id, owner_ref)` (index `rm_listings_node_item_owner`, migration 0005) and a line is covered by ONE owner, never a sum (ADR 0017) |
| `rm_departures`, `rm_waves` | linehaul and last-mile events | path search, `MakePromise` revalidation, cancellation risk |
| `rm_scorecards` | node-performance events | the `ScorecardHandicap` |
| `rm_node_tier_eligibility` (`node_id`, `tier`, `eligible`, `changed_at`; PK `(node_id, tier)`; migration 0007) | node-performance `NodeTierEligibilityChanged` | removing ineligible nodes from `SAME_DAY` and `NEXT_DAY` candidates (ADR 0022); a missing row means eligible |
| `rm_assignments`, `rm_shipments`, `rm_trackings`, `rm_outcomes` | order-sourcing and shipping-and-tracking events | finding a promise's assignment and judging it |
| `rm_load_transit_open`, `rm_transit_actuals` | linehaul `LoadDeparted` and `LoadArrived` | the measured transit behind a `MEASURED` basis (ADR 0014) |
| `rm_departure_arrival` | linehaul `LoadArrived` that carries a `departureId` | the late-risk sweep: a witness departure past its expected arrival with no row is flagged (ADR 0021); pruned past the transit window |
