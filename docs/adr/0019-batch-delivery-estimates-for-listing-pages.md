# ADR 0019: Batch delivery estimates for listing pages

## Status

Accepted (2026-11-09). Closes the delivery-promise half of network gap G60 (storefront NG5). Amends the MCP charter of [ADR 0007](./0007-mcp-server-adoption.md) (one more read tool) and the tool list of [ADR 0013](./0013-agent-authority-levels-and-complete-mcp-surface.md). Number 0018 was taken by the analytics read side, built in parallel.

## Context

Every quote (`POST /delivery-quotes`) persists one `DeliveryOffer` row per (group, tier) with a TTL, raises `DeliveryOfferQuoted` through the outbox, and stores an `idempotency_keys` row when the caller sends a key. That is right for a product page and for checkout, where the shopper may promise against the offer. It is wrong for a product LISTING page (search results, many items per view): quoting each item would write one offer row and one analytics event per product per page view, none of which can ever back a promise.

The storefront still wants a delivery date on every listing tile: the displayed promise date moves conversion and profit even when the network is unchanged. What it needs is the same answer the quote would give, computed and returned, with nothing stored.

The fleet REST guideline ([ADR 0012](./0012-adopt-the-fleet-rest-api-guideline.md), STANDARD 1.3e) models a computation with no aggregate id as `POST /{result-noun}` at collection level, like `POST /delivery-quotes`. The guideline makes every POST idempotent (section 2.1) and treats the safe methods by their semantics: a key on a `PUT` or `DELETE` "is accepted and ignored if sent". A POST that writes nothing is idempotent by effect in the same way, so it needs no key.

## Decision

The contract, verbatim:

1. New route POST /delivery-estimates. It is a noun route: a computation over a request body, per the repo's REST guideline ADR. It is SAFE: no persistence, no outbox, no event, no holds, no offer rows. It needs no Idempotency-Key; state in the spec that the key is not required and that, if sent, it is ignored. Follow how the guideline treats safe POSTs.
2. Request:
   - destinationPostalPrefix (string, same validation as quotes);
   - tiers? (subset of the service tiers; default all);
   - items: 1..50 of {catalogItemId, quantity 1..999}, unique catalogItemId (refuse duplicates with 422 validation-failed).
3. Response 200: {computedAt, destinationPostalPrefix, estimates:[...]}, in request order. Each estimate is {catalogItemId, quantity, options:[...], constraints:[...]}.
   - options: [{tier, window{start, end, timeZone}, basis, orderByAt?}]. These are the same fields and semantics as a quote's best offer per tier (orderByAt as in F1, timeZone as in ADR 0011).
   - constraints: [{tier?, code}], the same OfferConstraint vocabulary as quotes (DESTINATION_NOT_SERVED, ITEM_QUANTITY_NOT_AVAILABLE, DELIVERY_SLA_UNAVAILABLE, NODE_CAPACITY_EXHAUSTED, PAST_ORDER_DEADLINE).
4. Semantics:
   - each item is searched as a single-line group with EXACTLY the quote search: the same read models, OwnerChoice (ADR 0017), netting, scorecard handicap and MEASURED/SCHEDULED/NOMINAL basis;
   - one consistent read snapshot per request where the repo has that notion; otherwise one load of the shared inputs per request, never per item;
   - an estimate is advisory: it never reserves capacity, and the response says so in its description.
5. Errors: 422 validation-failed (bad prefix, 0 or more than 50 items, duplicates, bad tier). An unknown catalogItemId is NOT an error: it yields the ITEM_QUANTITY_NOT_AVAILABLE constraint.
6. MCP: add a read tool `estimate_delivery` bound to the same use case if the repo's MCP governance allows a safe POST-backed read. Otherwise skip it and say why. Respect the five-pin rule when growing the surface (Reads interface comment, maxTools, wantTools sorted, the authority want-map, cmd/mcp main_test count, regenerated golden).
7. ADR: the next free number (expected 0018; if the sibling analytics child takes 0018, take the next), 'Batch delivery estimates for listing pages'.
8. Tests:
   - the use case (no repository writes: assert zero rows in offers/outbox via the memory doubles and in an integration test);
   - validation;
   - HTTP golden/schema;
   - a performance guard (a 50-item request performs a bounded number of read-model loads: assert the call counts on the doubles).
   Regenerate the REST docs. Spectral must be clean.

How it lands here:

- **One use case, `EstimateDeliveries`, over the quote's own code.** It validates the command, reads the current policy once and assembles ONE search input for the union of the items (`searchInput`: the service area, the station, one `NetworkViews.Load` for every item id, one `HeldQuantities`, one `ConfirmedHolds`, one `Books.List`). It then runs `search.Run` once per item over that shared input with `Lines` set to the item alone: a single-line search sees only its own item's listings, so the answer equals a one-line quote's. Each group's tiers go through the quote's `offersOfGroup`, which builds the offer in memory exactly as the quote does (same best candidate per tier, same `ErrOfferExpiryTooLate` fallback to `DELIVERY_SLA_UNAVAILABLE`); the estimate keeps the offer's tier, window, basis and `orderByAt` and drops the offer and its event. No offer id is minted, nothing is saved, no unit of work is opened, nothing is appended to the outbox.
- **One load per request, not a snapshot.** The repository has no read-snapshot notion (the unit of work is a READ COMMITTED transaction for writes, and the quote reads outside it), so the estimate does not introduce one: the shared inputs are loaded once per request and every item is searched against that same in-memory copy. The measured-transit lookup (ADR 0014) is memoised per (origin, destination) pair for the request, so 50 items never read the same pair 50 times.
- **The window keeps the contract's names.** An option's `window` is `{start, end, timeZone}`: `start` and `end` are the quote offer's `window.earliest` and `window.latest`, `timeZone` the delivery station's IANA zone (always set: a computed window always names its calendar). `orderByAt` is the offer's `orderByAt`, omitted only when it is unknown.
- **Constraints.** A constraint that applies to the item on every tier alike, the quote's whole-quote `DESTINATION_NOT_SERVED` and `ITEM_QUANTITY_NOT_AVAILABLE` (the two the quote reports without a `groupNo`), is listed once with `tier` omitted; every other constraint names its tier. Options and constraints are ordered by tier (SAME_DAY, NEXT_DAY, TWO_DAY, STANDARD).
- **The key is ignored.** `POST /delivery-estimates` is registered without the Idempotency-Key middleware: a key that is sent is never read, never stored and never answered with `Idempotency-Replayed` or `409 idempotency-key-reused`. The OpenAPI operation still declares the header (`required: false`, described as ignored), a `409` response and the `Idempotency-Replayed` 2xx header, each described as never produced, because the fleet Spectral rules R3-R5 require them on every POST.
- **MCP.** The charter of ADR 0007 is read-only: it keeps quoting on REST because a quote persists offers. An estimate persists nothing, so it fits the charter, and this ADR is the amendment ADR 0007 asks for. `estimate_delivery(destinationPostalPrefix, items, tiers?)` is authority `L1` (it reports what the read models allow, changes nothing and proposes no action), annotated read-only and idempotent, bound to the same `EstimateDeliveries`. The five pins move together: the `Reads` interface and its comment, `maxTools` 5 -> 6, `wantTools` (sorted), the stateless authority map, `cmd/mcp` main_test's tool count, and the regenerated golden. `tiers` is the first optional tool argument of this server; the governance test lists it as a documented exception and the input field carries `omitempty`. `cmd/mcp` now wires the measured-transit read model like `cmd/api`, so both surfaces compute the same basis.

## Consequences

- Listing pages can show a delivery date per tile for up to 50 items in one call, with no write load on the offer table, the outbox or the idempotency table, and no analytics noise from `DeliveryOfferQuoted`.
- An estimate is advisory and is not an offer: it carries no offer id, cannot back `POST /promises`, and never reserves capacity. A shopper who goes on to buy still gets a quote (product page or checkout), which may differ if the network moved in between.
- A 50-item request costs a fixed number of read-model loads (policy, area, station, network, provisional holds, confirmed holds, books) plus at most one measured-transit read per lane pair, independent of the item count; the CPU cost is one search per item over the shared input.
- Without a read snapshot, the shared inputs are read in several statements, as the quote's are: a projection landing between two statements can be seen by one and not the other. The estimate is consistent with itself (every item uses the same copy) but not a point-in-time cut of the database.
- The OpenAPI operation documents a `409` and an `Idempotency-Replayed` header it never produces, to satisfy the fleet lint; the descriptions say so.
