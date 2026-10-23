-- Aggregates: the full Snapshot is stored as jsonb (Restore() is the single
-- boundary that validates it); the columns beside it exist for the queries and
-- constraints the use cases need.

CREATE TABLE delivery_offers (
    id          text        PRIMARY KEY,
    state       text        NOT NULL CHECK (state IN ('QUOTED', 'EXPIRED', 'CONSUMED')),
    expires_at  timestamptz NOT NULL,
    version     bigint      NOT NULL CHECK (version >= 1),
    snapshot    jsonb       NOT NULL
);
CREATE INDEX delivery_offers_quoted_expiry ON delivery_offers (expires_at, id) WHERE state = 'QUOTED';

CREATE TABLE promises (
    id                  text        PRIMARY KEY,
    offer_id            text        NOT NULL UNIQUE,
    order_id            text        NOT NULL DEFAULT '',
    group_no            integer     NOT NULL DEFAULT 0,
    state               text        NOT NULL CHECK (state IN ('PROVISIONAL', 'CONFIRMED', 'AT_RISK', 'KEPT', 'BROKEN', 'RELEASED', 'CANCELLED')),
    hold_expires_at     timestamptz NOT NULL,
    made_at             timestamptz NOT NULL,
    node_id             text        NOT NULL,
    pickup_departure_id text        NOT NULL DEFAULT '',
    leg_ids             text[]      NOT NULL DEFAULT '{}',
    wave_id             text        NOT NULL DEFAULT '',
    version             bigint      NOT NULL CHECK (version >= 1),
    snapshot            jsonb       NOT NULL
);
CREATE UNIQUE INDEX promises_order_group ON promises (order_id, group_no) WHERE order_id <> '' AND state NOT IN ('RELEASED', 'CANCELLED');
CREATE INDEX promises_order ON promises (order_id) WHERE order_id <> '';
CREATE INDEX promises_hold_expiry ON promises (hold_expires_at, id) WHERE state = 'PROVISIONAL';
CREATE INDEX promises_at_risk ON promises (made_at, id) WHERE state = 'AT_RISK';
CREATE INDEX promises_pickup ON promises (pickup_departure_id) WHERE state IN ('CONFIRMED', 'AT_RISK');
CREATE INDEX promises_legs ON promises USING gin (leg_ids);
CREATE INDEX promises_wave ON promises (wave_id) WHERE state IN ('CONFIRMED', 'AT_RISK');
CREATE INDEX promises_held ON promises (node_id) WHERE state = 'PROVISIONAL';

CREATE TABLE promise_books (
    id                  text        PRIMARY KEY,
    node_id             text        NOT NULL,
    pickup_departure_id text        NOT NULL,
    cutoff_at           timestamptz NOT NULL,
    state               text        NOT NULL CHECK (state IN ('OPEN', 'CLOSED')),
    version             bigint      NOT NULL CHECK (version >= 1),
    snapshot            jsonb       NOT NULL
);
CREATE INDEX promise_books_node_cutoff ON promise_books (node_id, cutoff_at);
CREATE INDEX promise_books_open_due ON promise_books (cutoff_at, id) WHERE state = 'OPEN';

CREATE TABLE promise_policies (
    version   integer     PRIMARY KEY CHECK (version >= 1),
    snapshot  jsonb       NOT NULL
);

-- Local read models fed by consumed events (one jsonb row per entity).
CREATE TABLE rm_nodes       (id text PRIMARY KEY, data jsonb NOT NULL);
CREATE TABLE rm_regions     (id text PRIMARY KEY, data jsonb NOT NULL);
CREATE TABLE rm_areas       (postal_prefix text PRIMARY KEY, data jsonb NOT NULL);
CREATE TABLE rm_lanes       (id text PRIMARY KEY, data jsonb NOT NULL);
CREATE TABLE rm_items       (id text PRIMARY KEY, data jsonb NOT NULL);
CREATE TABLE rm_listings    (id text PRIMARY KEY, item_id text NOT NULL, node_id text NOT NULL, data jsonb NOT NULL);
CREATE INDEX rm_listings_node_item ON rm_listings (node_id, item_id);
CREATE INDEX rm_listings_item ON rm_listings (item_id);
CREATE TABLE rm_departures  (id text PRIMARY KEY, cutoff_at timestamptz NOT NULL, scheduled boolean NOT NULL, data jsonb NOT NULL);
CREATE INDEX rm_departures_cutoff ON rm_departures (cutoff_at) WHERE scheduled;
CREATE TABLE rm_waves       (id text PRIMARY KEY, station_id text NOT NULL, window_end timestamptz NOT NULL, scheduled boolean NOT NULL, data jsonb NOT NULL);
CREATE INDEX rm_waves_station ON rm_waves (station_id, window_end) WHERE scheduled;
CREATE TABLE rm_scorecards  (node_id text PRIMARY KEY, data jsonb NOT NULL);
CREATE TABLE rm_assignments (id text PRIMARY KEY, promise_id text NOT NULL, data jsonb NOT NULL);
CREATE INDEX rm_assignments_promise ON rm_assignments (promise_id);
CREATE TABLE rm_shipments   (assignment_id text PRIMARY KEY, tracking_numbers text[] NOT NULL);
CREATE TABLE rm_trackings   (tracking_number text PRIMARY KEY, assignment_id text NOT NULL);
CREATE TABLE rm_outcomes    (tracking_number text PRIMARY KEY, data jsonb NOT NULL);

-- Inbox: one row per consumed CloudEvents id (idempotent consumers).
CREATE TABLE processed_events (
    event_id     text        PRIMARY KEY,
    event_type   text        NOT NULL,
    processed_at timestamptz NOT NULL
);

-- Transactional outbox, written in the same transaction as the aggregates.
CREATE TABLE outbox_events (
    id           bigserial   PRIMARY KEY,
    event_id     text        NOT NULL UNIQUE,
    topic        text        NOT NULL,
    event_type   text        NOT NULL,
    subject      text        NOT NULL,
    key          bytea       NOT NULL,
    dataschema   text        NOT NULL,
    value        bytea       NOT NULL,
    headers      jsonb       NOT NULL DEFAULT '[]',
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    attempts     integer     NOT NULL DEFAULT 0,
    last_error   text
);
CREATE INDEX outbox_events_unpublished ON outbox_events (id) WHERE published_at IS NULL;

-- REST idempotency (transactional): the row is inserted by the request's own
-- transaction and its outcome is written just before that transaction commits,
-- so a committed row always carries its outcome.
CREATE TABLE idempotency_keys (
    key              text        PRIMARY KEY,
    method           text        NOT NULL,
    path             text        NOT NULL,
    request_hash     text        NOT NULL,
    status_code      integer,
    response_body    bytea,
    response_headers jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    completed_at     timestamptz
);
