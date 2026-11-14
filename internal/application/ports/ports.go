// Package ports declares the application's OUT ports: interfaces only
// (enforced by internal/architecture). Their non-interface vocabulary is the
// two sentinels of errors.go, the filters of internal/application/repository
// and the read-model rows of internal/application/readmodel.
//
// Optimistic concurrency: every Save expects the stored version to be
// aggregate.Version()-1 (ADR 0002); a create (Version() 1) expects that the id
// is not stored. Any mismatch, including an update of a row that no longer
// exists, is ErrConcurrentUpdate. Inside a UnitOfWork every repository joins
// the transaction carried by the ctx the unit handed to its function.
package ports

import (
	"context"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/domain/book"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// OfferRepository persists DeliveryOffer aggregates.
type OfferRepository interface {
	// Get returns the offer in any state, or ErrNotFound.
	Get(ctx context.Context, id offer.ID) (*offer.Offer, error)
	// ListExpired returns the ids of QUOTED offers with expiresAt <= now,
	// oldest first, at most limit.
	ListExpired(ctx context.Context, now time.Time, limit int) ([]offer.ID, error)
	// Save persists the offer under the concurrency rule of the package comment.
	Save(ctx context.Context, o *offer.Offer) error
}

// PromiseRepository persists Promise aggregates.
type PromiseRepository interface {
	// Get returns the promise, or ErrNotFound.
	Get(ctx context.Context, id promise.ID) (*promise.Promise, error)
	// GetByOffer returns the promise made from the offer (an offer backs at
	// most one), or ErrNotFound.
	GetByOffer(ctx context.Context, offerID string) (*promise.Promise, error)
	// FindByOrderGroup returns the promise confirmed for an order group, or
	// ErrNotFound.
	FindByOrderGroup(ctx context.Context, orderID string, groupNo int) (*promise.Promise, error)
	// ListByOrder returns the promises confirmed for the order ordered by
	// groupNo; none is an empty list.
	ListByOrder(ctx context.Context, orderID string) ([]*promise.Promise, error)
	// ListHoldsExpired returns the ids of PROVISIONAL promises with
	// holdExpiresAt <= now, oldest first, at most limit.
	ListHoldsExpired(ctx context.Context, now time.Time, limit int) ([]promise.ID, error)
	// ListAtRisk returns the ids of AT_RISK promises, oldest first, at most limit.
	ListAtRisk(ctx context.Context, limit int) ([]promise.ID, error)
	// ListAtRiskPage returns the ids of AT_RISK promises in the same order as
	// ListAtRisk (oldest first), skipping the first offset, at most limit.
	ListAtRiskPage(ctx context.Context, offset, limit int) ([]promise.ID, error)
	// ListConfirmed returns the ids of CONFIRMED promises with an id greater
	// than after, ordered by id, at most limit; an empty after starts at the
	// first. The late-risk sweep pages through them (ADR 0021).
	ListConfirmed(ctx context.Context, after promise.ID, limit int) ([]promise.ID, error)
	// ListActiveByPath returns the ids of CONFIRMED and AT_RISK promises whose
	// witness path uses the departure or the wave of the filter.
	ListActiveByPath(ctx context.Context, f repository.PromiseRiskFilter) ([]promise.ID, error)
	// HeldQuantities returns the units held by PROVISIONAL promises, per
	// (node, item), for the nodes.
	HeldQuantities(ctx context.Context, nodeIDs []string) (map[repository.HoldKey]int, error)
	// ConfirmedHolds returns the lines of the active (CONFIRMED and AT_RISK)
	// promises, per (node, item) of their witness, with the instant each was
	// confirmed: units the warehouse has not reserved yet (ADR 0015).
	ConfirmedHolds(ctx context.Context, nodeIDs []string) ([]repository.ConfirmedHold, error)
	// Save persists the promise under the concurrency rule of the package
	// comment. A second promise for the same offer is ErrConcurrentUpdate.
	Save(ctx context.Context, p *promise.Promise) error
}

// BookRepository persists PromiseBook aggregates.
type BookRepository interface {
	// Get returns the book, or ErrNotFound.
	Get(ctx context.Context, id book.ID) (*book.Book, error)
	// List returns the books matching the filter ordered by cutoffAt then id.
	List(ctx context.Context, f repository.BookFilter) ([]*book.Book, error)
	// ListOpenDue returns the ids of OPEN books with cutoffAt <= now, oldest
	// first, at most limit.
	ListOpenDue(ctx context.Context, now time.Time, limit int) ([]book.ID, error)
	// Save persists the book under the concurrency rule of the package comment.
	Save(ctx context.Context, b *book.Book) error
}

// PolicyRepository persists PromisePolicy versions.
type PolicyRepository interface {
	// Current returns the highest published version, or ErrNotFound when none
	// was published (the caller then uses policy.Default()).
	Current(ctx context.Context) (*policy.Policy, error)
	// Save stores a new version; a version that already exists is
	// ErrConcurrentUpdate.
	Save(ctx context.Context, p *policy.Policy) error
}

// NetworkViews stores the event-fed read models the search runs on. A Get that
// matches nothing is ErrNotFound; a Put inserts or replaces the row.
type NetworkViews interface {
	GetNode(ctx context.Context, id string) (readmodel.Node, error)
	PutNode(ctx context.Context, n readmodel.Node) error
	GetRegion(ctx context.Context, id string) (readmodel.Region, error)
	PutRegion(ctx context.Context, r readmodel.Region) error
	GetArea(ctx context.Context, postalPrefix string) (readmodel.Area, error)
	PutArea(ctx context.Context, a readmodel.Area) error
	GetLane(ctx context.Context, id string) (search.Lane, error)
	PutLane(ctx context.Context, l search.Lane) error
	GetItem(ctx context.Context, id string) (readmodel.Item, error)
	PutItem(ctx context.Context, i readmodel.Item) error
	GetListing(ctx context.Context, id string) (readmodel.Listing, error)
	// ListingsForNode returns the listings of the items at the node.
	ListingsForNode(ctx context.Context, nodeID string, itemIDs []string) ([]readmodel.Listing, error)
	PutListing(ctx context.Context, l readmodel.Listing) error
	GetDeparture(ctx context.Context, id string) (search.Departure, error)
	PutDeparture(ctx context.Context, d search.Departure) error
	GetWave(ctx context.Context, id string) (search.Wave, error)
	PutWave(ctx context.Context, w search.Wave) error
	GetScorecard(ctx context.Context, nodeID string) (readmodel.Scorecard, error)
	PutScorecard(ctx context.Context, s readmodel.Scorecard) error
	// Load returns the slice of the network a search for the items needs: the
	// nodes with a listing of any item and every node a departure in the
	// window touches, the listings, the items, the scorecards of those nodes,
	// the lanes, the scheduled departures with cutoffAt in [From, Until) and
	// the scheduled waves at the station.
	Load(ctx context.Context, q readmodel.Query) (readmodel.Network, error)
}

// ShipmentViews stores the assignment map and the package facts that
// JudgeOnDelivery needs. A Get that matches nothing is ErrNotFound.
type ShipmentViews interface {
	GetAssignment(ctx context.Context, assignmentID string) (readmodel.Assignment, error)
	PutAssignment(ctx context.Context, a readmodel.Assignment) error
	// FindAssignmentByPromise returns the assignment locked for the promise.
	FindAssignmentByPromise(ctx context.Context, promiseID string) (readmodel.Assignment, error)
	// PutShipment records that the trackingNumbers belong to the assignment.
	// A number already owned by another assignment is shared, never re-owned.
	PutShipment(ctx context.Context, assignmentID string, trackingNumbers []string) error
	// Shipment returns the tracking numbers of the assignment (empty when
	// FulfillmentShipped has not been seen).
	Shipment(ctx context.Context, assignmentID string) ([]string, error)
	// AssignmentsOf returns every assignment a tracking number was shipped
	// under, sorted ascending (a merged package is shared by several, ADR
	// 0025), or ErrNotFound when there is none.
	AssignmentsOf(ctx context.Context, trackingNumber string) ([]string, error)
	// PutOutcome records the first final fact of a package; a second fact for
	// the same tracking number is ignored.
	PutOutcome(ctx context.Context, o readmodel.PackageOutcome) error
	// Outcomes returns the recorded outcomes of the tracking numbers.
	Outcomes(ctx context.Context, trackingNumbers []string) ([]readmodel.PackageOutcome, error)
}

// Inbox is the consumer-side idempotency table (processed_events).
type Inbox interface {
	// Begin records the CloudEvents id inside the unit of work and reports
	// whether it is new. false means the event was already processed: the
	// handler must change nothing.
	Begin(ctx context.Context, eventID, eventType string, at time.Time) (bool, error)
}

// TransitActuals stores the measured-transit read model (ADR 0014): one open
// row per departed load, paired by LoadArrived into a ring of observed
// transits per (origin, dest) pair; and, since ADR 0026, one open row per
// handed-over package, paired by the delivery into the same ring under the
// key (handoffNodeID, "area:"+prefix). Every call joins the unit of work
// carried by ctx when there is one.
type TransitActuals interface {
	// RecordLoadDeparted opens the pairing row of a load.
	RecordLoadDeparted(ctx context.Context, loadID, originNodeID string, departedAt time.Time) error
	// RecordLoadArrived pairs the load's open row into one observation of its
	// (origin, dest) pair, appends it to the ring (latest 200) and deletes the
	// open row. A load with no open row changes nothing.
	RecordLoadArrived(ctx context.Context, loadID, destNodeID string, arrivedAt time.Time) error
	// RecordCarrierHandoff opens the carrier pairing row of a package handed
	// to the carrier of a CARRIER_HANDOFF area, keyed by tracking number with
	// the ring key (handoffNodeID, "area:"+prefix) it will pair under
	// (ADR 0026). A repeated handoff overwrites the row.
	RecordCarrierHandoff(ctx context.Context, trackingNumber, handoffNodeID, postalPrefix string, handedAt time.Time) error
	// RecordCarrierDelivered pairs the package's open carrier row into one
	// observation of its (handoffNodeID, "area:"+prefix) pair and deletes the
	// row; a package with no open row changes nothing (ADR 0026).
	RecordCarrierDelivered(ctx context.Context, trackingNumber string, deliveredAt time.Time) error
	// MeasuredTransit answers the nearest-rank `percentile` transit in minutes
	// of the pair's observations inside the last windowDays days; ok is false
	// while there are fewer than minObservations of them.
	MeasuredTransit(ctx context.Context, origin, dest string, percentile, minObservations, windowDays int, now time.Time) (bool, int)
}

// PromiseCalibrationView stores the calibration read model (ADR 0020): per
// segment (tier, basis, node grade) the ring of the latest judged promises'
// lateBy minutes. It is fed only by this service's own judgements, in the unit
// of work that records the judgement, and read by every search. Every call
// joins the unit of work carried by ctx when there is one.
type PromiseCalibrationView interface {
	// RecordJudgement appends one observation to the segment's ring (latest
	// 500) under a row lock, pruning what is older than the longest window.
	RecordJudgement(ctx context.Context, key readmodel.CalibrationKey, obs readmodel.CalibrationObservation) error
	// Segments returns, per segment, the lateBy minutes of the observations
	// inside the last windowDays days, oldest first. A segment with none is
	// absent.
	Segments(ctx context.Context, windowDays int, now time.Time) (map[readmodel.CalibrationKey][]int, error)
}

// EventOutbox is the write side of the transactional outbox. Called with the
// ctx handed to UnitOfWork.Do it joins the SAME transaction as the aggregate
// Save calls, so the aggregates and their raised events commit or roll back
// together. The adapter encodes each event into its CloudEvents envelope (the
// id is minted once, at append) and stores one row per event in order; a relay
// drains the rows to Kafka afterwards. Appending no events is a no-op.
type EventOutbox interface {
	Append(ctx context.Context, events ...shared.Event) error
}

// UnitOfWork runs fn as ONE atomic unit: every repository and outbox call fn
// makes with the ctx it is handed commits together when fn returns nil, or
// rolls back together when fn returns an error (or panics).
//
//   - fn MUST use the ctx passed to it for every call that should participate;
//     the unit travels in that ctx.
//   - Do is re-entrant: when ctx already carries an open unit, Do joins it and
//     the outermost Do owns commit and rollback.
//   - The error fn returns is returned from Do unchanged (errors.Is keeps
//     working); a commit failure is returned wrapped.
type UnitOfWork interface {
	Do(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock is the service's notion of "now"; use cases stamp aggregates with it.
type Clock interface {
	Now() time.Time
}

// IDGenerator mints the opaque ids of new aggregates.
type IDGenerator interface {
	NewOfferID() offer.ID
	NewPromiseID() promise.ID
}

// CalibrationMetrics records what the calibration buffer did (ADR 0020). It is
// an optional capability of PromiseMetrics: the use cases record through it
// when the Deps' Metrics also implements it, and not at all otherwise. The
// implementation lives in internal/adapters/outbound/telemetry.
type CalibrationMetrics interface {
	// CalibrationBufferApplied records the buffer in minutes folded into the
	// offer a quote built for the tier (0 when the segment added none).
	CalibrationBufferApplied(ctx context.Context, tier string, minutes int)
	// CalibrationSegmentBelowMinimum counts a segment a search loaded whose
	// observations are fewer than minCalibrationObservations: it quotes as it
	// did before calibration.
	CalibrationSegmentBelowMinimum(ctx context.Context, tier, basis, nodeGrade string)
}

// PromiseMetrics records the business outcome of every MakePromise attempt
// (Tier 2 of the fleet metrics convention). The use case calls it once per
// attempt: PromiseMade when a new promise was held, PromiseRefused otherwise,
// with the problem slug that says why (the `type` suffix the REST adapter
// answers with). A replay of an earlier request is not an attempt. The
// implementation lives in internal/adapters/outbound/telemetry; a nil
// Deps.Metrics records nothing.
type PromiseMetrics interface {
	// PromiseMade counts a promise that was made and its capacity held.
	PromiseMade(ctx context.Context)
	// PromiseRefused counts a MakePromise that made no promise; reason is a
	// stable, low-cardinality slug.
	PromiseRefused(ctx context.Context, reason string)
	// TierCandidatesExcluded counts n candidate nodes removed from a fast tier
	// because node-performance revoked their eligibility (ADR 0022); tier is
	// SAME_DAY or NEXT_DAY.
	TierCandidatesExcluded(ctx context.Context, tier string, n int)
	// TierEligibilityIgnored counts a NodeTierEligibilityChanged the consumer
	// could not apply; reason is `unknown-tier` or `missing-field`.
	TierEligibilityIgnored(ctx context.Context, reason string)
}
