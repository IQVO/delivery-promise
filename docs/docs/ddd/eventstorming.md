---
id: eventstorming
title: EventStorming
sidebar_label: EventStorming
description: "Process-level EventStorming boards for Delivery Promise: read-model feed, quote to confirm, risk and judgement, clock-driven sweeps, with hotspots."
---

# EventStorming

Process-level boards following the ddd-crew [EventStorming glossary](https://github.com/ddd-crew/eventstorming-glossary-cheat-sheet): orange **events**, blue **commands**, lilac **policies**, yellow **aggregates**, green **read models**, pink **external systems**, red **hotspots**. They are drawn from the code on this branch; the command, policy and event names are the use-case and event names of `internal/application/usecases`.

```mermaid
flowchart LR
  classDef evt fill:#ffa94d,stroke:#d9480f,color:#000
  classDef cmd fill:#74c0fc,stroke:#1971c2,color:#000
  classDef pol fill:#d0bfff,stroke:#7048e8,color:#000
  classDef agg fill:#ffe066,stroke:#e67700,color:#000
  classDef rm fill:#b2f2bb,stroke:#2f9e44,color:#000
  classDef ext fill:#ffc9c9,stroke:#c92a2a,color:#000
  classDef hot fill:#ff6b6b,stroke:#c92a2a,color:#000
  subgraph B1["Board 1: feed the read models"]
    direction LR
    x1["node-topology, offer-availability, linehaul, last-mile, node-performance"]:::ext
    e1(["Node, lane, listing, departure, wave, scorecard events"]):::evt
    p1{{"Project into local read models (inbox claim)"}}:::pol
    r1[("rm_nodes, rm_lanes, rm_areas, rm_regions, rm_items, rm_listings, rm_departures, rm_waves, rm_scorecards")]:::rm
    x1 --> e1 --> p1 --> r1
  end
  subgraph B2["Board 2: quote, promise, confirm"]
    direction LR
    c1["QuoteDeliveryOffers"]:::cmd
    a1["DeliveryOffer"]:::agg
    e2(["DeliveryOfferQuoted"]):::evt
    c2["MakePromise"]:::cmd
    a2["Promise + PromiseBook"]:::agg
    e3(["PromiseMade"]):::evt
    e4(["PromiseBookExhausted"]):::evt
    c3["ConfirmPromise"]:::cmd
    e5(["PromiseConfirmed"]):::evt
    c1 --> a1 --> e2
    c2 --> a2 --> e3
    a2 --> e4
    c3 --> a2 --> e5
  end
  r1 -.-> c1
```

## Board 1: feed the read models

Every consumed topic has one handler per type (`Handlers`). A handler claims the CloudEvents `id` in `processed_events` and writes one `rm_*` row in the same transaction. There is no command and no aggregate here: the owner of the data can overwrite any row.

**Hotspot H1: freshness.** A stale departure or wave row can be quoted; `MakePromise` revalidates the witness path against the current rows and refuses a path that is no longer feasible as `promise-capacity-exhausted`. **Hotspot H2: out-of-order events.** A capacity event can arrive before its schedule event; the projection keeps the remaining count already stored when the schedule event arrives later.

## Board 2: quote, promise, confirm

```mermaid
flowchart LR
  classDef evt fill:#ffa94d,stroke:#d9480f,color:#000
  classDef cmd fill:#74c0fc,stroke:#1971c2,color:#000
  classDef pol fill:#d0bfff,stroke:#7048e8,color:#000
  classDef agg fill:#ffe066,stroke:#e67700,color:#000
  classDef ext fill:#ffc9c9,stroke:#c92a2a,color:#000
  classDef hot fill:#ff6b6b,stroke:#c92a2a,color:#000
  sh["Shopper / storefront"]:::ext
  co["customer-ordering checkout"]:::ext
  q["QuoteDeliveryOffers"]:::cmd
  off["DeliveryOffer QUOTED"]:::agg
  mk["MakePromise"]:::cmd
  prm["Promise PROVISIONAL + book hold"]:::agg
  ev1(["PromiseMade"]):::evt
  ev2(["PromiseBookExhausted"]):::evt
  cf["ConfirmPromise"]:::cmd
  cfd["Promise CONFIRMED"]:::agg
  ev3(["PromiseConfirmed"]):::evt
  pl{{"On CustomerOrderPlaced, confirm if still PROVISIONAL"}}:::pol
  h1["H3 order placed after the hold expired"]:::hot
  sh --> q --> off
  co --> mk --> prm --> ev1
  prm --> ev2
  co --> cf --> cfd --> ev3
  pl --> cf
  pl -.-> h1
```

Hotspot H3: the hold expired before the order was placed. The synchronous confirm in checkout makes a dead promise visible to the caller (`promise-hold-expired`); the reconciling consumer only logs `order placed on a dead promise` and never retries. Hotspot H4: `MakePromise` changes three aggregates (offer, promise, book) in one local transaction: a deliberate exception, because a promise without a hold would break the rule that a promise reserves capacity.

## Board 3: risk, re-promise and judgement

```mermaid
flowchart LR
  classDef evt fill:#ffa94d,stroke:#d9480f,color:#000
  classDef cmd fill:#74c0fc,stroke:#1971c2,color:#000
  classDef pol fill:#d0bfff,stroke:#7048e8,color:#000
  classDef agg fill:#ffe066,stroke:#e67700,color:#000
  classDef ext fill:#ffc9c9,stroke:#c92a2a,color:#000
  classDef hot fill:#ff6b6b,stroke:#c92a2a,color:#000
  x1(["FulfillmentUnsourceable, PackageExceptionRaised, PackageInductionDeferred, DepartureCancelled, DeliveryWaveCancelled, BookingFailed, BookingMoved"]):::ext
  rr{{"RepromiseOnRisk"}}:::pol
  fl["FlagAtRisk"]:::cmd
  pa["Promise AT_RISK"]:::agg
  eAR(["PromiseAtRisk"]):::evt
  rv["ReviseAtRisk"]:::cmd
  eRV(["PromiseRevised"]):::evt
  eUK(["PromiseUnkeepable"]):::evt
  h5["H5 nothing feasible: stays AT_RISK, sweeper retries"]:::hot
  y1(["FulfillmentLocked, FulfillmentShipped, PackageDelivered, PackageLost, PackageReturned"]):::ext
  jd{{"JudgeOnDelivery"}}:::pol
  jg["Judge"]:::cmd
  pj["Promise KEPT or BROKEN"]:::agg
  eK(["PromiseKept"]):::evt
  eB(["PromiseBroken"]):::evt
  x1 --> rr --> fl --> pa --> eAR
  pa --> rv
  rv --> eRV
  rv --> eUK --> h5
  y1 --> jd --> jg --> pj
  pj --> eK
  pj --> eB
```

Hotspot H5: a promise that cannot be revised is not broken until a package is judged; `PromiseUnkeepable` is raised once and customer-ordering is expected to cancel the group, which cancels the promise. Hotspot H6: judging needs the assignment (from `FulfillmentLocked`) and the package set (from `FulfillmentShipped`) as well as the delivery; whichever event arrives last triggers the judgement (ADR 0002 A25).

## Board 4: the clock

```mermaid
flowchart LR
  classDef evt fill:#ffa94d,stroke:#d9480f,color:#000
  classDef cmd fill:#74c0fc,stroke:#1971c2,color:#000
  classDef agg fill:#ffe066,stroke:#e67700,color:#000
  classDef pol fill:#d0bfff,stroke:#7048e8,color:#000
  tick{{"Sweeper every SWEEP_INTERVAL"}}:::pol
  c1["ExpireOffers"]:::cmd
  c2["ReleaseExpiredHolds"]:::cmd
  c3["CloseBooks"]:::cmd
  c4["RepriceAtRisk"]:::cmd
  a1["DeliveryOffer EXPIRED"]:::agg
  a2["Promise RELEASED + book hold returned"]:::agg
  a3["PromiseBook CLOSED"]:::agg
  e2(["PromiseReleased"]):::evt
  tick --> c1 --> a1
  tick --> c2 --> a2 --> e2
  tick --> c3 --> a3
  tick --> c4
```

Source for all boards: `internal/application/usecases/*.go`, `cmd/api/workers.go`, `internal/adapters/inbound/kafka/dispatch.go`.
