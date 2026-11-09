-- Reverts 0005: the owner key goes, the (node_id, item_id) index comes back.
-- The OwnerRef kept in the rows' data is harmless to the code of 0004.
CREATE INDEX rm_listings_node_item ON rm_listings (node_id, item_id);
DROP INDEX rm_listings_node_item_owner;
ALTER TABLE rm_listings DROP COLUMN owner_ref;
