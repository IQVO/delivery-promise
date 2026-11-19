-- Request-level promise coverage (ADR 0032). Additive: one new table, nothing
-- dropped or rewritten, 0001 and 0002 untouched.
--
-- One row per analytics.DeliveryRequestAnswered: a quote or an estimate the
-- service answered, with or without a promise. The fact is published BEST EFFORT
-- (a bounded queue that drops when full, no retry) and only once the producer
-- is switched on, so the table starts at the deploy date and tolerates a bounded
-- loss; the report documents it.
--
--   region                  the destination region, the first two characters of
--                           the postal prefix, computed by the producer: the
--                           database never holds more of the destination.
--   shortest_promised_days  local calendar days from the answer day to the day
--                           of the earliest latest-delivery across the answered
--                           options (>= 0), NULL when no promise was answered.
--   event_id                the CloudEvents id (the projection's dedupe key).
CREATE TABLE requests_answered (
    request_id             TEXT        PRIMARY KEY CHECK (request_id <> ''),
    request_kind           TEXT        NOT NULL CHECK (request_kind IN ('QUOTE', 'ESTIMATE')),
    region                 TEXT        NOT NULL CHECK (char_length(region) BETWEEN 1 AND 2),
    item_count             INTEGER     NOT NULL CHECK (item_count >= 1),
    answered_with_promise  BOOLEAN     NOT NULL,
    shortest_promised_days INTEGER     CHECK (shortest_promised_days >= 0),
    answered_at            TIMESTAMPTZ NOT NULL,
    event_id               TEXT        NOT NULL
);

CREATE INDEX idx_requests_answered_answered_at ON requests_answered (answered_at);
