-- Restores the per-route primary key. Every global key is also unique per
-- route, so no row has to go.
ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE idempotency_keys ADD PRIMARY KEY (route, key);
