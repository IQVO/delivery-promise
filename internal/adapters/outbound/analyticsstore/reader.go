package analyticsstore

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// Reader is the Postgres READER (report.Reader). Every query filters the
// counted fact's instant with `>= from AND < to`, and days are UTC calendar
// days. SQL only counts, sums and takes percentile_cont; the vocabulary, the
// ranks and every derived figure (means, rates, shares) come from
// internal/analytics/report. Tiers and bases sort byte-wise (COLLATE "C"), the
// order of the in-memory twin.
type Reader struct {
	pool *pgxpool.Pool
}

// NewReader constructs a Reader over the (read-only) reports pool.
func NewReader(pool *pgxpool.Pool) *Reader { return &Reader{pool: pool} }

var _ report.Reader = (*Reader)(nil)

// reliabilitySQL: the judgements of the range with the tier and basis of their
// PromiseMade (UNKNOWN until it is projected), then per day, tier and basis the
// verdict and cause counts and the lateness percentiles of the LATE ones.
//
//	$1 from, $2 to, $3 unknown tier, $4 unknown basis, $5 KEPT, $6 BROKEN,
//	$7 LATE, $8 LOST, $9 RETURNED, $10 p50 rank, $11 p90 rank
const reliabilitySQL = `
	WITH judged AS (
		SELECT (j.judged_at AT TIME ZONE 'UTC')::date AS day,
		       COALESCE(m.tier, $3) AS tier, COALESCE(m.basis, $4) AS basis,
		       j.verdict, j.cause, j.late_by_minutes::float8 AS late_by
		FROM promise_judgements j
		LEFT JOIN promises_made m ON m.promise_id = j.promise_id
		WHERE j.judged_at >= $1 AND j.judged_at < $2
	)
	SELECT day, tier, basis,
	       count(*) FILTER (WHERE verdict = $5),
	       count(*) FILTER (WHERE verdict = $6),
	       count(*) FILTER (WHERE cause = $7),
	       count(*) FILTER (WHERE cause = $8),
	       count(*) FILTER (WHERE cause = $9),
	       percentile_cont($10::float8) WITHIN GROUP (ORDER BY late_by) FILTER (WHERE cause = $7),
	       percentile_cont($11::float8) WITHIN GROUP (ORDER BY late_by) FILTER (WHERE cause = $7)
	FROM judged
	GROUP BY day, tier, basis
	ORDER BY day, tier COLLATE "C", basis COLLATE "C"`

// PromiseReliability implements report.Reader.
func (r *Reader) PromiseReliability(ctx context.Context, rg report.Range) ([]report.ReliabilityDay, error) {
	return collect(ctx, r.pool, reliabilitySQL, func(rows pgx.Rows) (report.ReliabilityDay, error) {
		var d report.ReliabilityDay
		err := rows.Scan(&d.Day, &d.Tier, &d.Basis, &d.Kept, &d.Broken, &d.BrokenLate, &d.BrokenLost, &d.BrokenReturned,
			&d.LateByP50, &d.LateByP90)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To, report.UnknownTier(), report.UnknownBasis(), report.VerdictKept, report.VerdictBroken,
		report.CauseLate, report.CauseLost, report.CauseReturned, report.P50(), report.P90())
}

// precisionSQL: the promises made in the range with whether each was ever
// revised or became unkeepable, then per day and tier the counts, the sum and
// percentiles of the window widths, and the flag counts.
//
//	$1 from, $2 to, $3 p50 rank, $4 p90 rank
const precisionSQL = `
	WITH made AS (
		SELECT (m.made_at AT TIME ZONE 'UTC')::date AS day, m.tier, m.window_minutes, m.exact_day, m.zone_known,
		       EXISTS (SELECT 1 FROM promise_revisions r WHERE r.promise_id = m.promise_id) AS revised,
		       EXISTS (SELECT 1 FROM promise_unkeepable u WHERE u.promise_id = m.promise_id) AS unkeepable
		FROM promises_made m
		WHERE m.made_at >= $1 AND m.made_at < $2
	)
	SELECT day, tier, count(*), COALESCE(sum(window_minutes), 0)::float8,
	       percentile_cont($3::float8) WITHIN GROUP (ORDER BY window_minutes),
	       percentile_cont($4::float8) WITHIN GROUP (ORDER BY window_minutes),
	       count(*) FILTER (WHERE exact_day),
	       count(*) FILTER (WHERE NOT zone_known),
	       count(*) FILTER (WHERE revised),
	       count(*) FILTER (WHERE unkeepable)
	FROM made
	GROUP BY day, tier
	ORDER BY day, tier COLLATE "C"`

// PromisePrecision implements report.Reader.
func (r *Reader) PromisePrecision(ctx context.Context, rg report.Range) ([]report.PrecisionDay, error) {
	return collect(ctx, r.pool, precisionSQL, func(rows pgx.Rows) (report.PrecisionDay, error) {
		var d report.PrecisionDay
		err := rows.Scan(&d.Day, &d.Tier, &d.PromisesMade, &d.WindowMinutesSum, &d.WindowP50, &d.WindowP90,
			&d.ExactDay, &d.ZoneUnknown, &d.Revised, &d.Unkeepable)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To, report.P50(), report.P90())
}

// conversionSQL: the offers quoted and the promises made per day and tier of
// the range, joined so a day and tier with only one of them still appears.
//
//	$1 from, $2 to
const conversionSQL = `
	WITH offers AS (
		SELECT (quoted_at AT TIME ZONE 'UTC')::date AS day, tier, count(*) AS n
		FROM offers_quoted
		WHERE quoted_at >= $1 AND quoted_at < $2
		GROUP BY 1, 2
	), made AS (
		SELECT (made_at AT TIME ZONE 'UTC')::date AS day, tier, count(*) AS n
		FROM promises_made
		WHERE made_at >= $1 AND made_at < $2
		GROUP BY 1, 2
	)
	SELECT COALESCE(o.day, m.day), COALESCE(o.tier, m.tier), COALESCE(o.n, 0), COALESCE(m.n, 0)
	FROM offers o
	FULL OUTER JOIN made m ON m.day = o.day AND m.tier = o.tier
	ORDER BY COALESCE(o.day, m.day), COALESCE(o.tier, m.tier) COLLATE "C"`

// QuoteConversion implements report.Reader.
func (r *Reader) QuoteConversion(ctx context.Context, rg report.Range) ([]report.ConversionDay, error) {
	return collect(ctx, r.pool, conversionSQL, func(rows pgx.Rows) (report.ConversionDay, error) {
		var d report.ConversionDay
		err := rows.Scan(&d.Day, &d.Tier, &d.OffersQuoted, &d.PromisesMade)
		d.Day = d.Day.UTC()
		return d, err
	}, rg.From, rg.To)
}

// LastEventAt implements report.Reader: the newest CloudEvents time among the
// applied events, nil while none was applied.
func (r *Reader) LastEventAt(ctx context.Context) (*time.Time, error) {
	var at *time.Time
	if err := r.pool.QueryRow(ctx, `SELECT max(occurred_at) FROM analytics_processed_events`).Scan(&at); err != nil {
		return nil, fmt.Errorf("analyticsstore: last event: %w", err)
	}
	if at != nil {
		utc := at.UTC()
		at = &utc
	}
	return at, nil
}

// Ping checks the analytical database answers (the reports' /readyz).
func (r *Reader) Ping(ctx context.Context) error { return r.pool.Ping(ctx) }

// collect runs query with args and scans every row; the result is a non-nil
// slice even when empty.
func collect[T any](ctx context.Context, pool *pgxpool.Pool, query string, scan func(pgx.Rows) (T, error), args ...any) ([]T, error) {
	rows, err := pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("analyticsstore: query: %w", err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("analyticsstore: scan: %w", err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("analyticsstore: rows: %w", err)
	}
	return out, nil
}
