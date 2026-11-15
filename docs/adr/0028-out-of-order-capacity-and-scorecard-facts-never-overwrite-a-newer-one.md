# ADR 0028: out-of-order capacity and scorecard facts never overwrite a newer one

## Status

Accepted (2026-11-14). Amends A25 and hotspot H2 of [ADR 0002](./0002-aggregates-and-invariants.md) (a capacity event that arrives before its schedule event) and follows the precedent of [ADR 0022](./0022-fast-tier-candidates-are-filtered-on-node-eligibility.md) (the `changedAt` guard of `NodeTierEligibilityChanged`).

## Context

`OnDepartureCapacityChanged`, `OnWaveCapacityChanged` and `OnScorecardUpdated` overwrote the stored row with whatever arrived last. The context documentation (warehouse-docs, domain-events) says the opposite: DepartureCapacityChanged and DeliveryWaveCapacityChanged apply "newer `time` wins", NodeScorecardUpdated "only if `computedAt` is newer". A retried or replayed older figure therefore brought back room that a newer fact had taken away: the documentation-derived BDD pass reproduced a stale `remainingPackages` reopening a full pickup, so the search quoted a tier whose witness path `MakePromise` then had to refuse as `promise-capacity-exhausted`.

## Decision

- `search.Departure` and `search.Wave` carry `CapacityAt`, the CloudEvents `time` of the newest fact that set `Remaining`. The two capacity handlers drop an event whose `time` is older than `CapacityAt`; an equal instant is the later delivery of the same fact and applies (the ADR 0022 rule). `CapacityAt` is stored in the row's `data` jsonb: no migration.
- `DepartureScheduled` and `DeliveryWaveScheduled` stamp `CapacityAt` only when they create the row; a row that a capacity event created earlier keeps its remaining packages and its `CapacityAt` (the A25 behaviour is unchanged).
- `NodeScorecardUpdated` is applied only when `computedAt` is not older than the stored scorecard's.
- A zero instant on either side (an older producer, a row written before `CapacityAt` existed) carries no ordering: the fact applies, as before.

## Consequences

- A late older figure is dropped silently; there is no new metric or error, and the inbox row is still written, so the event is not redelivered.
- Rows written before this change accept the first capacity event unconditionally and are stamped from then on.
- Not covered here: the `time` ordering of `NodeActivated`/`NodeSuspended` and of listing availability, which the documentation also words as "newer time wins". They are a separate change.
