---
id: 0027
title: A pickup point as the quoted destination
---

# A pickup point as the quoted destination

## Status

Accepted (2026-11-13). Extends the quote input under RN-0011 (pickup points
and lockers); node-topology's ADR 0017 defines the point itself.

## Context

A shopper may check out to a pickup point instead of a ship-to address. The
promise must still name a destination prefix and a wave station, and must be
judged when the package arrives. node-topology already publishes every node -
including PICKUP_POINT rows with their serving station - and delivery-promise
keeps a local copy of those nodes in its read model, so no new projection
table is needed: the point is a node.

## Decision

- `QuoteCommand` and `EstimateCommand` gain `pickupPointId`: exactly one of
  `destinationPostalPrefix` or `pickupPointId` is required (422 otherwise).
- The point resolves through the existing node read model: its own postal
  code (labellable by node-topology's rule) becomes the destination prefix,
  truncated to the prefix maximum; its serving delivery station becomes the
  wave's station; the region is the station's own. An unknown, non-ACTIVE or
  station-less point yields no station, which is DESTINATION_NOT_SERVED -
  exactly how checkout fails for a bad point; no error is invented.
- The search itself is unchanged: the point is a STOP on the station's wave,
  so the candidate, witness and window of a pickup quote are those of a
  ship-to quote to that station.
- `PackageReadyForCollection` (shipping-and-tracking) is consumed and judged
  as the kept outcome of a pickup promise - the exact role `PackageDelivered`
  plays for a ship-to promise. Collection ending the package later changes
  no judgement.
- No synchronous call, no new table, no new consumer group: the point rides
  the node read model this service already keeps.

## Consequences

- The offer and promise carry the POINT's prefix as the destination prefix,
  which is what customer-ordering records as a pickup order's prefix.
- A point that goes SUSPENDED stops being quotable the moment the local read
  model sees it; offers already quoted keep their window until they expire.
- The pickup hold (uncollected packages returning to the sender) is
  shipping-and-tracking's sweep; this service needs no hold of its own.
