# ADR 0016: the PromiseBook charge follows the node order-sourcing locks

## Status

Accepted (2026-11-05). Amends the witness-follows-the-quote assumption of [ADR 0002](./0002-aggregates-and-invariants.md) (A13) for locked promises; it supersedes nothing else.

## Context

A hold is charged on the book of the witness path (`nodeId:pickupDepartureId`) chosen when the offer was quoted. `order-sourcing` assigns the order later and may lock a different node (`FulfillmentLocked.nodeId`, `pickupDepartureId`): the quoted node can be out of stock, rejected, or no longer the cheapest candidate. `OnFulfillmentLocked` recorded the locked node in the assignment read model and did nothing else, so the quoted node's book kept a confirmed charge it would never ship, the node that really fulfils was never charged, `ConfirmedHolds` / `HeldQuantities` netted the wrong node's stock, and a later cancel released the quoted node's book (`Promise.BookID()` is the key of both the charge and the release).

## Decision

- `FulfillmentLocked` is consumed with the new optional field `pickupDepartureId` (tolerant: absent on older producers, which then change nothing).
- When a CONFIRMED or AT_RISK promise is locked on a (node, pickup) other than its witness, the lock handler, in its own transaction: holds and confirms the locked pickup's book (opened lazily from the node's profile), releases the confirmed hold on the old book, and re-points the promise at the new node, pickup and `bookId` (`Promise.Rehome`; terms, legs and wave are unchanged and no event is raised).
- From then on the charge, the netting of units (ADR 0015) and the release on cancel all use the locked node.
- A lock that cannot be charged (departure unknown to the read model, book full or closed) is logged and leaves the charge where it was: the lock is a fact that already happened, retrying a consumer cannot change it.

## Consequences

- Legs and wave of the witness still describe the quoted path; only the node, pickup and book follow the lock. Risk detection by departure therefore keys on the locked pickup.
- A lock on a full book keeps the old (wrong-node) charge and logs a warning; reporting it as a metric is a follow-up.
