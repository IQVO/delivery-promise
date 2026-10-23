# ADR 0003: consumed contracts, local read models and relationships

## Status

Accepted (2026-10-23).

## Context

The promise sits on the shopper's critical path and makes NO synchronous call (model `sync_calls: []`). Every input is a local read model kept from events. The consumed contracts are the producers' CloudEvents; `apis/asyncapi.yaml` restates, as `receive` operations, only the fields this service relies on (tolerant decoding: unknown fields and unknown types are ignored).

## Decision

### Consumed events (dispatch on the FULL type; consumer group mandatory)

| Producer topic | Types (`com.retailnetwork.<tier>.<producer>.<entity>.<Event>`) | Effect |
|---|---|---|
| `retailnetwork.node-topology.events` | `node.NodeRegistered`, `node.NodeOperationalProfileDeclared`, `node.NodeActivated`, `node.NodeSuspended`, `node.NodeRetired`, `lane.LaneOpened`, `lane.LaneClosed`, `servicearea.ServiceAreaAssigned`, `region.RegionDefined` | NodeProfileView, LaneView, RegionAndServiceAreaView |
| `retailnetwork.offer-availability.events` | `catalogitem.CatalogItemCreated`, `catalogitem.CatalogItemDiscontinued`, `listing.ListingCreated`, `listing.ListingAvailabilityChanged`, `listing.ListingSuppressed`, `listing.ListingRestored` | AvailabilityView |
| `retailnetwork.linehaul-transportation.events` | `departure.DepartureScheduled`, `departure.DepartureCapacityChanged`, `departure.DepartureCancelled`, `departure.BookingMoved`, `departure.BookingFailed` | DepartureView; RepromiseOnRisk (E13) |
| `retailnetwork.last-mile-delivery.events` | `wave.DeliveryWaveScheduled`, `wave.DeliveryWaveCapacityChanged`, `wave.DeliveryWaveCancelled`, `packageattempthistory.PackageInductionDeferred` | DeliveryWaveView; RepromiseOnRisk (E5, E15) |
| `retailnetwork.node-performance.events` | `scorecard.NodeScorecardUpdated` | ScorecardView (30d window only, E18) |
| `retailnetwork.order-sourcing.events` | `assignment.FulfillmentLocked`, `assignment.FulfillmentShipped`, `assignment.FulfillmentUnsourceable` | assignment map and package set (E1); RepromiseOnRisk |
| `retailnetwork.shipping-and-tracking.events` | `journey.PackageDelivered`, `journey.PackageExceptionRaised`, `journey.PackageLost`, `journey.PackageReturned` | JudgeOnDelivery, RepromiseOnRisk (E6) |
| `retailnetwork.customer-ordering.events` | `customerorder.CustomerOrderPlaced`, `customerorder.CustomerOrderCancelled` | reconciling ConfirmPromise (E7); CancelPromise (G21) |

One consumer reads the eight topics under ONE stable group, `DELIVERY_PROMISE_CONSUMER_GROUP` (a chart value, never derived from the pod name). A missing group is a boot error naming the variable, raised before the listener binds. A transient failure is retried with capped backoff, then the message goes to `<topic>.dlq`; a deterministic problem (not a CloudEvent, unknown type, malformed payload) is logged and skipped. Every handler runs in a unit of work that also inserts the CloudEvents `id` into `processed_events` (inbox), so a redelivery commits nothing.

### Local read models (Postgres tables, rebuilt from events)

`rm_nodes` (type, subtype, operator, region, time zone, state, profile revision and profile), `rm_lanes`, `rm_regions`, `rm_service_areas`, `rm_catalog_items`, `rm_listings`, `rm_departures`, `rm_waves`, `rm_scorecards`, `rm_assignments` (assignmentId to promiseId, orderId, groupNo, locked node), `rm_shipments` (trackingNumber to assignmentId) and `rm_package_outcomes`. Version guards make every projection idempotent and order-tolerant: a `profileRevision`, `assignmentVersion` or `asOf` not above the stored value is ignored (A25).

### Relationships

Customer/Supplier with customer-ordering (it calls `POST /delivery-offers:quote`, `POST /promises` and `POST /promises/{promiseId}:confirm`; it consumes `PromiseAtRisk`, `PromiseRevised`, `PromiseKept`, `PromiseBroken`, `PromiseUnkeepable`) and with order-sourcing (consumes `PromiseRevised`, `PromiseBookExhausted`); node-performance consumes `PromiseKept` and `PromiseBroken`. Open Host Service for the storefront (quote). Conformist to node-topology, offer-availability, linehaul-transportation, last-mile-delivery, node-performance and shipping-and-tracking.

### Sync calls

None. No client of a sibling service exists in this repository.

## Consequences

- A cold or stale read model yields fewer offers, never a false one: fast tiers need every input, only STANDARD falls back to nominal (A1).
- Every consumed type was re-read against the producers' `origin/feature/service` AsyncAPI (2026-10-23); the two mismatches found (A26) were fixed on the consuming side. `PromiseBroken.cause` is `LATE`, `LOST` or `RETURNED` (A18).
- The receive messages of `apis/asyncapi.yaml` stay tolerant copies: they list only the fields this service reads.
