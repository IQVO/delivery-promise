package postgres

import (
	"context"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
)

// ShipmentViews is the Postgres ports.ShipmentViews.
type ShipmentViews struct{ db }

// NewShipmentViews constructs ShipmentViews over pool.
func NewShipmentViews(pool *pgxpool.Pool) *ShipmentViews { return &ShipmentViews{db{pool}} }

// GetAssignment returns the assignment or ports.ErrNotFound.
func (v *ShipmentViews) GetAssignment(ctx context.Context, id string) (readmodel.Assignment, error) {
	return getJSON[readmodel.Assignment](ctx, v.q(ctx), `SELECT data FROM rm_assignments WHERE id = $1`, id)
}

// PutAssignment stores the assignment.
func (v *ShipmentViews) PutAssignment(ctx context.Context, a readmodel.Assignment) error {
	_, err := v.q(ctx).Exec(ctx, `INSERT INTO rm_assignments (id, promise_id, data) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET promise_id = EXCLUDED.promise_id, data = EXCLUDED.data`, a.ID, a.PromiseID, mustJSON(a))
	return err
}

// FindAssignmentByPromise returns the assignment of the promise.
func (v *ShipmentViews) FindAssignmentByPromise(ctx context.Context, promiseID string) (readmodel.Assignment, error) {
	return getJSON[readmodel.Assignment](ctx, v.q(ctx), `SELECT data FROM rm_assignments WHERE promise_id = $1 ORDER BY id LIMIT 1`, promiseID)
}

// PutShipment records the tracking numbers of an assignment.
func (v *ShipmentViews) PutShipment(ctx context.Context, assignmentID string, trackingNumbers []string) error {
	if trackingNumbers == nil {
		trackingNumbers = []string{}
	}
	q := v.q(ctx)
	if _, err := q.Exec(ctx, `INSERT INTO rm_shipments (assignment_id, tracking_numbers) VALUES ($1, $2)
		ON CONFLICT (assignment_id) DO UPDATE SET tracking_numbers = EXCLUDED.tracking_numbers`, assignmentID, trackingNumbers); err != nil {
		return err
	}
	for _, t := range trackingNumbers {
		if _, err := q.Exec(ctx, `INSERT INTO rm_trackings (tracking_number, assignment_id) VALUES ($1, $2)
			ON CONFLICT (tracking_number, assignment_id) DO NOTHING`, t, assignmentID); err != nil {
			return err
		}
	}
	return nil
}

// Shipment returns the assignment's tracking numbers, sorted.
func (v *ShipmentViews) Shipment(ctx context.Context, assignmentID string) ([]string, error) {
	var out []string
	err := v.q(ctx).QueryRow(ctx, `SELECT tracking_numbers FROM rm_shipments WHERE assignment_id = $1`, assignmentID).Scan(&out)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	slices.Sort(out)
	return out, err
}

// AssignmentsOf returns every assignment a tracking number was shipped under,
// sorted ascending, or ports.ErrNotFound when there is none.
func (v *ShipmentViews) AssignmentsOf(ctx context.Context, trackingNumber string) ([]string, error) {
	rows, err := v.q(ctx).Query(ctx, `SELECT assignment_id FROM rm_trackings WHERE tracking_number = $1 ORDER BY assignment_id`, trackingNumber)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, ports.ErrNotFound
	}
	return out, nil
}

// PutOutcome records the first final fact of a package; a later one is ignored.
func (v *ShipmentViews) PutOutcome(ctx context.Context, o readmodel.PackageOutcome) error {
	_, err := v.q(ctx).Exec(ctx, `INSERT INTO rm_outcomes (tracking_number, data) VALUES ($1, $2) ON CONFLICT (tracking_number) DO NOTHING`, o.TrackingNumber, mustJSON(o))
	return err
}

// Outcomes returns the recorded outcomes of the tracking numbers, by tracking number.
func (v *ShipmentViews) Outcomes(ctx context.Context, trackingNumbers []string) ([]readmodel.PackageOutcome, error) {
	return collect[readmodel.PackageOutcome](v.q(ctx).Query(ctx, `SELECT data FROM rm_outcomes WHERE tracking_number = ANY ($1) ORDER BY tracking_number`, trackingNumbers))
}
