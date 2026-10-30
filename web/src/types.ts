/** Wire types of apis/openapi.yaml (camelCase, as the REST contract names them). */

export const PROMISE_STATES = ["PROVISIONAL", "CONFIRMED", "AT_RISK", "KEPT", "BROKEN", "RELEASED", "CANCELLED"] as const;
export type PromiseState = (typeof PROMISE_STATES)[number];

export const OFFER_STATES = ["QUOTED", "EXPIRED", "CONSUMED"] as const;
export type OfferState = (typeof OFFER_STATES)[number];

export const TIERS = ["SAME_DAY", "NEXT_DAY", "TWO_DAY", "STANDARD"] as const;
export type Tier = (typeof TIERS)[number];

/** A shipment group line. */
export interface GroupLine {
  catalogItemId: string;
  quantity: number;
}

/** The node, pickup departure, legs and delivery wave that prove feasibility. */
export interface WitnessPath {
  nodeId: string;
  pickupDepartureId?: string;
  legs: string[];
  deliveryWaveId?: string;
}

/** One short-lived quote for a (group, tier). */
export interface Offer {
  offerId: string;
  state: OfferState;
  groupNo: number;
  destinationPostalPrefix: string;
  tier: Tier;
  lines: GroupLine[];
  /** timeZone: the delivery station's IANA zone, the local calendar of the window (ADR 0011). */
  window: { earliest: string; latest: string; timeZone?: string };
  witnessPath: WitnessPath;
  basis: "SCHEDULED" | "NOMINAL";
  policyVersion: string;
  quotedAt: string;
  expiresAt: string;
}

/** What a promise was promised to. */
export interface PromiseTerms {
  tier: Tier;
  shipBy: string;
  deliverByEarliest: string;
  deliverByLatest: string;
  /** The delivery station's IANA zone, the local calendar of the window (ADR 0011). */
  timeZone?: string;
  basis: "SCHEDULED" | "NOMINAL";
  policyVersion: string;
}

/** One re-promise. */
export interface Revision {
  revisionNo: number;
  previousTerms: PromiseTerms;
  reason: string;
  revisedAt: string;
}

/** The verdict on a delivered promise. */
export interface Judgement {
  outcome: "KEPT" | "BROKEN";
  deliveredAt?: string;
  lateByMinutes?: number;
  cause?: "LATE" | "LOST" | "RETURNED";
}

/** A commitment for one shipment group. */
export interface Promise_ {
  promiseId: string;
  offerId: string;
  orderId?: string;
  groupNo?: number;
  state: PromiseState;
  destinationPostalPrefix: string;
  lines: GroupLine[];
  terms: PromiseTerms;
  witnessPath: WitnessPath;
  holdExpiresAt: string;
  revisions: Revision[];
  judgement?: Judgement;
  cancelReason?: string;
  releaseReason?: string;
  atRiskReason?: string;
  version: number;
}

/** GET /promises?orderId=. */
export interface PromiseList {
  items: Promise_[];
}

/** Capacity of one pickup cutoff book. */
export interface Capacity {
  share: number;
  provisional: number;
  confirmed: number;
}

/** One capacity ledger book (nodeId:pickupDepartureId). */
export interface Book {
  bookId: string;
  nodeId: string;
  pickupDepartureId: string;
  cutoffAt: string;
  state: "OPEN" | "CLOSED";
  capacity: Capacity;
  version: number;
}

/** GET /promise-books?nodeId=&date=. */
export interface BookList {
  items: Book[];
}

/** A per-tier, per-region order deadline. */
export interface OrderDeadline {
  tier: Tier;
  regionId?: string;
  localTime: string;
}

/** GET /promise-policies/current. */
export interface Policy {
  policyVersion: string;
  version: number;
  offerTtlMinutes: number;
  holdTtlMinutes: number;
  packagesPerOrder: number;
  capacitySharePercent: number;
  orderDeadlines: OrderDeadline[];
  publishedAt: string;
}
