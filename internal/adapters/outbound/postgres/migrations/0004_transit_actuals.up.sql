-- Measured-transit read model (ADR 0014): one open row per departed load and,
-- per (origin, destination) pair, the ring of the latest observed transits.
CREATE TABLE rm_load_transit_open (
    load_id        text        PRIMARY KEY,
    origin_node_id text        NOT NULL,
    departed_at    timestamptz NOT NULL
);

CREATE TABLE rm_transit_actuals (
    origin_node_id text  NOT NULL,
    dest_node_id   text  NOT NULL,
    observations   jsonb NOT NULL,
    PRIMARY KEY (origin_node_id, dest_node_id)
);
