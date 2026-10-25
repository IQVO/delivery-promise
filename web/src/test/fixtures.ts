import type { Book, Offer, Policy, Promise_ } from "../types";

/** The worked example of the promise model: NEXT_DAY for prefix 787 from
 * N-AUS3 through the 18:00 pickup departure and the W-DAU1-1023-A wave. */

export const TERMS = {
  tier: "NEXT_DAY",
  shipBy: "2026-10-22T23:00:00Z",
  deliverByEarliest: "2026-10-23T14:00:00Z",
  deliverByLatest: "2026-10-24T02:00:00Z",
  basis: "SCHEDULED",
  policyVersion: "v0",
} as const;

export const WITNESS = {
  nodeId: "N-AUS3",
  pickupDepartureId: "dep-AUS3-1800",
  legs: ["dep-SAU1-DAU1-2200"],
  deliveryWaveId: "W-DAU1-1023-A",
} as const;

export function offer(over: Partial<Offer> = {}): Offer {
  return {
    offerId: "off-1",
    state: "CONSUMED",
    groupNo: 1,
    destinationPostalPrefix: "787",
    tier: "NEXT_DAY",
    lines: [{ catalogItemId: "ci-1001", quantity: 2 }],
    window: { earliest: "2026-10-23T14:00:00Z", latest: "2026-10-24T02:00:00Z" },
    witnessPath: { ...WITNESS, legs: [...WITNESS.legs] },
    basis: "SCHEDULED",
    policyVersion: "v0",
    quotedAt: "2026-10-22T19:05:00Z",
    expiresAt: "2026-10-22T19:15:00Z",
    ...over,
  };
}

export function promise(over: Partial<Promise_> = {}): Promise_ {
  return {
    promiseId: "prm-1",
    offerId: "off-1",
    orderId: "ord-1001",
    groupNo: 1,
    state: "CONFIRMED",
    destinationPostalPrefix: "787",
    lines: [{ catalogItemId: "ci-1001", quantity: 2 }],
    terms: { ...TERMS },
    witnessPath: { ...WITNESS, legs: [...WITNESS.legs] },
    holdExpiresAt: "2026-10-23T01:05:00Z",
    revisions: [],
    version: 2,
    ...over,
  };
}

export function book(over: Partial<Book> = {}): Book {
  return {
    bookId: "N-AUS3:dep-AUS3-1800",
    nodeId: "N-AUS3",
    pickupDepartureId: "dep-AUS3-1800",
    cutoffAt: "2026-10-22T23:00:00Z",
    state: "OPEN",
    capacity: { share: 2500, provisional: 0, confirmed: 1 },
    version: 1,
    ...over,
  };
}

export function policy(over: Partial<Policy> = {}): Policy {
  return {
    policyVersion: "v1",
    version: 1,
    offerTtlMinutes: 5,
    holdTtlMinutes: 20,
    packagesPerOrder: 2,
    capacitySharePercent: 50,
    orderDeadlines: [{ tier: "NEXT_DAY", regionId: "R-SC", localTime: "23:59" }],
    publishedAt: "2026-10-22T18:00:00Z",
    ...over,
  };
}
