# ADR 0020: promise calibration from the service's own judgement

## Status

Accepted (2026-11-09). Builds on [ADR 0014](./0014-measured-transit-basis.md): that ADR says "BROKEN rates by lane are the calibration signal for tuning the service level"; this one closes that loop. Feature E1 of the promise-quality wave.

## Context

The search promises the earliest feasible witness path. ADR 0014 stretches a leg that runs later than its timetable, but nothing tells the search how often the promises it quotes are actually kept. The service already judges every confirmed promise `KEPT` or `BROKEN{lateByMinutes}`, so it holds, locally and without any sibling, the one signal that measures its own promising: how late, if at all, a promise of a given kind turned out to be.

Public fulfillment-network practice quotes a high-probability arrival rather than the earliest feasible one: it observes the lateness distribution of the promises it made and adds a buffer to the next quote so that a target share of them lands on time, and it tells the shopper how sure the quote is. This ADR does that from the service's own judgements. It adds no data source and no synchronous call: the promise hot path still reads only local tables.

## Decision

The contract, verbatim:

- Read model `PromiseCalibrationView`, fed ONLY by this service's own promise judgements (`PromiseKept`, `PromiseBroken{lateBy}`), local, no sibling call. Migration 0006 (the table AND the policy columns, in that one migration).
  - Segment key = (`tier`, `basis`, `nodeGrade`) where `nodeGrade` is the grade of the promise's locked node as the service already stores it (A, B, C, D or PROVISIONAL). Add the destination region ONLY if it is already persisted on the promise terms; do not add a new data source for it.
  - Per segment keep a ring of the last 500 observations within `calibrationWindowDays`; each observation is `lateByMinutes` (0 for KEPT). Written inside the same transaction that records the judgement; rebuildable from nothing (no backfill).
- `PromisePolicy` gains (versioned like its other fields; Snapshot/New/Restore/builder/repocontract/openapi/docs):
  - `targetOnTimeProbabilityPercent` per policy (default 95, range 50..99; "per tier" in the plan is satisfied by the policy being per tier if tiers already carry their own policy values; if the policy is one global value, make it one global value and say so in the ADR),
  - `minCalibrationObservations` (default 50, range 10..10000),
  - `calibrationWindowDays` (default 28, 1..365),
  - `maxCalibrationBufferMinutes` (default 720, 0..2880).
- Buffer rule (pure domain function `CalibrationBuffer(obs []int, targetPercent) minutes`): with n >= `minCalibrationObservations`, the buffer is the smallest `b` such that at least `targetPercent`% of the observations have `lateBy <= b` (the target quantile of lateBy, kept promises count as 0), capped at `maxCalibrationBufferMinutes`. If the observed kept share already meets the target the buffer is 0. **One-sided**: the buffer never makes a promise earlier. Below `minCalibrationObservations` the buffer is 0 and the search behaves exactly as F2 does today.
- Search: the segment's buffer is added to each candidate's final-arrival time BEFORE it is snapped to a delivery window (so the quoted window width never changes; the offer lands in a later fixed window when needed). Wire it through an Input closure like `MeasuredTransit`. Apply it to every tier, including STANDARD. A candidate that no longer fits the horizon is dropped like any infeasible one.
- `confidence` (additive, optional, number in [0,1] rounded to 3 decimals) on `DeliveryOffer`, each `POST /delivery-estimates` option, the promise terms/`PromiseMade` event, the MCP outputs and the analytics `DeliveryOfferQuoted`: the segment's observed share of observations with `lateBy <= buffer`, present only when n >= `minCalibrationObservations`; omitted otherwise (unknown, not zero). Consumers must read absence as unknown.
- The plan's "widen F2's leg stretching to sort dwell and the last-mile wave" is OUT OF SCOPE for this slice; it is a deferred follow-up (see Consequences).
- Observability: gauge/histogram of the applied buffer per tier; counter of segments below minimum. The F9 promise-precision report is unchanged.

### How the contract reads against the code

Where the contract left a choice to the code as it is, this is the reading:

- **One global target, not one per tier.** The scalar fields of `PromisePolicy` (TTLs, packages per order, capacity share, the ADR 0014 transit fields) are single values; only the order deadlines are keyed by tier. The four calibration fields follow the scalars: `targetOnTimeProbabilityPercent` is ONE global value. The tier still enters the buffer, through the segment key, so each tier gets its own observed lateness; only the target is shared.
- **No destination region in the key.** The promise terms persist tier, shipBy, window, basis and policy version, and the promise the destination postal prefix; the region of the destination comes from the service-area read model at search time and is not stored on the promise. Adding it would need a new data source, which the contract forbids. The key stays (`tier`, `basis`, `nodeGrade`).
- **The node grade is read from the scorecard read model** (`Scorecard.Grade`, the same value the search handicap uses) of the promise's locked witness node, at the instant of the judgement. A node with no scorecard has the empty grade; the segment key spells it `PROVISIONAL` (the search already ranks the empty grade like a provisional one), so the key has the five values A, B, C, D, PROVISIONAL. At quote time the key is built from the same read model for the candidate's node.
- **What is observed.** `KEPT` records 0. `BROKEN` with cause `LATE` records its `lateByMinutes`. `LOST` and `RETURNED` are NOT recorded: their `lateByMinutes` is "minutes from the deadline to the instant the loss was declared, 0 if not later", which says nothing about how late a delivery runs, and a buffer cannot make a lost package arrive; recording them as 0 would count a loss as an on-time delivery.
- **The policy columns.** `PromisePolicy` is persisted as one JSON snapshot (`promise_policies.snapshot`), as ADR 0014's three fields already are; there are no policy columns to add. Migration 0006 therefore creates the table only, and the four fields are versioned in the snapshot. A snapshot persisted before this ADR restores with the defaults. `maxCalibrationBufferMinutes` is the one field whose range includes 0, so it is carried as an optional pointer in `Input` and `Snapshot` (absent = 720); the other three take their default when 0, like the transit fields.
- **The pure function.** `CalibrationBuffer(obs []int, targetPercent int) int` is the uncapped target quantile (nearest rank, kept promises as 0, clamped at 0). `Calibrate(obs, policy)` applies `minCalibrationObservations` and `maxCalibrationBufferMinutes` and derives `confidence`. Both live in `internal/domain/search`.

### Shape

- Table `rm_promise_calibration(tier, basis, node_grade, observations jsonb)`, primary key the three-part key; `observations` is the ring, newest last, `{lateBy, observedAt}`, capped at 500. A write appends under a row lock inside the judgement's unit of work and prunes what is older than the longest window (365 days); a read prunes to the current policy's `calibrationWindowDays`. Port `ports.PromiseCalibrationView` (`Record`, `Segments`), a Postgres adapter and a memory twin with one shared contract test.
- The search takes `Input.CalibrationBuffer(tier, basis, grade) Calibration` (`BufferMinutes`, `Confidence`). The use case loads the segments once per request, lazily, and answers from that copy; a store error is logged and means "no calibration" (the F2 behaviour), never a failed quote. The closure is nil when no segment has reached the minimum, and then the search computes its paths once for all tiers, as before. With a closure the paths are computed per tier, because the buffer depends on the tier.
- A `Candidate` carries `BufferMinutes` and `Confidence`. The offer, the promise terms and `PromiseMade` carry `confidence`; a revision (`tryRevise`) takes the confidence of the candidate it moves to.
- Metrics, on the existing `PromiseMetrics` instruments: histogram `promising.calibration.buffer` (minutes, attribute `tier`) per offer built, counter `promising.calibration.segments_below_minimum` (attributes `tier`, `basis`, `node_grade`) per segment a search consulted that had fewer than the minimum.

## Consequences

- A segment that runs late quotes a later window before customers are told a date that the data says will be missed; a segment that keeps its promises quotes exactly what it quoted before. Nothing is ever promised earlier.
- Because the buffer is added before the wave snap, the offer jumps to the next fixed window rather than widening: the quoted window width never changes, and a tier whose next window is past its calendar horizon loses that candidate (`DELIVERY_SLA_UNAVAILABLE` when it was the only one).
- The target is shared by every tier. A tier that needs a different target needs a per-tier policy field, a later and additive change.
- A segment is small: tier x basis x grade, 5 grades and 3 bases, at most 60 rows. The 500-observation ring and a 50-observation minimum mean a thin segment (a rare tier on a rare grade) stays on the F2 behaviour and reports no `confidence`: absence means unknown, never zero.
- The signal is the service's own judgement, so it lags: a regime change shows after enough late judgements, and the buffer then also keeps the segment's quotes later, which lowers new lateness and lets the ring recover. The window (28 days) and the cap (720 minutes) bound both effects.
- `confidence` is a statement about the segment, not about this one promise. It is the observed share of in-window observations with `lateBy <= buffer`, so with a full ring and a buffer at the target quantile it sits at or above the target.
- Deferred follow-up, out of scope here: the plan's widening of F2's leg stretching to the sort dwell and the last-mile wave. This ADR calibrates the whole promise from its outcome; it does not model those parts separately.
- Deploy: no new topic, environment variable or chart value. The migration runs at start-up.
