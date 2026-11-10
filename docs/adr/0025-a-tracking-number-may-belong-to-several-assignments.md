# ADR 0025: A tracking number may belong to several assignments

## Status

Accepted (2026-11-10). Amends the shipment read models of [ADR 0003](./0003-consumed-contracts-and-local-read-models.md) (`rm_trackings`) and the judgement rule of [ADR 0002](./0002-aggregates-and-invariants.md) (A18, a group is judged on its last delivered package). Slice C1-DP of the cross-order consolidation design. Number 0024 is taken by the sibling that adds the CONSOLIDATED tier, built in parallel; the two are file-disjoint apart from the index.

## Context

Until now one assignment was one shipment group of one order, one promise, one purchase order and one tracking number set. order-sourcing is about to merge several QUEUED assignments of one shopper and one ship-to into a single release (one purchase order, one package). The merged package carries one tracking number, but every member keeps its own `FulfillmentLocked` and `FulfillmentShipped`, so delivery-promise sees the SAME tracking number listed under several assignment ids.

The shipment read models cannot represent that today: `rm_trackings` has `tracking_number` as its primary key and `PutShipment` re-owns the number on every write, so the last member to ship wins and the others are never found by their tracking number. A `PackageDelivered`, `PackageLost` or `PackageReturned` would then judge ONE promise and leave the others CONFIRMED forever.

## Decision

The contract, verbatim:

- Migration `0009_tracking_many_assignments`: the primary key of `rm_trackings` moves from `(tracking_number)` to `(tracking_number, assignment_id)`. The down step keeps the row with the lowest assignment id per tracking number and restores the old key.
- Port `ShipmentViews.AssignmentOf` becomes `AssignmentsOf(ctx, trackingNumber) ([]string, error)`: sorted ascending, `ports.ErrNotFound` when there is none. `PutShipment` inserts the `(trackingNumber, assignmentId)` pair `ON CONFLICT DO NOTHING` instead of re-owning the number. The memory twin, the Postgres adapter and the `repocontract` cases are updated together.
- `recordOutcome` judges, after `PutOutcome`, the union of the event's `assignmentId` and `AssignmentsOf(trackingNumber)`, each through the existing `judge`. A group is judged when ALL of its shipped tracking numbers have outcomes; the outcome row stays one per tracking number and the first final fact wins, unchanged. `PackageLost` and `PackageReturned` use the same loop: the cause LOST or RETURNED breaks every sharing promise.
- No new consumed type. Followers are known through their own `FulfillmentLocked` and `FulfillmentShipped`.

How it lands here:

- **Each promise is judged on its own window.** `judge` already reads the assignment, its package set and the outcomes of those numbers, then calls `Promise.Judge(verdict, now)`. Two promises that share a package therefore get the verdict of THEIR window: one delivery at the same instant can keep one promise and break the other with `LATE` when their latest times differ.
- **Order of facts does not matter.** A delivery that arrives before a member's lock or shipment is recorded against the tracking number; the member is judged by the later `FulfillmentLocked` or `FulfillmentShipped`, exactly as for a single group (A25). A sharing member that has not shipped yet simply is not in `AssignmentsOf` at the time.
- **Replay is idempotent.** The inbox claims the CloudEvents id; a repeat of the same fact finds the outcome row present and each promise already judged (`Promise.Judge` on a closed promise is ignored as stale).
- **Risk signals that carry a tracking number reach every sharing promise.** `PackageExceptionRaised` and `PackageInductionDeferred` are keyed by the carrier's assignment id and carry the tracking number. When a tracking number is present, every assignment that shares it is flagged AT_RISK, not only the one the event names; an induction deferral without an assignment id (label data not yet seen) is found through `AssignmentsOf` and flags all of them. Departure-level signals (`BookingMoved`, `BookingFailed`) are untouched.
- **Capacity is deliberately over-charged.** Each promise keeps its own PromiseBook charge and re-homes it on its own lock (ADR 0016). One merged package therefore charges N orders of capacity on the departure book while linehaul books ONE package. This conservative over-charge is accepted by the product owner (design question Q4): charging once per merged package would need a new shared-charge concept and is not part of this slice. The sibling ADR 0024 states the same for the CONSOLIDATED tier.

## Consequences

- One `PackageDelivered` on a merged package judges every sharing promise; one `PackageLost` or `PackageReturned` breaks every one. Calibration (ADR 0020) records one observation per judged promise, as for any promise.
- `AssignmentsOf` returns ids in ascending order, so the judging loop and any test are deterministic.
- The migration is reversible. The down step is lossy by design for tracking numbers shared by several assignments (only the lowest id is kept), which is the pre-0009 behaviour and is harmless: the same facts are replayed by the shipped events.
- The PromiseBook over-charges merged orders until each promise is judged or released. Capacity bands (ADR 0023) may read TIGHT a little earlier than the physical load warrants.
- Consolidation must stay OFF in order-sourcing until this service is deployed (a merged package before this change would leave follower promises un-judged). The activation gate is recorded in the design; there is no env var, secret or chart value on this side.
- The risk extension for shared tracking numbers is a deliberate widening of the contract above, so that a package exception on a merged package does not leave the followers' promises unflagged. It never changes a promise that does not share a tracking number.
