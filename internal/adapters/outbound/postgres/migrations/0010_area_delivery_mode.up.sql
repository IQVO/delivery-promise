-- Service-area delivery mode (ADR 0026): the planner's CARRIER_HANDOFF
-- declaration and its 1..4 carrier options. rm_areas keeps its jsonb row as
-- the source the adapter reads back; the two columns mirror the mode and the
-- options so they stay queryable in SQL. The carrier pairing rows of the
-- measured-transit ring (ADR 0014 machinery) are keyed by tracking number and
-- remember the (handoffNodeID, "area:"+prefix) pair they will close under.
ALTER TABLE rm_areas
    ADD COLUMN delivery_mode   text  NOT NULL DEFAULT 'NETWORK_ROUTE' CHECK (delivery_mode IN ('NETWORK_ROUTE', 'CARRIER_HANDOFF')),
    ADD COLUMN carrier_options jsonb NOT NULL DEFAULT '[]';

CREATE TABLE rm_carrier_transit_open (
    tracking_number text        PRIMARY KEY,
    handoff_node_id text        NOT NULL,
    dest_node_id    text        NOT NULL,
    handed_at       timestamptz NOT NULL
);
