# ADR 0024: An opt-in CONSOLIDATED tier with a customer-chosen delivery day

## Status

Accepted (2026-11-10). Amends the tier model and the quote contract of [ADR 0002](./0002-aggregates-and-invariants.md) (a fifth tier, one more optional request field, one more optional computed field), the batch estimates of [ADR 0019](./0019-batch-delivery-estimates-for-listing-pages.md) (the estimate refuses the new tier) and the `DeliveryOfferQuoted` and `PromiseMade` payloads catalogued in [ADR 0004](./0004-cloudevents-envelope-and-type-catalogue.md) (one more optional field each; no new type). Built in parallel with ADR 0025 (many assignments per tracking number); the two are file-disjoint apart from the indexes.

## Context

A shopper who does not need a parcel soon can let the network wait: orders of one shopper to one address that are released together ship as one package. The saving only exists when the promise leaves the network time to hold the group, so the shopper must say WHEN they want it, and the quote must say whether the choice leaves room to merge.

Delivery-promise knows four tiers, SAME_DAY, NEXT_DAY, TWO_DAY and STANDARD, and its callers validate exactly those four. A fifth tier on the wire would make the whole quote fail in every caller that has not been taught it, so the new tier must be requested, never volunteered.

## Decision

The contract, verbatim:

- **Tier.** `CONSOLIDATED` is a fifth tier. `shared.AllTiers()` stays the four classic tiers (the default quote set), `shared.KnownTiers()` returns five, `Valid()` accepts five and `Rank()` orders CONSOLIDATED last (4). `MaxCalendarDays(CONSOLIDATED)` is `MaxConsolidatedCalendarDays` = 14. `AllowsNominal()` is true for STANDARD and CONSOLIDATED (the `nominal_fallback`). `SlowerOrEqual()` of STANDARD is `[STANDARD]` and of CONSOLIDATED `[CONSOLIDATED]`: a re-promise never falls through from one to the other. A CONSOLIDATED promise has no `TierOrderDeadline` configured, so `PastDeadline` is false; its calibration segment is its own and stays at buffer 0 until the segment has `minCalibrationObservations`; fast-tier eligibility (ADR 0022) does not govern it.
- **Quote request.** `QuoteRequest` gains the optional `consolidatedDeliveryDay` (`YYYY-MM-DD`, a calendar date of the destination station's time zone). `tiers` keeps "all four when omitted". `CONSOLIDATED` is returned ONLY when `tiers` lists it or `consolidatedDeliveryDay` is present; a present day adds CONSOLIDATED to whatever set `tiers` names (the four classic tiers when `tiers` is omitted). `POST /delivery-estimates` answers 422 `validation-failed` for `CONSOLIDATED`: it is a persistent-offer tier, an estimate stores nothing. The `Tier` schema enum and the offer, promise and MCP output schemas gain the value.
- **`consolidationEligible`.** A boolean on the offer, on the promise terms and on `DeliveryOfferQuoted` and `PromiseMade`; present only for the tier CONSOLIDATED, omitted otherwise and never `false` by default. Persisted with the offer and the promise terms (jsonb snapshot, no migration).
- **Meaning of `consolidationEligible`.** "This option leaves the network at least one local day to merge it with other orders." It is computed from this service's own candidate set and never asserts that another order exists.
- **Local construction (no sibling call), for the CONSOLIDATED tier of a group:**
  1. `fastest` = the minimum `Window.Latest` of the STANDARD candidates of the same group (the same paths, nominal fallback included). None: the tier is unavailable (`DELIVERY_SLA_UNAVAILABLE`, or `NODE_CAPACITY_EXHAUSTED` when the same search without the capacity checks would find one).
  2. The chosen local day `D`: the request's day, else `DefaultConsolidatedOffsetDays` = 2 days after the local day of `fastest`; clamped UP to the local day of `fastest` ("at or after the fastest feasible window"). A `D` more than `MaxConsolidatedCalendarDays` local days from now is `DELIVERY_SLA_UNAVAILABLE`.
  3. Candidates are the pickups and paths a scheduled search builds, but the final wave is the earliest usable wave whose induct cutoff is not before the arrival and whose window END falls on local day `D`; a pickup with none is dropped. With no scheduled candidate the NOMINAL fallback applies: the window is `D` 09:00 to 21:00 local, when the nominal arrival's local date is before `D`.
  4. Rank: the usual order (earliest window end, in region), then the LATER `ShipBy` first (the longest hold), then fewer legs, better grade, node id. The first candidate is the offer.
  5. `consolidationEligible = (D >= local day of fastest + MinConsolidationHoldDays)` with `MinConsolidationHoldDays` = 1. It is `false` when `D` is the fastest day.
- **Promise.** `MakePromise` copies tier, window, `consolidationEligible` and the witness; the capacity hold goes on the chosen pickup's PromiseBook as for any tier.
- **Re-promise.** `ReviseAtRisk` of a CONSOLIDATED promise searches `[CONSOLIDATED]` with the local date of its `Terms.Window.Latest` as a MINIMUM day: the search walks forward day by day up to the horizon, and the existing rule (the first candidate that does not move `deliverByLatest` earlier and whose book accepts the hold) takes the first day that works; none is `PromiseUnkeepable` as today. The chosen day is not a stored field: it is the local day of the promise window.
- **Constants** sit beside the other search constants (`MaxConsolidatedCalendarDays` 14, `DefaultConsolidatedOffsetDays` 2, `MinConsolidationHoldDays` 1). A policy field for them is a deferred follow-up, not part of this decision.

How it lands here:

- **Search.** `search.Input` gains `ConsolidatedDay` (the local date as text, empty for the default) and `ConsolidatedNotBefore` (an instant whose local day is the MINIMUM day; set, the search walks forward day by day and returns the candidates of every day that has one, in day order). `Candidate.ConsolidationEligible` is a `*bool`. The wave choice becomes a function handed to the existing candidate builder, so the scheduled paths are the same code as for every other tier; the earliest arrival of a pickup is memoised per search because the day walk asks for it again. `search.MaxPickups` (six pickups per node) still bounds the pickups: the chosen day moves the WAVE, not the pickup, so a far day is reached from the earliest pickups.
- **Nominal fallback per day.** A day without a scheduled wave falls back to a NOMINAL window of that day, as STANDARD does without a wave. A consequence for a re-promise: when the wave of a CONSOLIDATED promise is cancelled and no other wave ends on its day, the promise is re-promised on the same day on a NOMINAL basis rather than moved to a later day; it moves to a later day only when the nominal path cannot make the day.
- **Horizon.** The read models are loaded for `SearchHorizon` (8 days). A search that asks for CONSOLIDATED loads `MaxConsolidatedCalendarDays + 1` days instead, so a day up to 14 days away can find its waves.
- **No migration.** Offers and promises persist as one jsonb snapshot, and `consolidationEligible` is a nullable value inside it; a row written before this change restores it as unknown.
- **Capacity.** Delivery-promise charges ONE order of capacity per promise, as for every tier. When order-sourcing later merges the groups of two promises into one package, the departure book has been charged for two orders while one package ships: a deliberate conservative over-charge, accepted by the product owner (the alternative needs a shared-charge concept that does not exist).

## Consequences

- Default quotes are unchanged (golden): the four classic tiers, no `consolidationEligible`. A caller that never sends the day nor lists the tier never sees the fifth tier.
- The storefront callers validate four tiers today; they must accept the fifth before they ask for it. The incentive, the picker copy and the day range shown to shoppers belong to the storefront and are an open question for the product owner (an activation gate, not part of this slice). The network sends no fee.
- Order-sourcing and customer-ordering must accept the tier before any CONSOLIDATED group is placed; the tier never leaves order-sourcing (the purchase order carries the fulfilment class STANDARD).
- A far day is limited by the horizon of 14 days and by what the loaded waves cover; a day without a wave is simply unavailable.
- `consolidationEligible = false` is a valid, quotable option: the shopper chose the fastest day and the network has nothing to hold.
- The over-charge on the departure book makes a merged package look fuller than it is; the book recovers as the promises are judged.
