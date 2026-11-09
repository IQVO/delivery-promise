# ADR 0023: Offers carry a capacity band

## Status

Accepted (2026-11-09). Amends the offer shape of [ADR 0002](./0002-aggregates-and-invariants.md) (one more optional, computed field), the estimate options of [ADR 0019](./0019-batch-delivery-estimates-for-listing-pages.md) and the `DeliveryOfferQuoted` payload catalogued in [ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md) (one more optional field; no new type). Numbers 0020 to 0022 are taken by siblings built in parallel (calibration, late risk, tier eligibility).

## Context

A delivery offer says WHEN a package arrives. It says nothing about how much of the capacity behind that date is already spoken for. The PromiseBook of the offer's chosen window (one node, one pickup departure) holds a finite number of orders (its share); the more of it is provisional or confirmed, the more likely the next quote is the last one that fits, and the more a storefront may want to treat that window differently (a nudge, a different default, a price signal).

The network must stay out of pricing. What it can do is publish the pressure it already measures, as a coarse, stable signal that a consumer reads from the response it already receives.

## Decision

The contract, verbatim:

- `DeliveryOffer.capacityBand` is `AMPLE` | `TIGHT` | `SCARCE`, computed at quote time from the book that the offer's chosen window would charge: utilisation `u = (provisional + confirmed) / capacity share` of that book (the same quantities the capacity check uses: `Book.Hold` refuses when `provisional + confirmed >= share`).
  - `u < 0.60` -> AMPLE; `0.60 <= u < 0.85` -> TIGHT; `u >= 0.85` -> SCARCE.
  - The thresholds are constants in the domain (`shared.TightFromPercent` 60, `shared.ScarceFromPercent` 85), NOT PromisePolicy fields, to keep this change free of a persisted-policy edit.
  - Omit the field when the book has no finite capacity or the offer has no book. Never emit an empty string.
- Where it appears (all additive, optional): the quote/offer REST schema, each option of `POST /delivery-estimates`, the MCP tool outputs for `quote_delivery` and `estimate_delivery` (and the MCP output schema and the golden fixtures), and the `DeliveryOfferQuoted` analytics event.
- The network never computes a fee. The storefront `pricing` context may read the band from the quote response it already receives; that change is outside this repository.
- It must NOT change which offers are produced, their windows or their order.

How it lands here:

- **Pure function of two integers.** `shared.CapacityBandFor(used, share)` returns the band and whether there is one. The integer comparison is exact: SCARCE when `used * 100 >= share * 85`, TIGHT when `used * 100 >= share * 60`, otherwise AMPLE, so 0.59 and 0.84 never round up. A `share` of zero or less (no finite capacity) and a negative `used` yield no band.
- **The search reads the book it already reads.** `search.BookView` gains `Share` and `Used` (provisional plus confirmed), filled where the book rows are loaded. For a book that does not exist yet, the lazily computed `Opening.Share` is the share and nothing is used yet, so a fresh book is AMPLE. `Candidate.CapacityBand` is set in the same place that picks the candidate's `BookID`, for SCHEDULED, MEASURED and NOMINAL candidates alike. It is not an input to ranking: the candidates, their order and their windows are exactly what they were.
- **The offer carries it, the store does not.** The band is a snapshot of the pressure at quote time, so `offer.Quote.CapacityBand` sets a read-only `Offer.CapacityBand()` and the `DeliveryOfferQuoted.CapacityBand` field. It is NOT part of `offer.Snapshot` and has no column: there is no migration. A stored offer read back later (`GET /delivery-quotes/{quoteId}`) restores without a band and omits the field, because a stale pressure reading would be misleading; the band exists on the quote response and on the estimate that were computed from live books.
- **Wire.** `capacityBand` is an optional enum on the OpenAPI `DeliveryOffer` schema and on the estimate option, optional on the MCP tool outputs, and an optional string on the `DeliveryOfferQuoted` analytics schema. It is omitted, never empty, when unknown. Consumers of the analytics event decode tolerantly and ignore it.
- **MCP.** The server is read-only (ADR 0007) and has no quote tool: a quote persists offers, so quoting stays on REST. The band therefore reaches MCP through `estimate_delivery` (each option carries it, bound to the same use case as `POST /delivery-estimates`) and through the output schema of `get_delivery_offer`, which shares the offer view but always omits the band because a stored offer has none. The tool descriptions, the output schemas and the registry golden say so.

## Consequences

- A storefront can show or price on three buckets of capacity pressure without a call, from the quote or the listing estimate it already makes.
- The offers, windows and order of a quote are byte-identical to before except for the new field; a test compares the offers with the field stripped.
- The thresholds are code constants. Changing them is a code change with an ADR, not a policy publish; a per-policy threshold would need a persisted-policy edit and a migration, which this change avoids on purpose.
- The band describes the book at quote time. By the time the shopper promises, the book may have moved; `POST /promises` is still the only thing that reserves capacity, and it can still answer 409 `promise-capacity-exhausted`.
- A stored offer has no band, so the read of an old quote cannot be used to ask "how tight was it". The analytics event keeps that history.
- The signal says nothing about price. Fees stay in the storefront.
