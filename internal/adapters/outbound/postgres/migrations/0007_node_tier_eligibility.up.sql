-- Fast-tier eligibility read model (ADR 0022), fed by node-performance's
-- NodeTierEligibilityChanged. One row per (node, tier) that was ever mentioned;
-- a missing row means ELIGIBLE. changed_at is the event's changedAt: a row is
-- replaced only by an event that is not older.
CREATE TABLE rm_node_tier_eligibility (
    node_id    text        NOT NULL,
    tier       text        NOT NULL,
    eligible   boolean     NOT NULL,
    changed_at timestamptz NOT NULL,
    PRIMARY KEY (node_id, tier)
);

-- The search reads only the ineligible rows, once per request.
CREATE INDEX rm_node_tier_eligibility_ineligible ON rm_node_tier_eligibility (node_id, tier) WHERE NOT eligible;
