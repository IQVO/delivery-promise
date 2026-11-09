-- Owner-aware AvailabilityView (ADR 0017): a listing row is keyed by
-- (item_id, node_id, owner_ref), owner_ref '' for an operator-owned listing,
-- never NULL. The backfill sets every row written before this migration to ''
-- (operator-owned), in the column and in the row's data, which is what those
-- rows were netted as. A row that already names its owner in its data (one
-- written under 0005, rolled back and re-applied) keeps it.
ALTER TABLE rm_listings ADD COLUMN owner_ref text NOT NULL DEFAULT '';

UPDATE rm_listings SET owner_ref = COALESCE(data->>'OwnerRef', '');
UPDATE rm_listings SET data = jsonb_set(data, '{OwnerRef}', '""'::jsonb) WHERE NOT data ? 'OwnerRef';

-- The netting lookups read one node's items by owner; the key index replaces
-- the (node_id, item_id) one, whose prefix it covers.
CREATE INDEX rm_listings_node_item_owner ON rm_listings (node_id, item_id, owner_ref);
DROP INDEX rm_listings_node_item;
