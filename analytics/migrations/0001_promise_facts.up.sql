-- delivery-promise analytics read model (ADR 0018).
--
-- This is the ANALYTICAL database, separate from the OLTP database. It is
-- written only by cmd/dp-projector and read (read-only) by cmd/dp-reports.
-- Everything here is a projection of this context's own two topics
-- (retailnetwork.delivery-promise.events and .analytics), not a source of
-- truth, and can be dropped and rebuilt from them.

-- Idempotency: every applied CloudEvents id is recorded here exactly once, in
-- the SAME transaction as its effect on a fact table. occurred_at is the
-- event's CloudEvents `time` (the basis of /reports/freshness).
CREATE TABLE analytics_processed_events (
    event_id    TEXT        PRIMARY KEY,
    event_type  TEXT        NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    applied_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- One row per offer, from deliveryoffer.DeliveryOfferQuoted.
CREATE TABLE offers_quoted (
    offer_id  TEXT        PRIMARY KEY CHECK (offer_id <> ''),
    tier      TEXT        NOT NULL CHECK (tier <> ''),
    quoted_at TIMESTAMPTZ NOT NULL,
    event_id  TEXT        NOT NULL
);

-- One row per promise, from promise.PromiseMade: the terms AS MADE.
--   window_minutes  deliverByLatest - deliverByEarliest, in minutes
--   exact_day       the window starts and ends on one local calendar day
--   zone_known      time_zone was present and loadable (else read in UTC)
-- The three derived columns are computed by internal/analytics/report when
-- the fact is applied, so the reports only count and sum them.
CREATE TABLE promises_made (
    promise_id          TEXT             PRIMARY KEY CHECK (promise_id <> ''),
    offer_id            TEXT             NOT NULL,
    tier                TEXT             NOT NULL CHECK (tier <> ''),
    basis               TEXT             NOT NULL CHECK (basis <> ''),
    made_at             TIMESTAMPTZ      NOT NULL,
    deliver_by_earliest TIMESTAMPTZ      NOT NULL,
    deliver_by_latest   TIMESTAMPTZ      NOT NULL,
    time_zone           TEXT             NOT NULL DEFAULT '',
    window_minutes      DOUBLE PRECISION NOT NULL CHECK (window_minutes >= 0),
    exact_day           BOOLEAN          NOT NULL,
    zone_known          BOOLEAN          NOT NULL,
    event_id            TEXT             NOT NULL
);

-- One row per promise.PromiseRevised (revisionNo is strictly increasing per
-- promise).
CREATE TABLE promise_revisions (
    promise_id  TEXT        NOT NULL CHECK (promise_id <> ''),
    revision_no INTEGER     NOT NULL CHECK (revision_no >= 1),
    revised_at  TIMESTAMPTZ NOT NULL,
    event_id    TEXT        NOT NULL,
    PRIMARY KEY (promise_id, revision_no)
);

-- One row per promise.PromiseUnkeepable (raised at most once per promise).
CREATE TABLE promise_unkeepable (
    promise_id    TEXT        PRIMARY KEY CHECK (promise_id <> ''),
    unkeepable_at TIMESTAMPTZ NOT NULL,
    event_id      TEXT        NOT NULL
);

-- One row per promise: its verdict, from promise.PromiseKept or
-- promise.PromiseBroken (cause LATE, LOST or RETURNED). A promise is judged
-- exactly once.
CREATE TABLE promise_judgements (
    promise_id      TEXT        PRIMARY KEY CHECK (promise_id <> ''),
    verdict         TEXT        NOT NULL CHECK (verdict IN ('KEPT', 'BROKEN')),
    cause           TEXT        NOT NULL DEFAULT '' CHECK (cause IN ('', 'LATE', 'LOST', 'RETURNED')),
    late_by_minutes INTEGER     NOT NULL DEFAULT 0 CHECK (late_by_minutes >= 0),
    judged_at       TIMESTAMPTZ NOT NULL,
    event_id        TEXT        NOT NULL,
    CHECK ((verdict = 'KEPT') = (cause = ''))
);

-- The reports filter each fact on its own instant.
CREATE INDEX idx_offers_quoted_quoted_at ON offers_quoted (quoted_at);
CREATE INDEX idx_promises_made_made_at ON promises_made (made_at);
CREATE INDEX idx_promise_judgements_judged_at ON promise_judgements (judged_at);
CREATE INDEX idx_analytics_processed_events_occurred_at ON analytics_processed_events (occurred_at);
