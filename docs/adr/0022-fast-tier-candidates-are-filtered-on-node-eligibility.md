# ADR 0022: fast-tier candidates are filtered on node eligibility

## Status

Accepted (2026-11-09).

## Context

The promise search ranks every fulfillment node that covers the lines and has a feasible witness path. The only node-quality input is the 30-day grade of node-performance, applied as a handicap (ADR 0003, E18). A node whose recent on-time handover collapses therefore keeps winning SAME_DAY and NEXT_DAY sourcing until its 30-day grade drops, and every promise quoted on it in the meantime is a promise likely to be broken. Public marketplaces revoke fast-delivery badges on short measurement windows for this reason.

node-performance (its ADR 0013, "fast-tier eligibility from the 7-day window") now judges eligibility itself from the 7-day scorecard, with hysteresis, and publishes the decision as an integration event. This service does not recompute the rule and does not read node-performance code or data: it consumes the WRITTEN event and keeps a local read model, as the estate requires (sibling data used in a decision is a local, event-fed read model; the promise hot path makes no synchronous call).

The event contract this ADR builds against (the producer's side, restated here as the tolerant local copy of `apis/asyncapi.yaml`):

- type `com.retailnetwork.partner.node-performance.scorecard.NodeTierEligibilityChanged`, topic `retailnetwork.node-performance.events` (integration, not analytics), source `/retailnetwork/node-performance`, dataschema `urn:retailnetwork:node-performance:events:NodeTierEligibilityChanged:v1`, subject and Kafka key = the scorecardId (`<nodeId>:7d`);
- data: `nodeId` (string), `tier` (`SAME_DAY` or `NEXT_DAY`), `eligible` (bool), `metric` (`onTimeHandoverRate`), `value` (number, the rate that caused the change), `threshold` (0.90 on suspension, 0.95 on reinstatement), `window` (`7d`), `changedAt` (RFC 3339 UTC);
- raised only when the node's flag FLIPS, as two events per flip (one per tier) with the same `changedAt`; there is no event for the initial ELIGIBLE state, so a node never mentioned is eligible.

## Decision

- New local read model **NodeTierEligibility**: table `rm_node_tier_eligibility(node_id, tier, eligible, changed_at)`, primary key `(node_id, tier)` (migration 0007). **A missing row means ELIGIBLE.**
- The consumer dispatches on the FULL type `com.retailnetwork.partner.node-performance.scorecard.NodeTierEligibilityChanged` (the topic is already consumed; no new subscription). Every message runs in the unit of work that records the CloudEvents `id` in the inbox (`processed_events`), so a replay changes nothing.
- **Out-of-order guard:** the event is applied only when its `changedAt` is greater than or equal to the stored `changed_at` of the `(node_id, tier)` row; an older one is dropped. Equal timestamps apply (the later delivery of the same instant wins, and the two events of a flip are on different tiers).
- **Tolerant decoding:** unknown fields are ignored. An event with an unknown `tier` value (anything but `SAME_DAY` or `NEXT_DAY`), or with a missing `nodeId`, `tier`, `eligible` or `changedAt`, is ignored and counted (metric `deliverypromise.tier.eligibility.events.ignored{reason}`, `reason` = `unknown-tier` or `missing-field`), never dead-lettered: a producer that later governs another tier must not fill the DLQ of this service.
- **Search:** the use case loads the ineligible `(node, tier)` pairs once per search and hands the pure domain an Input closure `TierIneligible(nodeID, tier) bool`, in the same style as `MeasuredTransit` (ADR 0014): no repository call in the domain, and a nil closure never filters (the unfiltered default). A candidate fulfillment node that is ineligible for the tier being searched is removed from that tier's candidates before they are ranked.
  - Only `SAME_DAY` and `NEXT_DAY` are filtered; `TWO_DAY` and `STANDARD` never are. The same node stays a candidate for STANDARD, including the nominal fallback.
  - If no candidate remains, the tier simply yields no offer, exactly as for any infeasible tier (`DELIVERY_SLA_UNAVAILABLE`); there is no new constraint code and no new error.
  - **Existing promises are never touched.** The filter applies to future quotes and estimates only. `ReviseAtRisk` (re-promising a promise already made, possibly locked to a node) does not use it, and no event moves or flags a promise.
- Metric **`deliverypromise_tier_candidates_excluded_total{tier}`** (OpenTelemetry counter `deliverypromise.tier.candidates.excluded`, exported with the `_total` suffix, label `tier` = `SAME_DAY` or `NEXT_DAY`): counts the ineligible candidate nodes removed, once per node and tier per search request (an estimate of N items counts a node once, not N times).
- `apis/asyncapi.yaml` gains the consumed message `NodeTierEligibilityChanged` under the `nodePerformanceEvents` channel and its receive operation; this ADR and ADR 0003/0004 carry the full type string.

## Consequences

- A node that is failing its recent on-time handover stops being offered SAME_DAY and NEXT_DAY within one event of node-performance's decision, long before its 30-day grade moves; it is reinstated by the producer's own hysteresis (three consecutive distinct UTC days at or above 0.95), which this service merely follows. This service owns no threshold and publishes nothing.
- A cold or never-fed read model filters nothing: every node is eligible. The fast tiers are then quoted exactly as before (no regression on a fresh deploy or a replay from an empty database).
- A fast tier can lose its only candidate and yield no offer (the shopper sees the slower tiers). That is the intended cost: it is cheaper than a broken promise.
- The read model can briefly lag the producer; the quote it feeds is a future promise, and the promise still has to survive every other search rule. A stale ineligible row (a missed reinstatement) is corrected by the next event of that `(node, tier)`.
- Deploy: none. The topic is already consumed, the consumer group is unchanged and there are no new environment variables or chart values. Migration 0007 runs with the existing migration step.
- Observability: ADR 0009's one Tier 2 business counter gains two siblings, both on the existing meter (no new exporter or setting): the exclusion counter above and the ignored-event counter. The documented reading is on the observability page.
- Judging, the PromiseBook and the outbox are untouched; the REST and MCP surfaces are unchanged.
