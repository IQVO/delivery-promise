-- ADR 0011: the delivery window names its local time zone.
--
-- Offers and promises are stored as a jsonb Snapshot, so the new
-- Window.TimeZone needs no column: binaries from ADR 0011 on write it on every
-- new offer and promise. This migration backfills the rows written before, from
-- the same source the search uses: the destination postal prefix's service
-- area (rm_areas) names the delivery station, whose node row (rm_nodes) carries
-- its IANA zone. A row is only touched when its window has no zone yet and the
-- station's zone is a name PostgreSQL knows and the domain accepts
-- (shared.ValidTimeZone); any other row keeps an absent zone, which the
-- contract reads as UTC. Re-running it changes nothing.

UPDATE delivery_offers AS o
SET snapshot = jsonb_set(o.snapshot, '{Window,TimeZone}', to_jsonb(z.zone))
FROM (
    SELECT a.postal_prefix, n.data ->> 'Timezone' AS zone
    FROM rm_areas AS a
    JOIN rm_nodes AS n ON n.id = a.data ->> 'StationID'
) AS z
WHERE z.postal_prefix = o.snapshot ->> 'DestinationPrefix'
  AND jsonb_typeof(o.snapshot -> 'Window') = 'object'
  AND COALESCE(o.snapshot #>> '{Window,TimeZone}', '') = ''
  AND length(z.zone) BETWEEN 1 AND 64
  AND z.zone ~ '^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)*$'
  AND EXISTS (SELECT 1 FROM pg_timezone_names AS t WHERE t.name = z.zone);

UPDATE promises AS p
SET snapshot = jsonb_set(p.snapshot, '{Terms,Window,TimeZone}', to_jsonb(z.zone))
FROM (
    SELECT a.postal_prefix, n.data ->> 'Timezone' AS zone
    FROM rm_areas AS a
    JOIN rm_nodes AS n ON n.id = a.data ->> 'StationID'
) AS z
WHERE z.postal_prefix = p.snapshot ->> 'DestinationPrefix'
  AND jsonb_typeof(p.snapshot #> '{Terms,Window}') = 'object'
  AND COALESCE(p.snapshot #>> '{Terms,Window,TimeZone}', '') = ''
  AND length(z.zone) BETWEEN 1 AND 64
  AND z.zone ~ '^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)*$'
  AND EXISTS (SELECT 1 FROM pg_timezone_names AS t WHERE t.name = z.zone);
