# ADR 0002: aggregates, invariants and the assumptions the model left open

## Status

Accepted (2026-10-23).

## Context

`model/model.yaml` (retail-network-docs, context `delivery-promise`), the Model Gap Register (G8, G9, G14, G19, G20, G21, G22 and the items it names) and `contracts-delta` (E1, E5-E9, E12, E13, E15, E18) define this context. This ADR fixes the domain packages that implement them and records, as numbered assumptions, every place where the model is silent. Assumptions are final unless the orchestrator overrules them.

## Decision

### Domain packages (`internal/domain/*`, pure Go: no I/O, clock and ids passed in, `Snapshot`/`Restore`, optimistic `version`)

| Package | Aggregate / role | States | Commands |
|---|---|---|---|
| `shared` | Event interface, `Header`, errors, `Tier`, `Basis`, `Line`, `Window`, `Witness` | | |
| `offer` | `DeliveryOffer` (root `offerId`) | QUOTED, EXPIRED, CONSUMED | Quote (QuoteDeliveryOffers), Consume (from MakePromise), Expire (ExpireOffers) |
| `promise` | `Promise` (root `promiseId`) | PROVISIONAL, CONFIRMED, AT_RISK, KEPT, BROKEN, RELEASED, CANCELLED | Make, Confirm, FlagAtRisk, Revise, Judge, Cancel, ReleaseExpiredHold |
| `book` | `PromiseBook` (root `bookId = nodeId:pickupDepartureId`) | OPEN, CLOSED | Open (lazy), Hold, Confirm, ReleaseProvisional, ReleaseConfirmed, Close |
| `policy` | `PromisePolicy` (root `policyVersion`), G19 | immutable versions | Publish |
| `calendar` | business-time arithmetic over operating hours, holidays and a time zone | | |
| `search` | PromisePolicy search, ShipmentGrouping, ScorecardHandicap (pure functions over a network snapshot) | | |

### Invariants (Err names are the model's; HTTP problem slug = kebab-case without `Err`)

| Err | Rule | Enforced in |
|---|---|---|
| `ErrOfferExpiryTooLate` | `expiresAt <= min(quotedAt + offerTtl, latest instant the witness cutoff can still be met)`; never returned over HTTP: the search never builds such an offer | `offer.Quote` |
| `ErrOfferNotQuotable` | an expired or consumed offer cannot back a promise (also `now >= expiresAt`) | `offer.Consume` |
| `ErrInvalidPromiseTerms` | `deliverByEarliest <= deliverByLatest` and `shipBy < deliverByEarliest` | `promise.Make`, `Revise` |
| `ErrNominalBasisNotAllowedForTier` | a fast-tier promise has basis SCHEDULED | `offer.Quote`, `promise.Make` |
| `ErrPromiseHoldExpired` | confirm only before `holdExpiresAt` | `promise.Confirm` |
| `ErrRevisionMovesEarlier` | a revision never moves `deliverByLatest` earlier | `promise.Revise` |
| `ErrPromiseClosed` | KEPT, BROKEN, RELEASED and CANCELLED are terminal | every promise command |
| `ErrGroupSpansNodes` | a promise covers lines shippable from the witness node | `promise.Make` |
| `ErrPromiseCapacityExhausted` | `provisional + confirmed <= share` | `book.Hold` |
| `ErrBookClosed` | no hold after the book is CLOSED or after `cutoffAt - handling` | `book.Hold` |
| `ErrCapacityUnderflow` | a release or confirm never makes a counter negative; internal guard | `book` |
| `ErrIllegalTransition` (shared) | the command is not allowed from the state | all aggregates |
| `ErrValidation` (shared) | a value-object rule is broken | all constructors |
| `ErrNotFound`, `ErrConcurrentUpdate` | repository miss, version mismatch: port sentinels, not domain errors | `ports` |

### Transitions

Offer: QUOTED -> CONSUMED (Consume), QUOTED -> EXPIRED (Expire). Promise: the table of the model (`PROVISIONAL -> CONFIRMED | RELEASED`, `CONFIRMED -> AT_RISK | CONFIRMED (revise) | KEPT | BROKEN | CANCELLED`, `AT_RISK -> CONFIRMED (revise) | KEPT | BROKEN | CANCELLED`); every other (state, command) pair is `ErrPromiseClosed` for the four terminal states and `ErrIllegalTransition` otherwise. Book: OPEN -> CLOSED.

### Transactions

`MakePromise` consumes the offer, creates the promise and holds the book (opening it lazily) in ONE local transaction (G22, documented exception to one aggregate per transaction). A revision that changes the book holds the new book, confirms it and releases the old one in one transaction. Every use case saves its aggregates AND appends its outbox rows in one transaction; a Kafka-driven use case also inserts the CloudEvents id into `processed_events` in it.

### Assumptions (numbered, final)

- A1. **Nominal path** (STANDARD only, contracts-delta E8): used when no SCHEDULED candidate satisfies STANDARD for any node. Pickup cutoff = the node's next profile cutoff instant at or after `ready`; transit = the shortest path over OPEN lanes of `nominalTransitMinutes` plus the `sortDwellMinutes` of intermediate nodes; the window is the destination station's next local calendar day after arrival, 09:00-21:00 local; at most 7 calendar days. The witness path carries no departure or wave ids; its book is `nodeId:nominal-<cutoff as yyyyMMddTHHmmZ>`.
- A2. **ShipmentGrouping** is a deterministic greedy cover: one node for the whole cart when one can cover every line, else repeatedly the node covering the most remaining lines (ties by node id). Quotes are per group (`groupNo` from 1, G8); a group's candidate nodes are all nodes that cover all its lines.
- A3. **Availability** of an item at a node = `availableQty` of its listing minus the units of PROVISIONAL promises held on that node; discontinued items, suppressed listings and nodes that are not ACTIVE FULFILLMENT_CENTERs with an operational profile are not candidates.
- A4. **ScorecardHandicap**: grade B multiplies the handling time by 1.25 (rounded up to a minute); C skips one more feasible pickup, D two; A, PROVISIONAL (E18) and no scorecard add nothing. Only the `30d` window is stored; the `7d` window is ignored.
- A5. **Tier fit**: a candidate fits a tier when the calendar days between the destination station's local date of `now` and of `window.latest` are at most 0 / 1 / 2 / 7. SAME_DAY additionally needs a SUB_SAME_DAY or MICRO fulfillment centre whose pickup goes straight to the destination station.
- A6. **Packages**: each order needs `packagesPerOrder` (policy, default 1) packages of room on the pickup and every linehaul departure (G9).
- A7. **Earliest arrival** is a time-expanded earliest-arrival search: at each intermediate node `sortDwellMinutes` (0 when unknown) is added, then the first non-PICKUP departure with `cutoffAt` at or after that instant and room; a departure on a lane known to be closed is skipped, an unknown lane is tolerated.
- A8. **Wave**: the first wave at the destination station that is not cancelled, has `inductCutoffAt` at or after the arrival and stops left; ties by window end.
- A9. **Best per tier**: smallest `deliverByLatest`; ties: node in the destination region, fewer legs, better grade (A, B, C, D; PROVISIONAL and none count as A), then node id.
- A10. **Expiry**: `expiresAt = min(quotedAt + offerTtl, cutoff minus the handling time counted in business minutes)`. A candidate whose expiry is not after `now` is infeasible.
- A11. **Constraints**: DESTINATION_NOT_SERVED (no service area or station), ITEM_QUANTITY_NOT_AVAILABLE (a line no node covers), PAST_ORDER_DEADLINE (G19, tier deadline passed in the station's local time), NODE_CAPACITY_EXHAUSTED (a path exists when capacity is ignored), else DELIVERY_SLA_UNAVAILABLE. The first three apply to the whole quote, the last two to a (group, tier).
- A12. **Book share** (G9): `share = ceil(min(CutoffSlot.maxOrders if any, throughputCap x window) x capacitySharePercent / 100)`, at least 1. `window` = the node's operating time between its previous cutoff and this one: HOUR cap = `value x minutes/60`, DAY cap = `value x minutes / operating minutes of the cutoff's day`. `capacitySharePercent` (policy, default 100) is the "proportional share of D4" of G9. The book opens lazily on the first hold, with `holdDeadline = cutoffAt - handling` (business minutes).
- A13. **Book closing**: a clock job closes OPEN books at `cutoffAt` (G9); `Hold` also refuses from `holdDeadline` (the model invariant). Confirm and release stay legal on a CLOSED book so an in-flight confirm never fails for the clock.
- A14. `PromiseBookExhausted` is raised every time a hold makes `provisional + confirmed = share`.
- A15. **Hold**: `holdExpiresAt = now + holdTtlMinutes` (policy, default 15). Defaults without a published policy (`policyVersion v0`): offer TTL 10, hold TTL 15, packagesPerOrder 1, share 100, no deadlines.
- A16. **Confirm** is idempotent: a CONFIRMED promise with the same `orderId` and `groupNo` is a no-op (200); another order is `ErrIllegalTransition`. The reconciling consumer of `CustomerOrderPlaced` applies the same rule and logs `ErrPromiseHoldExpired`.
- A17. **Revision** (RevisePromise): candidates from the search for the promise's tier, then slower tiers down to STANDARD; the first whose `deliverByLatest` is not earlier than the current one and whose book accepts the hold wins; excluded nodes (`FulfillmentUnsourceable.excludedNodes`) and excluded departures (cancelled, failed bookings) are skipped; once an assignment is LOCKED only the locked node is a candidate (ADR 0005 of the model: the node never changes after lock). Revision numbers start at 1.
- A18. **Judgement**: a group is judged when its LAST shipped package is delivered (`deliveredAt` = the latest), or at the first package LOST or RETURNED (E1, E6). `lateByMinutes` is rounded up to whole minutes; for LOST and RETURNED it is the rounded-up minutes from `deliverByLatest` to the event instant, 0 if not later, and `deliveredAt` is omitted.
- A19. **PromiseUnkeepable** `{promiseId, orderId, groupNo, reason}` (E9), integration event, raised at most once per promise when RevisePromise finds no candidate; the promise stays AT_RISK until customer-ordering cancels it.
- A20. **Analytics events**: `DeliveryOfferQuoted` (one per persisted offer) and `PromisePolicyPublished` have the payloads of `apis/asyncapi.yaml`; with `PromiseMade`, `PromiseConfirmed`, `PromiseReleased` and `PromiseCancelled` they go ONLY to the analytics topic.
- A21. **PromisePolicy** (G19) is a versioned aggregate: `POST /promise-policies` stores version N+1, `GET /promise-policies/current` reads the highest. `orderDeadlines` are `{tier, regionId?, localTime}` in the destination station's time zone; a region-specific entry overrides an all-region one.
- A22. **Problem slugs** follow the shared conventions: `not-found`, `illegal-transition`, `concurrent-update`, `validation-failed` replace the pack's `offer-not-found`, `promise-not-found`, `stale-write`, `invalid-request`; the model slugs (`offer-not-quotable`, ...) are kept; `idempotency-key-reused` (422) is infrastructure.
- A23. **Idempotency**: `Idempotency-Key` is required on every state-changing POST except the quote, where it is optional (a repeat inside the TTL returns the same offers).
- A24. `CustomerOrderCancelled` with an empty or absent `groupNos` cancels every group of the order.
- A25. **Out-of-order feeds**: capacity events that arrive before their schedule event create an unscheduled row that the search ignores; package outcomes and shipments are stored and the group judgement is evaluated on every event that can complete it (shipment, delivery, loss, return, lock).
- A26. The CloudEvents entities of `BookingMoved`, `BookingFailed` (`departure`) and `PackageInductionDeferred` (`package`) are best guesses until their producers push an AsyncAPI; the consumed type table is one file (`internal/adapters/inbound/kafka/types.go`).
- A27. **Re-validation at MakePromise** (documented in the model: "re-validates the offer's witness path"): the pickup, every leg and the wave of the witness must still exist, not be cancelled, and have room. A failure is `ErrPromiseCapacityExhausted` so checkout re-quotes.
- A28. A stale write is retried up to three times by the use case, then reported as `concurrent-update`.
- A29. Domain bounds are plain literals: quantity <= 2147483647, lines per group <= 100, ids <= 64 characters, postal prefix 1-10 alphanumerics.

## Consequences

- Every assumption has a table-driven test in the package that enforces it.
- `model_gaps` still open for the orchestrator: A1, A2, A12 (share formula), A17, A19, A20, A21, A26.
