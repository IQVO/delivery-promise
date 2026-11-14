# ADR 0026: a carrier-handoff service area is promised without a delivery wave

## Status

Accepted (2026-11-13). Amends the search and witness rules of [ADR 0002](./0002-aggregates-and-invariants.md) (the final leg need not be a wave), the consumed contracts of [ADR 0003](./0003-consumed-contracts-and-local-read-models.md) (one new consumed type, extended `ServiceAreaAssigned`) and the policy snapshot of ADR 0002/0020 (one new field). Slice C4-DP of the carrier service-area design; node-topology's ADR 0016 declares the area mode this builds on.

## Context

node-topology is about to let the planner declare a delivery MODE per service area: `NETWORK_ROUTE` (today's behaviour, zero value) or `CARRIER_HANDOFF` (an external carrier collects at a handoff node and delivers the area). A `CARRIER_HANDOFF` area carries 1..4 declared carrier options `{carrierId, handoffNodeId, costPerPackageCents, transitMinutes}`.

Today every promise candidate ends on a delivery wave: `candidateFrom` snaps the arrival to `firstWave` and the witness REQUIRES a `DeliveryWaveID`. A carrier area has no wave - the carrier delivers the area on its own transit - so the promise for such an area needs a final leg that is a carrier, not a wave, and a witness that proves it. The per-package carrier is not a free operator choice: a promise made against one carrier's transit must be the carrier the handoff will actually use, so the choice is a DETERMINISTIC rule over the area's declared options, computed here at quote time and recomputed by shipping-and-tracking at handoff from the same area version.

Measured carrier transit reuses the ADR 0014 machinery unchanged: the ring is keyed by two id strings, and a carrier observation pairs `PackageHandedToCarrier` with `PackageDelivered` under the key `(handoffNodeId, "area:"+prefix)` - the same ring, the same percentile policy knobs, the same one-sided rule.

## Decision

The contract, verbatim:

- Area read model: `readmodel.Area` gains `DeliveryMode` and `Carriers`; `OnServiceAreaAssigned` decodes the new fields (absent = NETWORK_ROUTE); migration `0010_area_delivery_mode` adds `delivery_mode TEXT NOT NULL DEFAULT 'NETWORK_ROUTE'` and `carrier_options JSONB NOT NULL DEFAULT '[]'` to the areas table of `RegionAndServiceAreaView`; memory twin + repocontract + both cmd wiring (playbook: a read-model change touches ports, memory, postgres, repocontract, wiring). The rm_areas table keeps its jsonb `data` row as the source the adapter reads back; the two new columns are written beside it so the mode is queryable in SQL.
- `searchInput`: when the area's mode is CARRIER_HANDOFF, set `in.Carrier = &search.CarrierArea{Options: ...}` (new `Input` field) and `in.Station` remains the area station for region/context, but the final-leg target becomes the CHOSEN option's handoff node (the choice rule below). `MeasuredCarrierTransit` closure: like `measuredTransitOf` but asking the ring with dest `"area:"+prefix` (nil when the port is absent).
- Choice rule (ONE pure function, `internal/domain/search/carrier.go`, mirrored verbatim in shipping-and-tracking - the rule is contract-pinned in both repos' ADRs): sort options by (costPerPackageCents asc, transitMinutes desc, carrierId asc); the first is the chosen option. Both repos compute it from the same area assignment version.
- Candidate: `candidateFrom` keeps everything up to `earliestArrival` (whose target is the handoff node for carrier areas - `searchArrival` already parameterises the target as `s.in.Station.ID`, so the carrier path passes the handoff node and keeps the same Dijkstra); then, instead of `wave(...)`: `effective = max(transitMinutes, measuredCarrierMinutes)` (one-sided, ADR 0014 rule); `earliest = arrivalAtHandoff.Add(effective)`; `latest = earliest.Add(pol.CarrierWindowHours())`; require the window to fit the tier's `MaxCalendarDays` (else `DeliverySLAUnavailable`). `Witness{CarrierID, HandoffNodeID}` set, `DeliveryWaveID` empty.
- `Witness` gains `CarrierID`, `HandoffNodeID` (four places: struct, `Validate`, `Clone`, the offer/promise snapshot plumbing; `repocontract`/goldens). `Validate`: a SCHEDULED/MEASURED witness needs a pickup departure AND (`DeliveryWaveID` OR (`CarrierID` AND `HandoffNodeID`)); never both a wave and a carrier. Policy: `CarrierWindowHours *int` on the policy snapshot (playbook: pointer, 0 not legal; default 24 when nil; range 1..96) - flows into `policy.go` `Snapshot`/`Input`, the policy REST view, and this ADR.
- Carrier transit actuals: extend the `TransitActuals` port with `RecordCarrierHandoff(ctx, trackingNumber, handoffNodeID, prefix, at)` and `RecordCarrierDelivered(ctx, trackingNumber, at)` (open row keyed by tracking number - the analogue of the load row; paired into an observation of `(handoffNodeID, "area:"+prefix)`); memory twin, Postgres, repocontract, wiring in `cmd/api` and `cmd/mcp` (Deps optional). New dispatch entries on the ST topic: `...journey.PackageHandedToCarrier` -> the handoff recorder, and the EXISTING `PackageDelivered` handler additionally calls `RecordCarrierDelivered` (one new use-case method, one extra dispatch line). asyncapi receive operation + ADR 0003 consumed row.
- Views: offer/promise/estimate responses and MCP/A2A DTOs gain optional `carrierId`/`handoffNodeId`/`carrierArea` fields (additive, `omitempty`); openapi + regenerated REST docs in the same commits.
- At-risk sweeper: a carrier witness has no wave and no departure to watch on its final leg; the PREDICTED_LATE sweep skips carrier witnesses (state the gap: carrier lateness is caught by shipping-and-tracking's silence exceptions and the measured ring, not by the sweeper). Departure cancellations on the legs up to the handoff node still flag and re-promise a carrier promise through the existing path.

`CarrierWindowHours` default: 24 hours (promise Latest = carrier Earliest + width). Range 1..96.

## Consequences

- Every area without a mode keeps promising exactly as before: absent fields decode as NETWORK_ROUTE, and an old producer replay changes nothing (failure mode: an area assigned CARRIER_HANDOFF while this service has no carrier options decoded keeps using waves).
- A carrier promise is judged by `PackageDelivered` timing as any other: a slower carrier after an area reassignment breaks the promise like any late delivery (documented, not prevented).
- Carrier lateness is NOT caught by the at-risk sweeper (no departure leg on the final mile of a carrier promise); the ST silence policy on `WITH_CARRIER` and the measured ring are the guards.
- The policy field is additive on the jsonb snapshot: a policy persisted before this ADR restores with the default 24h; no policy column exists or is added.
- Deploy: consumers before producers - this service deploys BEFORE node-topology's carrier-area declaration lands; activation is the first `CARRIER_HANDOFF` assignment (and, for the measured ring, ST's `PackageHandedToCarrier` existing). No new topic, no new env var, no chart value on this side: the two new consumed types ride the existing topics and the existing consumer group.
