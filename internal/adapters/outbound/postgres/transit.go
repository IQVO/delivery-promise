package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
)

// TransitActuals is the Postgres ports.TransitActuals over the
// rm_load_transit_open and rm_transit_actuals tables (ADR 0014).
type TransitActuals struct{ db }

// NewTransitActuals constructs TransitActuals over pool.
func NewTransitActuals(pool *pgxpool.Pool) *TransitActuals { return &TransitActuals{db{pool}} }

// RecordLoadDeparted opens the pairing row of a load and prunes the open rows
// older than the longest transit window, so a load whose arrival was never
// seen cannot linger.
func (t *TransitActuals) RecordLoadDeparted(ctx context.Context, loadID, originNodeID string, departedAt time.Time) error {
	if _, err := t.q(ctx).Exec(ctx,
		`INSERT INTO rm_load_transit_open (load_id, origin_node_id, departed_at) VALUES ($1, $2, $3)
		ON CONFLICT (load_id) DO UPDATE SET origin_node_id = EXCLUDED.origin_node_id, departed_at = EXCLUDED.departed_at`,
		loadID, originNodeID, departedAt); err != nil {
		return err
	}
	_, err := t.q(ctx).Exec(ctx, `DELETE FROM rm_load_transit_open WHERE departed_at < $1`, departedAt.AddDate(0, 0, -policy.MaxTransitWindowDays))
	return err
}

// RecordLoadArrived pairs the load's open row into one observation of its
// (origin, dest) pair, appends it to the ring (latest 200) and deletes the
// open row; a load with no open row changes nothing.
func (t *TransitActuals) RecordLoadArrived(ctx context.Context, loadID, destNodeID string, arrivedAt time.Time) error {
	var origin string
	var departedAt time.Time
	err := t.q(ctx).QueryRow(ctx, `SELECT origin_node_id, departed_at FROM rm_load_transit_open WHERE load_id = $1`, loadID).Scan(&origin, &departedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := t.observe(ctx, origin, destNodeID, departedAt, arrivedAt); err != nil {
		return err
	}
	_, err = t.q(ctx).Exec(ctx, `DELETE FROM rm_load_transit_open WHERE load_id = $1`, loadID)
	return err
}

// observe appends one observed transit of (origin, dest) to the ring and
// stores it back: the shared pairing step of the load and carrier rows.
func (t *TransitActuals) observe(ctx context.Context, origin, dest string, departedAt, arrivedAt time.Time) error {
	ring, err := t.ring(ctx, origin, dest)
	if err != nil {
		return err
	}
	minutes := int(arrivedAt.Sub(departedAt).Minutes())
	ring = readmodel.AppendTransitObservation(ring,
		readmodel.TransitObservation{Minutes: minutes, ObservedAt: arrivedAt.UTC()}, policy.MaxTransitWindowDays, arrivedAt)
	_, err = t.q(ctx).Exec(ctx,
		`INSERT INTO rm_transit_actuals (origin_node_id, dest_node_id, observations) VALUES ($1, $2, $3)
		ON CONFLICT (origin_node_id, dest_node_id) DO UPDATE SET observations = EXCLUDED.observations`,
		origin, dest, mustJSON(ring))
	return err
}

// RecordCarrierHandoff opens the carrier pairing row of a package handed to
// the carrier of a CARRIER_HANDOFF area, keyed by tracking number under the
// ring pair (handoffNodeID, "area:"+prefix), and prunes the open rows older
// than the longest transit window (ADR 0026).
func (t *TransitActuals) RecordCarrierHandoff(ctx context.Context, trackingNumber, handoffNodeID, postalPrefix string, handedAt time.Time) error {
	if _, err := t.q(ctx).Exec(ctx,
		`INSERT INTO rm_carrier_transit_open (tracking_number, handoff_node_id, dest_node_id, handed_at) VALUES ($1, $2, $3, $4)
		ON CONFLICT (tracking_number) DO UPDATE SET handoff_node_id = EXCLUDED.handoff_node_id, dest_node_id = EXCLUDED.dest_node_id, handed_at = EXCLUDED.handed_at`,
		trackingNumber, handoffNodeID, "area:"+postalPrefix, handedAt); err != nil {
		return err
	}
	_, err := t.q(ctx).Exec(ctx, `DELETE FROM rm_carrier_transit_open WHERE handed_at < $1`, handedAt.AddDate(0, 0, -policy.MaxTransitWindowDays))
	return err
}

// RecordCarrierDelivered pairs the package's open carrier row into one
// observation of its (handoffNodeID, "area:"+prefix) pair and deletes the row;
// a package with no open row changes nothing (ADR 0026).
func (t *TransitActuals) RecordCarrierDelivered(ctx context.Context, trackingNumber string, deliveredAt time.Time) error {
	var origin, dest string
	var handedAt time.Time
	err := t.q(ctx).QueryRow(ctx, `SELECT handoff_node_id, dest_node_id, handed_at FROM rm_carrier_transit_open WHERE tracking_number = $1`, trackingNumber).Scan(&origin, &dest, &handedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := t.observe(ctx, origin, dest, handedAt, deliveredAt); err != nil {
		return err
	}
	_, err = t.q(ctx).Exec(ctx, `DELETE FROM rm_carrier_transit_open WHERE tracking_number = $1`, trackingNumber)
	return err
}

// MeasuredTransit answers the nearest-rank percentile transit of the pair's
// in-window observations; ok is false below minObservations.
func (t *TransitActuals) MeasuredTransit(ctx context.Context, origin, dest string, percentile, minObservations, windowDays int, now time.Time) (bool, int) {
	ring, err := t.ring(ctx, origin, dest)
	if err != nil {
		return false, 0
	}
	return readmodel.TransitPercentile(ring, percentile, minObservations, windowDays, now)
}

// ring reads the observations of one pair; a missing row is an empty ring.
func (t *TransitActuals) ring(ctx context.Context, origin, dest string) ([]readmodel.TransitObservation, error) {
	var ring []readmodel.TransitObservation
	if err := scanSnapshot(t.q(ctx).QueryRow(ctx,
		`SELECT observations FROM rm_transit_actuals WHERE origin_node_id = $1 AND dest_node_id = $2`, origin, dest), &ring); err != nil &&
		!errors.Is(err, ports.ErrNotFound) {
		return nil, err
	}
	return ring, nil
}
