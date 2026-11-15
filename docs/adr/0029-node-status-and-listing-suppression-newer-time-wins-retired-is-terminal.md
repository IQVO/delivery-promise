# ADR 0029: node status and listing suppression follow "newer time wins", and a retired node is terminal

## Status

Accepted (2026-11-15). Completes [ADR 0028](./0028-out-of-order-capacity-and-scorecard-facts-never-overwrite-a-newer-one.md) (whose last consequence left these two projections for "a separate change") and follows the precedent of [ADR 0022](./0022-fast-tier-candidates-are-filtered-on-node-eligibility.md). Amends the consumed-contract reading of [ADR 0003](./0003-consumed-contracts-and-local-read-models.md) for `NodeProfileView` and `AvailabilityView`.

## Context

The context documentation (warehouse-docs, delivery-promise domain-events) words the projections of `NodeActivated`, `NodeSuspended`, `ListingSuppressed` and `ListingRestored` as "newer `time` wins" and `NodeRetired` as "`RETIRED` (terminal)". The code did neither: `OnNodeStateChanged` and `OnListingToggled` wrote whatever arrived last.

- A `NodeSuspended` replayed or delivered late after a newer `NodeActivated` took a healthy node out of the candidate set; an older `NodeActivated` delivered after a newer `NodeSuspended` put a suspended node back, so the search quoted promises from a node that had been taken out of service.
- Any `NodeActivated` or `NodeSuspended` delivered after `NodeRetired` revived a retired node. node-topology ADR 0002 makes `RETIRED` terminal at the producer (`ErrNodeRetired` on every command), so such a message can only be an out-of-order delivery, never a real revival.
- The same last-delivered-wins hole existed for a suppressed listing that a late older `ListingRestored` brought back to the candidate set (and the reverse).

The three sources agree (ADR 0022 and 0028 for the rule, warehouse-docs for the wording, node-topology ADR 0002 for the terminal state) and only the code diverged, so the code is fixed, not the text. Covered by `features/stale_facts_rules.feature` and `internal/application/usecases/projections_state_order_test.go`.

## Decision

- `readmodel.Node` carries `StateAt`, the CloudEvents `time` of the newest `NodeActivated` / `NodeSuspended` applied to `State`; `readmodel.Listing` carries `SuppressedAt`, the CloudEvents `time` of the newest `ListingSuppressed` / `ListingRestored` applied to `Suppressed`. Both live in the row's `data` jsonb: no migration.
- `OnNodeStateChanged` (`NodeActivated`, `NodeSuspended`): dropped when `time` is older than `StateAt`; an equal instant is the later delivery of the same fact and applies (the ADR 0022 rule). `OnListingToggled` follows the same rule against `SuppressedAt`. A zero instant on either side (an older producer, a row written before the stamp existed) carries no ordering: the fact applies, as before. The same `staleFact` helper serves ADR 0028's capacity facts.
- **`RETIRED` is terminal.** A node whose row says `RETIRED` takes no later status fact, whatever its time or type. `NodeRetired` itself applies whatever its `time` (it cannot be older than a real activation; if it arrives late it still retires the node, and a later `NodeActivated` stays dropped). A `NodeRetired` that arrives before `NodeRegistered` keeps the row retired: registration never touches the status.
- `ListingAvailabilityChanged` keeps its own `asOf` guard (ADR 0003) and, rewriting the row, keeps `Suppressed` and `SuppressedAt`.

## Consequences

- A late older fact is dropped silently; there is no new metric or error, and the inbox row is still written, so the event is not redelivered (same as ADR 0028).
- Rows written before this change have no stamp and accept the first status or suppression fact unconditionally; they are stamped from then on. A node already revived from `RETIRED` by the old code stays active until the producer repeats a status (nothing here rewrites history); a repeated `NodeRetired` retires it.
- A retirement cannot be undone from this side. If node-topology ever allows a node to be re-registered after retirement, that is a new node id or a new ADR; this projection will not follow it.
- No contract, REST, MCP or event-envelope change; the CloudEvents `time` was already consumed (`Meta.Time`).
- Docs: `docs/docs/ddd/domain-events.md`, `eventstorming.md` (H2) and `development/testing.md` state the new rule.
