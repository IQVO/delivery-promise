---
id: class-diagram
title: Class diagram
sidebar_label: Class diagram
description: "The domain model of Delivery Promise (aggregates, value objects, events) and the hexagonal ports and adapters, from the Go packages."
---

# Class diagram

Two views: the domain model (`internal/domain`) and the ports and adapters around the use cases.

## Domain model

```mermaid
classDiagram
  class DeliveryOffer {
    +ID id
    +int groupNo
    +string destinationPrefix
    +Tier tier
    +Line[] lines
    +Window window
    +Witness witness
    +Basis basis
    +string policyVersion
    +time quotedAt
    +time expiresAt
    +State state
    +Consume(now)
    +Expire(now)
    +Quotable(now) bool
  }
  class Promise {
    +ID id
    +string offerId
    +string orderId
    +int groupNo
    +State state
    +string destinationPrefix
    +Line[] lines
    +Terms terms
    +Witness witness
    +string bookId
    +time holdExpiresAt
    +Revision[] revisions
    +Judgement judgement
    +int64 version
    +Make(in, now)$ Promise
    +Confirm(orderId, groupNo, now)
    +FlagAtRisk(reason, now)
    +Revise(in, now)
    +MarkUnkeepable(now)
    +Judge(verdict, now)
    +Cancel(reason, now)
    +ReleaseExpiredHold(now)
  }
  class PromiseBook {
    +ID id
    +string nodeId
    +string pickupDepartureId
    +time cutoffAt
    +time holdDeadline
    +int share
    +int provisional
    +int confirmed
    +State state
    +int64 version
    +Open(opening)$ Book
    +Hold(now)
    +ConfirmHold()
    +ReleaseProvisional()
    +ReleaseConfirmed()
    +Close(now)
  }
  class PromisePolicy {
    +int version
    +int offerTtl
    +int holdTtl
    +int packagesPerOrder
    +int sharePercent
    +Deadline[] deadlines
    +time publishedAt
    +Default()$ Policy
    +Publish(current, input, now)$ Policy
    +PastDeadline(tier, regionId, localMinute) bool
  }
  class Terms {
    +Tier tier
    +time shipBy
    +Window window
    +Basis basis
    +string policyVersion
  }
  class Revision {
    +int no
    +Terms previousTerms
    +string reason
    +time revisedAt
  }
  class Judgement {
    +Outcome outcome
    +time deliveredAt
    +int lateByMinutes
    +Cause cause
    +time judgedAt
  }
  class Witness {
    +string nodeId
    +string pickupDepartureId
    +string[] legs
    +string deliveryWaveId
  }
  class Window {
    +time earliest
    +time latest
    +string timeZone
  }
  class Line {
    +string catalogItemId
    +int quantity
  }
  class Deadline {
    +Tier tier
    +string regionId
    +int minute
  }
  class Search {
    <<domain service>>
    +Run(input) Result
    +OpeningFor(node, pickup, cutoff, policy)
    +BookIDFor(witness, cutoff)
  }
  DeliveryOffer "1" --> "0..1" Promise : consumed by
  Promise "*" --> "1" PromiseBook : holds capacity on
  Promise *-- Terms
  Promise *-- "0..*" Revision
  Promise *-- "0..1" Judgement
  Promise *-- Witness
  DeliveryOffer *-- Witness
  DeliveryOffer *-- Window
  Terms *-- Window
  PromisePolicy *-- "0..*" Deadline
  Search ..> PromisePolicy : reads
  Search ..> DeliveryOffer : proposes
```

Source: `internal/domain/offer/offer.go`, `internal/domain/promise/promise.go`, `internal/domain/book/book.go`, `internal/domain/policy/policy.go`, `internal/domain/search`, `internal/domain/shared`.

Enumerations: `Tier` (`SAME_DAY`, `NEXT_DAY`, `TWO_DAY`, `STANDARD`; only `STANDARD` allows a `NOMINAL` basis), `Basis` (`SCHEDULED`, `NOMINAL`), offer `State` (`QUOTED`, `EXPIRED`, `CONSUMED`), promise `State` (`PROVISIONAL`, `CONFIRMED`, `AT_RISK`, `KEPT`, `BROKEN`, `RELEASED`, `CANCELLED`), book `State` (`OPEN`, `CLOSED`), `Outcome` (`KEPT`, `BROKEN`), `Cause` (`LATE`, `LOST`, `RETURNED`). Each aggregate has a `Snapshot` and a `Restore` that validates every invariant (`shared.Corrupt` otherwise). Domain events (`PromiseMade`, `PromiseConfirmed`, `PromiseAtRisk`, `PromiseRevised`, `PromiseKept`, `PromiseBroken`, `PromiseReleased`, `PromiseCancelled`, `PromiseUnkeepable`, `PromiseBookExhausted`, `DeliveryOfferQuoted`, `PromisePolicyPublished`) are returned by the commands and appended to the outbox by the use case.

## Ports and adapters

```mermaid
classDiagram
  class Service {
    <<usecases.Service>>
    +QuoteDeliveryOffers()
    +MakePromise()
    +ConfirmPromise()
    +CancelPromise()
    +PublishPromisePolicy()
    +ReviseAtRisk()
    +ReleaseExpiredHolds()
    +ExpireOffers()
    +CloseBooks()
    +RepriceAtRisk()
    +On... projections
    +Get/List reads
  }
  class UnitOfWork { <<port>> }
  class OfferRepository { <<port>> }
  class PromiseRepository { <<port>> }
  class BookRepository { <<port>> }
  class PolicyRepository { <<port>> }
  class NetworkViews { <<port>> }
  class ShipmentViews { <<port>> }
  class EventOutbox { <<port>> }
  class Inbox { <<port>> }
  class Clock { <<port>> }
  class IDGenerator { <<port>> }
  class PromiseMetrics { <<port>> }
  class HTTPHandlers { <<inbound chi>> }
  class KafkaConsumer { <<inbound>> }
  class MCPTools { <<inbound read-only>> }
  class A2ASkills { <<inbound read-only>> }
  class PostgresAdapters { <<outbound>> }
  class OutboxRelay { <<outbound>> }
  class KafkaWriter { <<outbound>> }
  class Telemetry { <<outbound>> }
  HTTPHandlers --> Service
  KafkaConsumer --> Service
  MCPTools --> Service : reads only
  A2ASkills --> Service : reads only
  Service --> UnitOfWork
  Service --> OfferRepository
  Service --> PromiseRepository
  Service --> BookRepository
  Service --> PolicyRepository
  Service --> NetworkViews
  Service --> ShipmentViews
  Service --> EventOutbox
  Service --> Inbox
  Service --> Clock
  Service --> IDGenerator
  Service --> PromiseMetrics
  PostgresAdapters ..|> UnitOfWork
  PostgresAdapters ..|> OfferRepository
  PostgresAdapters ..|> PromiseRepository
  PostgresAdapters ..|> BookRepository
  PostgresAdapters ..|> PolicyRepository
  PostgresAdapters ..|> NetworkViews
  PostgresAdapters ..|> ShipmentViews
  PostgresAdapters ..|> EventOutbox
  PostgresAdapters ..|> Inbox
  Telemetry ..|> PromiseMetrics
  OutboxRelay --> KafkaWriter : sends rows
  OutboxRelay --> PostgresAdapters : claims rows
```

Source: `internal/application/usecases/service.go` (`Deps`), `internal/application/ports`, `cmd/api/main.go`, `cmd/api/workers.go`, `internal/adapters/**`. `Clock` and `IDGenerator` are implemented by `outbound/clock` and `outbound/ids`; `cmd/mcp` and `cmd/a2a` construct the same `Service` with a `Reads`-only facade, so neither can reach a write method.
