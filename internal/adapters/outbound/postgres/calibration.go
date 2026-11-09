package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/domain/policy"
	"github.com/claudioed/delivery-promise/internal/domain/search"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// PromiseCalibrationView is the Postgres ports.PromiseCalibrationView over the
// rm_promise_calibration table (ADR 0020).
type PromiseCalibrationView struct{ db }

// NewPromiseCalibrationView constructs PromiseCalibrationView over pool.
func NewPromiseCalibrationView(pool *pgxpool.Pool) *PromiseCalibrationView {
	return &PromiseCalibrationView{db{pool}}
}

// RecordJudgement appends one observation to the segment's ring. The row is
// created empty if missing, then locked, so two judgements of one segment in
// two transactions append one after the other instead of overwriting each other.
func (c *PromiseCalibrationView) RecordJudgement(ctx context.Context, key readmodel.CalibrationKey, obs readmodel.CalibrationObservation) error {
	if _, err := c.q(ctx).Exec(ctx,
		`INSERT INTO rm_promise_calibration (tier, basis, node_grade, observations) VALUES ($1, $2, $3, '[]'::jsonb)
		ON CONFLICT (tier, basis, node_grade) DO NOTHING`, string(key.Tier), string(key.Basis), string(key.NodeGrade)); err != nil {
		return err
	}
	var ring []readmodel.CalibrationObservation
	if err := scanSnapshot(c.q(ctx).QueryRow(ctx,
		`SELECT observations FROM rm_promise_calibration WHERE tier = $1 AND basis = $2 AND node_grade = $3 FOR UPDATE`,
		string(key.Tier), string(key.Basis), string(key.NodeGrade)), &ring); err != nil && !errors.Is(err, ports.ErrNotFound) {
		return err
	}
	obs.ObservedAt = obs.ObservedAt.UTC()
	ring = readmodel.AppendCalibrationObservation(ring, obs, policy.MaxCalibrationWindowDays, obs.ObservedAt)
	_, err := c.q(ctx).Exec(ctx,
		`UPDATE rm_promise_calibration SET observations = $4 WHERE tier = $1 AND basis = $2 AND node_grade = $3`,
		string(key.Tier), string(key.Basis), string(key.NodeGrade), mustJSON(ring))
	return err
}

// Segments returns the in-window lateBy minutes of every segment that has any.
func (c *PromiseCalibrationView) Segments(ctx context.Context, windowDays int, now time.Time) (map[readmodel.CalibrationKey][]int, error) {
	rows, err := c.q(ctx).Query(ctx, `SELECT tier, basis, node_grade, observations FROM rm_promise_calibration`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[readmodel.CalibrationKey][]int{}
	for rows.Next() {
		var tier, basis, grade string
		var raw []byte
		if err := rows.Scan(&tier, &basis, &grade, &raw); err != nil {
			return nil, err
		}
		var ring []readmodel.CalibrationObservation
		if err := json.Unmarshal(raw, &ring); err != nil {
			return nil, err
		}
		if lateBy := readmodel.CalibrationLateBy(ring, windowDays, now); len(lateBy) > 0 {
			out[readmodel.CalibrationKey{Tier: shared.Tier(tier), Basis: shared.Basis(basis), NodeGrade: search.Grade(grade)}] = lateBy
		}
	}
	return out, rows.Err()
}
