# Domain model: ubiquitous language, aggregates, events, use cases

Source of truth: retail-network-docs `model/model.yaml` (context `delivery-promise`),
amended by the Model Gap Register and contracts-delta. Invariants, transitions and
every assumption the model left open are in `docs/adr/0002-aggregates-and-invariants.md`
(assumptions A1-A33).

## Ubiquitous Language (use these exact names)

- **DeliveryOffer** — a quote for ONE shipment group (`lines[]`, one line on a
  product page) and one tier, backed by a witness path. Stores `basis` and
  `policyVersion`. Expires; consumed by exactly one promise.
- **Promise** — the date range a package will arrive (`shipBy`, `deliverByEarliest`,
  `deliverByLatest`), held by capacity in a PromiseBook until the order is placed
  or the hold expires. Judged KEPT or BROKEN.
- **PromiseBook** — the capacity of one (node, pickup departure) pair, `bookId =
  nodeId:pickupDepartureId`; opens lazily on the first hold, closes at the cutoff.
- **Witness path** — the feasible node cutoff -> scheduled departures -> delivery
  wave that makes a date true. `SCHEDULED` basis names departure and wave ids;
  `NOMINAL` (STANDARD tier only) carries none.
- **Tier** — `SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`.
- **Constraint** — why a tier is not offered: `DESTINATION_NOT_SERVED`,
  `ITEM_QUANTITY_NOT_AVAILABLE`, `PAST_ORDER_DEADLINE`, `DELIVERY_SLA_UNAVAILABLE`,
  `NODE_CAPACITY_EXHAUSTED`.
- **PromisePolicy** — the versioned offer/hold TTLs, packages per order, capacity
  share and per-tier order deadlines (`policyVersion` `v0` is the built-in default).
- **Hold** — provisional capacity (`holdExpiresAt`); **confirm** turns it into
  confirmed capacity when the order is placed.

## Aggregates (`internal/domain/*`)

- **DeliveryOffer** (`offer`): `QUOTED` -> `CONSUMED` | `EXPIRED`; `ErrOfferNotQuotable`
  past expiry or once consumed; a fast tier is never NOMINAL.
- **Promise** (`promise`): `PROVISIONAL` -> `CONFIRMED` | `RELEASED`; `CONFIRMED` ->
  `AT_RISK` | revised | `KEPT` | `BROKEN` | `CANCELLED`; terminal states refuse
  everything (`ErrPromiseClosed`); a revision never moves the window earlier
  (`ErrRevisionMovesEarlier`); confirm only before the hold expires
  (`ErrPromiseHoldExpired`).
- **PromiseBook** (`book`): `provisional + confirmed <= share`
  (`ErrPromiseCapacityExhausted`); no hold once CLOSED (`ErrBookClosed`).
- **PromisePolicy** (`policy`): immutable versions, `ErrVersionNotNext`.
- `search`: pure functions over a network snapshot (feasibility search, shipment
  grouping, scorecard handicap). `calendar`: business-time arithmetic. `shared`:
  `Event`, `Header`, `Tier`, `Basis`, `Line`, `Window`, `Witness`, `ErrValidation`,
  `ErrIllegalTransition`, `ErrInvalidSnapshot`.

Every aggregate has `Snapshot()`, a validating `Restore(Snapshot)` and an optimistic
`Version()`; `now` and ids are passed in. `ErrNotFound` and `ErrConcurrentUpdate`
are port errors (`internal/application/ports/errors.go`). The domain imports nothing
outside `internal/domain`. `MakePromise` changes offer, promise and book in ONE
transaction (documented exception, G22).

## Domain events (`internal/domain/*/events.go`; wire shape in `apis/asyncapi.yaml`)

- Integration (`retailnetwork.delivery-promise.events`): `PromiseAtRisk`,
  `PromiseRevised`, `PromiseKept`, `PromiseBroken`, `PromiseUnkeepable`,
  `PromiseBookExhausted`.
- Analytics (`retailnetwork.delivery-promise.analytics`): `PromiseMade`,
  `PromiseConfirmed`, `PromiseReleased`, `PromiseCancelled`, `DeliveryOfferQuoted`,
  `PromisePolicyPublished`.

## Key use cases (`internal/application/usecases`)

One method on `Service` per command and query; commands run in ONE `ports.UnitOfWork`
(load, domain call, save, outbox append) and retry on `ErrConcurrentUpdate`
(`service.go`, `MaxAttempts`).

- `QuoteDeliveryOffers` (`quote.go`), `MakePromise` (`make_promise.go`),
  `ConfirmPromise`, `CancelPromises`, `ReleaseExpiredHolds`, `ExpireOffers`,
  `CloseBooks`, `PublishPromisePolicy` and the queries (`lifecycle.go`).
- `RepriceAtRisk`, `ReviseAtRisk`, `ReviseAll` (`risk.go`): re-promise when the
  witness path breaks; `PromiseUnkeepable` once when no path is left.
- Judgement from shipping facts (`judge.go`): KEPT on the LAST package delivered,
  BROKEN `LOST` / `RETURNED` / `LATE`.
- Projections of consumed events (`projections.go`, `network.go`, `events.go`): the
  `On...` methods the Kafka consumer calls, each behind the inbox.
- Ports (`internal/application/ports/ports.go`) are interfaces only; the memory test
  doubles in `internal/adapters/outbound/memory` are test doubles and must pass the
  same suite as Postgres (`internal/adapters/outbound/repocontract`).
