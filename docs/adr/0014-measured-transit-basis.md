# ADR 0014: measured-transit promising (basis MEASURED)

## Status

Accepted (2026-11-05).

## Context

The promise search computes every leg from SCHEDULED facts: a departure's
`arrivesAt` is its scheduled arrival, and only a STANDARD quote may fall back
to nominal lane times (`basis = NOMINAL`). Nothing the search reads says how
long the network ACTUALLY takes on a lane: a load that leaves on time and
arrives two hours late is invisible until a promise is judged `BROKEN`, and
the only learning signal (`node-performance` grades) measures nodes, not legs.
Public fulfillment-network practice is distributional promising: quote a
high-probability arrival computed from measured transit, not the timetable,
with the lateness cost weighted heavier than the impatience cost.

The service already consumes `retailnetwork.linehaul-transportation.events`,
whose `LoadDeparted` (`loadId`, `originNodeId`, `departedAt`) and `LoadArrived`
(`loadId`, `destinationNodeId`, `arrivedAt`) pair by `loadId` into one
observed actual transit per (origin, destination) pair — no producer change
is needed.

## Decision

- New local read model **TransitActuals**, fed only by the two load events:
  - `rm_load_transit_open(load_id PK, origin_node_id, departed_at)` written on
    `LoadDeparted`;
  - on `LoadArrived`, the open row pairs into one observation
    `arrivedAt − departedAt`, appended (ring, last 200) to
    `rm_transit_actuals(origin_node_id, dest_node_id, data jsonb)`; the open
    row is deleted. Open rows older than the window are pruned on write.
  - The window (default 28 days), the percentile (default P90) and the
    minimum observations (default 20) are `PromisePolicy` fields:
    `transitWindowDays`, `transitServiceLevelPercent`, `minTransitObservations`.
- The search consults the model per leg through the Input closure
  `MeasuredTransit(origin, dest) (ok, minutes)` (pure domain, no repository).
  When a pair has at least `minTransitObservations` observations and the
  P-service-level transit EXCEEDS the departure's scheduled transit
  (`arrivesAt − departsAt`), the leg's arrival is
  `arrivesAt + (measured − scheduled)` — a one-sided adjustment: measured
  transit never promises EARLIER than the timetable.
- A candidate whose witness path used at least one measured adjustment is
  quoted with **`basis = MEASURED`**; otherwise the basis is unchanged
  (`SCHEDULED`, or `NOMINAL` on the STANDARD fallback). `MEASURED` requires
  the full witness (same rule as `SCHEDULED`); `ErrNominalBasisNotAllowedForTier`
  still applies only to `NOMINAL`, so a fast tier may be `MEASURED`.
- The offer's `DeliveryOfferQuoted` analytics event and the promise terms
  carry the basis unchanged — the vocabulary gains a value, no field changes.

## Consequences

- Lanes that run late stretch the promise before customers are lied to; lanes
  that run on time keep the timetable promise (no early speculative
  shortening — that needs arrival-side evidence this model does not hold).
- The adjustment is per (origin, destination), not per departure schedule or
  carrier: with 20+ observations the percentile is robust to single
  outages; below it the timetable stands.
- One new consumer pairing state (`rm_load_transit_open`) is best-effort: a
  missed departure (service down) simply loses one observation; nothing
  breaks.
- Judging (`KEPT/BROKEN{lateBy}`) is untouched — it now closes the loop the
  adjustment opens: BROKEN rates by lane are the calibration signal for
  tuning the service level.
- Roadmap: this is feature F2. The per-node handling-time distribution and
  the wave-side (induction-to-window) distribution are later slices on the
  same read model.
