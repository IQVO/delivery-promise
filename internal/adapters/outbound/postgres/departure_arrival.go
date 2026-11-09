package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/delivery-promise/internal/domain/policy"
)

// DepartureArrivals is the Postgres ports.DepartureArrivals over the
// rm_departure_arrival table (ADR 0021).
type DepartureArrivals struct{ db }

// NewDepartureArrivals constructs DepartureArrivals over pool.
func NewDepartureArrivals(pool *pgxpool.Pool) *DepartureArrivals {
	return &DepartureArrivals{db{pool}}
}

// RecordDepartureArrived stores the arrival of the departure (the first one
// seen is kept) and prunes the rows older than the longest transit window.
func (d *DepartureArrivals) RecordDepartureArrived(ctx context.Context, departureID string, arrivedAt time.Time) error {
	if _, err := d.q(ctx).Exec(ctx,
		`INSERT INTO rm_departure_arrival (departure_id, arrived_at) VALUES ($1, $2) ON CONFLICT (departure_id) DO NOTHING`,
		departureID, arrivedAt.UTC()); err != nil {
		return err
	}
	_, err := d.q(ctx).Exec(ctx, `DELETE FROM rm_departure_arrival WHERE arrived_at < $1`, arrivedAt.AddDate(0, 0, -policy.MaxTransitWindowDays).UTC())
	return err
}

// DepartureArrived reports whether an arrival row exists for the departure.
func (d *DepartureArrivals) DepartureArrived(ctx context.Context, departureID string) (bool, error) {
	var found bool
	err := d.q(ctx).QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM rm_departure_arrival WHERE departure_id = $1)`, departureID).Scan(&found)
	return found, err
}
