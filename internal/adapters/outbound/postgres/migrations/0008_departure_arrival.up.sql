-- DepartureArrival read model (ADR 0021): one row per scheduled departure whose
-- load was seen arriving (a LoadArrived that carries a departureId). The
-- late-risk sweep flags a promise whose witness departure is past its expected
-- arrival and has no row here. Rows older than the transit window are pruned
-- on write.
CREATE TABLE rm_departure_arrival (
    departure_id text        PRIMARY KEY,
    arrived_at   timestamptz NOT NULL
);

CREATE INDEX rm_departure_arrival_arrived_at ON rm_departure_arrival (arrived_at);
