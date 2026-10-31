# ADR 0011: the delivery window names its local time zone

## Status

Accepted (2026-10-30).

## Context

A delivery window is two instants. For the worked example, NEXT_DAY in Austin, the window 09:00-21:00 local time on
2026-10-31 is `2026-10-31T14:00:00Z` .. `2026-11-01T02:00:00Z`. A consumer that shows the shopper delivery DATES
(the cart, the checkout) has to turn those instants into days. A delivery date is a day of the destination's local
calendar, but the instants alone do not say which calendar: taking their UTC dates turned one evening in Austin
into "Oct 31 - Nov 1". Only this context knows the calendar. The search already works in the delivery station's
zone (`search.Node.Loc`, loaded from the node-topology `Timezone` of the station that serves the destination
postal prefix, `usecases/network.go`): the nominal window is "the station's next local day, 09:00 to 21:00" and the
order deadlines are local times of that zone. The contract just never said so.

The model (`network/model/model.yaml`) keeps `deliverByEarliest`/`deliverByLatest` as instants and is silent on the
zone. The REST contract and the AsyncAPI payloads are published; consumers decode tolerantly (ADR 0003), so an added
optional field is non-breaking, a changed or removed one is not.

## Decision

- **The window carries the zone.** `shared.Window` gains `TimeZone`, the IANA name of the delivery station serving
  the destination (`America/Chicago`). The search sets it on every candidate, SCHEDULED and NOMINAL alike, from the
  station's location (`searcher.timeZone()`, the `time.Location` name; a station without a loadable zone is searched
  in UTC and names `UTC`). It travels unchanged into the offer, into the promise terms when the offer is consumed,
  and into the new terms of a re-promise; the revision history keeps the zone of the terms it replaced.
- **Always set for new data, optional for old.** `offer.New` refuses a quote whose window has no zone
  (`ErrValidation`), so every offer quoted from now on names one. `Window.Validate` accepts an empty zone, and
  `offer.Restore`, `promise.Make` and `promise.Restore` therefore accept a window persisted before this ADR. A zone
  that is present must have the shape of an IANA name (`shared.ValidTimeZone`: 1..64 ASCII letters, digits, `/`,
  `_`, `-`, `+`); the domain does not consult a zone database.
- **REST (additive, optional).** `DeliveryWindow` (the `window` of `POST /delivery-offers:quote` and
  `GET /delivery-offers/{offerId}`) and `PromiseTerms` (the `terms` and `previousTerms` of `GET /promises/{promiseId}`
  and `GET /promises?orderId=`) gain an optional `timeZone` string. It is omitted, never empty, when the stored
  window has none; a consumer then reads the window in UTC, which is what every consumer did before. The instants
  stay RFC 3339 UTC. `openapi.yaml` goes to 0.2.0.
- **Events (additive, optional).** `timeZone` is added as an optional property to exactly the AsyncAPI messages whose
  payload already carries `deliverByEarliest` and `deliverByLatest`: `PromiseMade`, `PromiseRevised` and
  `DeliveryOfferQuoted`. `PromiseKept` and `PromiseBroken` carry only `deliverByLatest` as the judging deadline, an
  instant compared with another instant, and stay as they are. The `dataschema` stays `:v1` (additive evolution,
  `.claude/rules/integration-events.md`); the encoder omits the key when the window has none. Every consumer of these
  types decodes tolerantly, so none is affected. `asyncapi.yaml` goes to 1.1.0.
- **Persistence.** Offers and promises are stored as their jsonb `Snapshot`, so the zone is persisted as
  `Window.TimeZone` / `Terms.Window.TimeZone` without a new column. Migration `0002_window_time_zone` backfills the rows
  written before: a window without a zone gets the zone of the station that `rm_areas` assigns to the row's
  destination prefix, when `rm_nodes` has one that PostgreSQL knows (`pg_timezone_names`) and the domain accepts.
  Other rows keep an absent zone. Re-running it changes nothing. Its down migration is a deliberate no-op: binaries
  before this ADR ignore the key, and stripping it would also erase the zones quoting wrote since, which a re-run
  could only re-derive from today's area-to-station map.
- **Agent surfaces.** The MCP tools and the A2A `promise_status` skill return the same views, so they carry the field;
  their descriptions name it and the MCP tool registry golden records the new optional property.

## Consequences

- A consumer can derive the delivery DATES the shopper is promised: convert earliest and latest into `timeZone`,
  then take the date. The worked example becomes the single day 2026-10-31 in `America/Chicago`.
- The contract states what the search always assumed: a window belongs to the destination station's calendar. A
  station that changes zone in node-topology changes the zone of the next quote, never of an existing offer or
  promise.
- Old rows the backfill cannot place (an area or station no longer projected, a zone no database knows) stay without
  a zone and are read in UTC by consumers, the behaviour they had before; they age out with the offer retention and
  promise judgement.
- The backfill reads today's area-to-station map: an offer or promise quoted before a service-area remap gets the zone
  of the area's current station. Remaps across zones are rare and only affect rows older than this ADR.
- The down migration does not restore the exact pre-0002 data. That is intended: the extra key is harmless to older
  binaries, and keeping it avoids losing zones written after the upgrade.
