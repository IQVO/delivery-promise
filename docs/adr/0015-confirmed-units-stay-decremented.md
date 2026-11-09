# ADR 0015: confirmed units stay decremented until the feed that accounts for them lands

## Status

Accepted (2026-11-05). Amends assumption A3 of [ADR 0002](./0002-aggregates-and-invariants.md), which netted only PROVISIONAL promises.

## Context

`Available(node, item)` was the listing's `availableQty` minus the units of PROVISIONAL promises. When a promise is confirmed (`ConfirmPromise`, `CustomerOrderPlaced`) it leaves PROVISIONAL, so its units were added back to `Available` at once. Nothing else has taken them over yet: in the docs' worked example (`order-assignment-model.md`, "Worked example, continued") the order is placed at 14:10, the fleet raises the held order at 15:56 and releases it to the floor at 16:02, so the WMS reserves nothing for about 1h50 and no feed computed before that can exclude the units. For that whole time the confirmed units were promisable a second time (`delivery-promise` and `order-sourcing` both read the raw feed figure). The failure surfaced later as a vendor `OUT_OF_STOCK` and a broken or revised promise.

## Decision

- A promise records `confirmedAt` (`Promise.Confirm`, persisted in the snapshot; a promise confirmed before the field existed falls back to its `holdExpiresAt`, the latest it can have been confirmed).
- Units of CONFIRMED and AT_RISK promises stay netted from `Available` (`PromiseRepository.ConfirmedHolds`), exactly like provisional holds, UNTIL the listing figure of that (node, item) was computed at or after `confirmedAt + ReservationLag`. `ReservationLag` is 2 hours: the 1h52 of the docs' example, rounded up.
- The test is on the figure's `asOf`, not on its arrival: a feed that lands late or out of order, or whose `asOf` predates the reservation, releases nothing (the projection already ignores an `asOf` older than the stored one).
- Terminal and cancelled promises (KEPT, BROKEN, CANCELLED, RELEASED) are not netted: their units are shipped or given back.
- The hold TTL and the sweep are unchanged: they still bound only PROVISIONAL holds.

## Consequences

- A confirmed order can no longer be sold twice inside the reservation gap; the cost is that a replenishment fed inside the 2 hours after a confirm stays hidden by the confirmed quantity until a later figure lands (conservative, "zero, never a guess").
- The lag is a constant, not policy: it should become a `PromisePolicy` field if operators need to tune it.
- `order-sourcing` still compares the raw `availableQty` in `CanShip` (a separate repository, not changed here).
