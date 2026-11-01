---
id: domain-message-flow
title: Domain message flow
sidebar_label: Domain message flow
description: "Four Delivery Promise scenarios as ddd-crew domain message flows: quote to kept promise, a cancelled departure, an unsourceable group, a full or expired hold."
---

# Domain message flow

Following ddd-crew [Domain Message Flow Modelling](https://github.com/ddd-crew/domain-message-flow-modelling). Participants are bounded contexts and actors. Every arrow is a message prefixed `cmd:` (command), `qry:` (query) or `evt:` (event). Events travel over Kafka through each producer's outbox; the diagrams draw them straight from producer to consumer. A self-arrow on `delivery-promise` marks an event published with no consuming context (analytics only). Full event types are on [Domain events](/docs/ddd/domain-events); the internal steps are on [Sequence diagrams](/docs/ddd/sequence-diagrams).

## 1. Quote, promise, confirm, keep (happy path)

### 1a. Quote and checkout

```mermaid
sequenceDiagram
  autonumber
  actor SH as Shopper
  participant CO as customer-ordering
  participant DP as delivery-promise
  participant OS as order-sourcing
  SH->>DP: qry: POST /delivery-quotes
  DP-->>SH: qry: reply offers per tier
  SH->>CO: cmd: place order at checkout
  CO->>DP: cmd: POST /promises per group
  Note over DP: offer CONSUMED, promise PROVISIONAL, hold on the book
  DP-->>CO: cmd: reply 201 with holdExpiresAt
  DP-)DP: evt: PromiseMade
  CO->>DP: cmd: POST /promises/id/confirmations
  Note over DP: promise CONFIRMED, hold becomes confirmed
  DP-)DP: evt: PromiseConfirmed
  CO->>DP: evt: CustomerOrderPlaced (reconciling confirm, a no-op)
  CO->>OS: evt: CustomerOrderPlaced
```

Source: `apis/openapi.yaml`, `internal/application/usecases/make_promise.go`, `lifecycle.go` (`ConfirmPromise`), `projections.go` (`OnCustomerOrderPlaced`).

### 1b. Judged at delivery

```mermaid
sequenceDiagram
  autonumber
  participant OS as order-sourcing
  participant ST as shipping-and-tracking
  participant DP as delivery-promise
  participant CO as customer-ordering
  participant NP as node-performance
  OS->>DP: evt: FulfillmentLocked
  OS->>DP: evt: FulfillmentShipped
  ST->>DP: evt: PackageDelivered
  Note over DP: judge, last package delivered on time
  DP->>CO: evt: PromiseKept
  DP->>NP: evt: PromiseKept
```

Source: `internal/application/usecases/judge.go`. The group is judged only when the lock, the shipment and every delivery are known; the event that completes the facts triggers the judgement, whichever arrives last.

## 2. A cancelled departure breaks the witness path

### 2a. At risk, then revised

```mermaid
sequenceDiagram
  autonumber
  participant LH as linehaul-transportation
  participant DP as delivery-promise
  participant CO as customer-ordering
  participant OS as order-sourcing
  LH->>DP: evt: DepartureCancelled
  Note over DP: read model updated, promises using it found
  DP->>CO: evt: PromiseAtRisk
  Note over DP: ReviseAtRisk searches again
  alt a feasible path that does not move earlier exists
    DP->>CO: evt: PromiseRevised
    DP->>OS: evt: PromiseRevised
  else nothing feasible at any tier
    DP->>CO: evt: PromiseUnkeepable
    Note over DP: stays AT_RISK and the sweeper keeps retrying
  end
```

Source: `internal/application/usecases/risk.go`, `projections.go` (`OnDepartureCancelled`).

### 2b. Judged broken

```mermaid
sequenceDiagram
  autonumber
  participant ST as shipping-and-tracking
  participant DP as delivery-promise
  participant CO as customer-ordering
  participant NP as node-performance
  Note over DP: promise AT_RISK or revised too late
  ST->>DP: evt: PackageDelivered
  Note over DP: delivered after deliverByLatest
  DP->>CO: evt: PromiseBroken with cause LATE and lateByMinutes
  DP->>NP: evt: PromiseBroken
```

Source: `internal/application/usecases/judge.go`. A lost or returned package breaks the promise at once with cause `LOST` or `RETURNED`.

## 3. Order sourcing cannot meet the promise

```mermaid
sequenceDiagram
  autonumber
  participant OS as order-sourcing
  participant DP as delivery-promise
  participant CO as customer-ordering
  OS->>DP: evt: FulfillmentUnsourceable with promiseId and excludedNodes
  DP->>CO: evt: PromiseAtRisk
  Note over DP: ReviseAtRisk searches again, skipping excluded nodes
  alt revised window found
    DP->>CO: evt: PromiseRevised
    DP->>OS: evt: PromiseRevised
  else no revised promise is feasible
    DP->>CO: evt: PromiseUnkeepable
    CO->>DP: evt: CustomerOrderCancelled with groupNos
    Note over DP: CancelPromise, capacity released
    DP-)DP: evt: PromiseCancelled
  end
```

Source: `projections.go` (`OnFulfillmentUnsourceable`, `OnCustomerOrderCancelled`), `apis/asyncapi.yaml` (`PromiseUnkeepable`: customer-ordering consumes it and cancels the group; its `CustomerOrderCancelled` then cancels the promise).

## 4. A full pickup cutoff, and a hold nobody confirmed

```mermaid
sequenceDiagram
  autonumber
  participant CO as customer-ordering
  participant DP as delivery-promise
  participant OS as order-sourcing
  CO->>DP: cmd: POST /promises
  alt book has room
    DP-->>CO: cmd: reply 201 PROVISIONAL
    Note over DP: the hold that reaches share raises the signal
    DP->>OS: evt: PromiseBookExhausted
  else book already full or witness path no longer feasible
    DP-->>CO: cmd: reply 409 promise-capacity-exhausted
    CO->>DP: qry: POST /delivery-quotes
    DP-->>CO: qry: reply new offers
  end
  Note over CO: shopper abandons checkout
  Note over DP: ReleaseExpiredHolds after holdExpiresAt
  DP-)DP: evt: PromiseReleased
```

Source: `internal/domain/book/book.go`, `make_promise.go` (`refusal`), `lifecycle.go` (`ReleaseExpiredHolds`). The `book-closed` branch (the cutoff minus handling time has passed) has the same reply shape with `409 book-closed`.
