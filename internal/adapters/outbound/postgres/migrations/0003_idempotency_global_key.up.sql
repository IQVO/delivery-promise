-- Fleet REST API guideline 2.3: an Idempotency-Key is GLOBAL, not
-- per route. The stored row is found by key alone and compared by fingerprint
-- (method + path + SHA-256 of the body), so the same key on another operation
-- is a 409 idempotency-key-reused instead of a fresh request.
--
-- `route` stays and now holds "METHOD /concrete/path" for new rows. Rows written
-- before this migration keep their (pattern) route; within the 24-hour
-- retention a reuse of such a key answers 409, which is the safe side.
-- Duplicate keys (the same key on two routes) keep only their newest row.
DELETE FROM idempotency_keys a
USING idempotency_keys b
WHERE a.key = b.key
  AND (a.created_at, a.route) < (b.created_at, b.route);

ALTER TABLE idempotency_keys DROP CONSTRAINT idempotency_keys_pkey;
ALTER TABLE idempotency_keys ADD PRIMARY KEY (key);
