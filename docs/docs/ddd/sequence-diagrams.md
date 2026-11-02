---
id: sequence-diagrams
title: Sequence diagrams
sidebar_label: Sequence diagrams
description: "Adapter, use case, aggregate and outbox interactions of every Delivery Promise flow: quote, promise, confirm, cancel, re-promise, judge, relay and sweeper."
---

# Sequence diagrams

Each diagram follows one trigger through the hexagon: inbound adapter, `usecases.Service`, aggregates and repositories, outbox. "UoW" is the Postgres unit of work (`ports.UnitOfWork`): everything inside it commits or rolls back together, including the outbox rows.

## Quote

```mermaid
sequenceDiagram
  autonumber
  participant C as Shopper or checkout
  participant H as HTTP adapter
  participant S as Service
  participant R as Read models and policy
  participant D as Search (domain)
  participant DB as Postgres
  C->>H: POST /delivery-offers:quote
  H->>S: QuoteDeliveryOffers(command)
  S->>S: validate prefix, lines, tiers
  S->>R: current policy, nodes, listings, departures, waves, scorecards
  S->>D: Run(search input)
  D-->>S: groups with candidates per tier, constraints
  S->>DB: UoW begin
  S->>DB: save QUOTED offers
  S->>DB: append DeliveryOfferQuoted per offer to outbox_events
  S->>DB: UoW commit
  S-->>H: offers and unavailable
  H-->>C: 200 offers, unavailable
```

Source: `internal/application/usecases/quote.go`, `internal/domain/search`, `internal/adapters/inbound/http/server.go`.

## Make a promise

```mermaid
sequenceDiagram
  autonumber
  participant CO as customer-ordering
  participant H as HTTP adapter and idempotency
  participant S as Service
  participant DB as Postgres
  participant B as PromiseBook
  participant P as Promise
  CO->>H: POST /promises with Idempotency-Key
  H->>DB: claim (route, key) inside the request transaction
  H->>S: MakePromise(offerId, lines)
  S->>DB: load offer
  S->>S: offer.Consume, same lines, not expired
  S->>DB: revalidate node, departures, wave in read models
  S->>DB: load or lazily open the witness book
  S->>B: Hold(now)
  B-->>S: PromiseBookExhausted when share is reached
  S->>P: Make(terms, witness, holdTtl)
  P-->>S: PromiseMade
  S->>DB: save offer, book, promise
  S->>DB: append events to outbox_events
  H->>DB: store the outcome with the key and commit
  H-->>CO: 201 PROVISIONAL promise
```

Source: `internal/application/usecases/make_promise.go`, `internal/adapters/inbound/http/idempotency.go`. Offer, promise and book change in one transaction: the documented exception to one aggregate per transaction (gap G22).

## Confirm

```mermaid
sequenceDiagram
  autonumber
  participant CO as customer-ordering
  participant H as HTTP adapter
  participant K as Kafka consumer
  participant S as Service
  participant DB as Postgres
  CO->>H: POST /promises/id:confirm with orderId and groupNo
  H->>S: ConfirmPromise
  S->>DB: UoW begin, load promise and book
  S->>S: promise.Confirm before holdExpiresAt
  S->>S: book.ConfirmHold, provisional to confirmed
  S->>DB: save promise and book, append PromiseConfirmed
  S->>DB: UoW commit
  H-->>CO: 200 CONFIRMED promise
  K->>S: later CustomerOrderPlaced from customer-ordering
  S->>DB: claim event id in processed_events
  S->>S: confirm is a no-op when already confirmed
```

Source: `internal/application/usecases/lifecycle.go`, `internal/application/usecases/projections.go` (`OnCustomerOrderPlaced`).

## Cancel

```mermaid
sequenceDiagram
  autonumber
  participant CO as customer-ordering
  participant H as HTTP adapter
  participant K as Kafka consumer
  participant S as Service
  participant DB as Postgres
  alt compensation in checkout
    CO->>H: POST /promises/id:cancel
    H->>S: CancelPromise(id)
  else order cancelled
    K->>S: OnCustomerOrderCancelled
    S->>DB: claim event id
  end
  S->>DB: load promise and book
  S->>S: promise.Cancel, CONFIRMED or AT_RISK to CANCELLED
  S->>S: book.ReleaseConfirmed
  S->>DB: save, append PromiseCancelled, commit
  H-->>CO: 200 CANCELLED promise
```

Source: `internal/application/usecases/lifecycle.go` (`CancelPromise`, `CancelPromises`, `cancel`).

## Re-promise on risk

```mermaid
sequenceDiagram
  autonumber
  participant X as Producer (linehaul, last-mile, sourcing, tracking)
  participant K as Kafka consumer
  participant S as Service
  participant DB as Postgres
  participant SW as Sweeper
  X->>K: DepartureCancelled or WaveCancelled or FulfillmentUnsourceable or BookingFailed
  K->>S: handler for the full event type
  S->>DB: UoW begin, claim event id
  S->>DB: update read model, find affected promises
  S->>S: promise.FlagAtRisk, CONFIRMED to AT_RISK
  S->>DB: save, append PromiseAtRisk, commit
  S->>S: ReviseAtRisk searches again from now, tier then slower tiers
  alt a candidate is not earlier and its book accepts the hold
    S->>DB: hold and confirm new book, release old book
    S->>DB: promise.Revise, append PromiseRevised, commit
  else no candidate at any tier
    S->>DB: MarkUnkeepable, append PromiseUnkeepable once
  end
  SW->>S: RepriceAtRisk every sweep interval
  S->>S: retry ReviseAtRisk for promises still AT_RISK
```

Source: `internal/application/usecases/risk.go`, `internal/application/usecases/projections.go`, `cmd/api/workers.go`.

## Judge on delivery

```mermaid
sequenceDiagram
  autonumber
  participant OS as order-sourcing
  participant ST as shipping-and-tracking
  participant K as Kafka consumer
  participant S as Service
  participant DB as Postgres
  OS->>K: FulfillmentLocked then FulfillmentShipped
  K->>S: record assignment, promise, node, packages
  ST->>K: PackageDelivered or PackageLost or PackageReturned
  K->>S: record package outcome
  S->>S: judge, wait until the facts are complete
  alt first lost or returned package
    S->>DB: promise.Judge BROKEN with cause LOST or RETURNED
  else every package delivered
    S->>DB: promise.Judge on the latest delivery, KEPT or BROKEN with cause LATE
  end
  S->>DB: save, append PromiseKept or PromiseBroken, commit
```

Source: `internal/application/usecases/judge.go`, `internal/application/usecases/projections.go`.

## Outbox relay

```mermaid
sequenceDiagram
  autonumber
  participant R as Outbox relay
  participant DB as Postgres outbox_events
  participant KW as Kafka writer
  participant KB as Kafka broker
  loop every OUTBOX_RELAY_INTERVAL
    R->>DB: claim up to 100 unpublished rows, FOR UPDATE SKIP LOCKED, id order
    R->>KW: send one message (CloudEvents, traceparent header)
    KW->>KB: write to the events or analytics topic
    alt acknowledged
      R->>DB: set published_at
    else failure
      R->>DB: attempts + 1, last_error
      Note over R,DB: at the attempt cap the row is parked and an ERROR is logged
    end
  end
```

Source: `internal/adapters/outbound/outbox/relay.go`, `internal/adapters/outbound/kafka`, `cmd/api/workers.go`.

## Sweeper

```mermaid
sequenceDiagram
  autonumber
  participant SW as Sweeper (every SWEEP_INTERVAL)
  participant S as Service
  participant DB as Postgres
  SW->>S: ExpireOffers(100)
  S->>DB: QUOTED offers past expiresAt to EXPIRED
  SW->>S: ReleaseExpiredHolds(100)
  S->>DB: PROVISIONAL past holdExpiresAt to RELEASED
  S->>DB: book.ReleaseProvisional, append PromiseReleased
  SW->>S: CloseBooks(100)
  S->>DB: OPEN books at cutoff to CLOSED
  SW->>S: RepriceAtRisk(100)
  S->>DB: retry re-promise of AT_RISK promises
```

Source: `cmd/api/workers.go` (`sweepOnce`), `internal/application/usecases/lifecycle.go`, `internal/application/usecases/risk.go`.

## Read through MCP or A2A

```mermaid
sequenceDiagram
  autonumber
  participant A as Agent
  participant M as cmd/mcp or cmd/a2a
  participant S as Service (reads only)
  participant DB as Postgres
  A->>M: tools/call get_promise or SendMessage promise_status
  M->>S: GetPromise or ListPromisesByOrder
  S->>DB: select
  DB-->>S: rows
  S-->>M: views
  M-->>A: structured result or a problem inside the message
```

Source: `internal/adapters/inbound/mcp`, `internal/adapters/inbound/a2a/skills.go`, `cmd/mcp/main.go`, `cmd/a2a/main.go`. Neither binary can write: their `Reads` interface has no write method.
