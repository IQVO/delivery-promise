# ADR 0017: Owner-aware availability and promise netting

## Status

Accepted (2026-11-08). Amends assumption A3 of [ADR 0002](./0002-aggregates-and-invariants.md), the AvailabilityView of [ADR 0003](./0003-consumed-contracts-and-local-read-models.md), the netting key of [ADR 0015](./0015-confirmed-units-stay-decremented.md) and the lock rehome of [ADR 0016](./0016-book-charge-follows-the-locked-node.md). Closes the delivery-promise half of network gap G47; `order-sourcing` closes the other half with the same OwnerChoice rule.

## Context

`offer-availability` lists one item at one node once per OWNER: a listing carries an optional `ownerRef` (the `sellerId`; omitted means operator-owned) on `ListingCreated` and `ListingAvailabilityChanged`, and its own uniqueness is `(catalogItemId, nodeId, ownerRef)` (its ADR 0006). `ListingSuppressed` and `ListingRestored` name only the listing and the node.

delivery-promise read availability by `(catalogItemId, nodeId)` and summed every listing of that pair. Two owners' listings of one item at one node therefore collided: two sellers with 3 units each made a line of 5 look coverable, although no single owner can ship it. Unit netting was per `(node, item)` too: provisional holds (ADR 0002, A3) and confirmed holds until the figure catches up (ADR 0015, `ReservationLag`), so a hold taken against one seller's stock reduced every owner's figure. The book charge follows the locked node (ADR 0016), and the rehome did not ask whether an owner at the locked node can cover the lines.

## Decision

The contract, verbatim:

1. AvailabilityView is keyed by (catalogItemId, nodeId, ownerRef), operator-owned = '' (never NULL in a key). Fed from ownerRef on the consumed listing events: absent means ''. The `asOf` monotonic rule applies per key. Write a migration with the next free number: the backfill sets ownerRef ''. Update the migration tests' ceilings and step counts.
2. Netting is per (node, item, ownerRef): provisional and confirmed holds (ADR 0015 semantics unchanged) record the ownerRef they took.
3. A quoted line must be covered by ONE owner: Available(node, item, owner) >= qty. Summing across owners is forbidden.
4. OwnerChoice rule (deterministic, shared with order-sourcing):
   - operator-owned ('') first, if it covers;
   - otherwise the covering seller owner with the highest available (after netting);
   - ties go to the lowest ownerRef in byte order;
   - no covering owner means the node is not a candidate for that line (existing ITEM_QUANTITY_NOT_AVAILABLE path).
   A pure domain function with slice-table tests.
5. DeliveryOffer.lines[] and Promise lines persist the chosen `ownerRef` (four-places rule + repocontract).
   - MakePromise re-validates with the same rule. If the owner chosen at quote time no longer covers but another owner does, the promise takes the new owner. That is not a re-quote.
   - The REST responses of POST /delivery-quotes, GET /delivery-offers/`{offerId}` and GET /promises(/`{id}`) gain optional `lines[].ownerRef` (omitted when operator-owned). Regenerate the REST docs and the MCP golden.
   - PromiseMade.data lines (if it carries lines) gain optional ownerRef. Do not add new events.
6. Rehome on lock (ADR 0016): when the locked node differs, re-net under the locked node using the same OwnerChoice at that node. If no single owner covers there, keep the charge where it was and log, consistent with ADR 0016's 'a lock is a fact' rule.
7. ADR: the next free number (expected 0017), 'Owner-aware availability and promise netting'. It names the OwnerChoice rule verbatim and states that order-sourcing applies the same rule.
8. Tests:
   - OwnerChoice table;
   - two owners with 3 each and a line of 5 gives ITEM_QUANTITY_NOT_AVAILABLE for that node;
   - netting isolation (a hold on owner A does not reduce owner B);
   - the ADR 0015 lag still per key;
   - projection keyed test; migration integration test; repocontract round-trips; encoder goldens.
The search package is performance-sensitive: keep lookups indexed by key, with no full scans per quote.

How it lands here:

- The OwnerChoice rule is `shared.ChooseOwner` (`internal/domain/shared/owner.go`), a pure function over the per-owner net units of one (node, item). `order-sourcing` applies the same rule, with the same ordering, to its sourcing decision; the two contexts must keep them identical.
- `rm_listings` gains `owner_ref text NOT NULL DEFAULT ''` (migration 0005; existing rows are backfilled to `''`) and the index `(node_id, item_id, owner_ref)`. A listing id still names its row, because the suppression events carry only the listing id; since the producer allows one listing per key, the `asOf` of a listing is the `asOf` of its key.
- Each line of an offer and of a promise records the owner it was netted against (`shared.Line.OwnerRef`, `''` for operator-owned). The holds queries group by that owner, so a provisional or confirmed hold only nets the figure of its own key, and ADR 0015's release test compares the figure of that same key.
- The search receives the per-owner units of a (node, item) through an indexed lookup built once per quote; a node is a candidate for a line only when `ChooseOwner` finds a single owner, and the candidate carries the chosen owner of each line, which the offer persists.
- A revision (re-promise) takes the owners the search chose at its new witness node.

## Consequences

- A cart line larger than any single owner's stock at a node is no longer offered from that node, even when the owners together hold enough; splitting a line across owners is not supported.
- A promise's owner can change between the quote and MakePromise when the rule picks another owner then; the lines' items and quantities, which the caller compares, are unchanged.
- Snapshots persisted before this ADR restore with every line operator-owned, which is what the listings they were netted against were.
- The REST responses and `PromiseMade` gain an optional `ownerRef` per line, omitted for operator-owned lines; requests do not accept it (the owner is the promise side's choice, not the caller's).
