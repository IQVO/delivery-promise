package analyticsstore

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// requestCoverageSQL: the request facts answered in the range (ADR 0031), per
// UTC day, region, answered flag and shortest promised days (NULL: none). The
// top regions, the cumulative counts and the shares come from
// internal/analytics/report.
//
//	$1 from, $2 to
const requestCoverageSQL = `
	SELECT (answered_at AT TIME ZONE 'UTC')::date AS day, region, answered_with_promise, shortest_promised_days, count(*) AS n
	FROM requests_answered
	WHERE answered_at >= $1 AND answered_at < $2
	GROUP BY 1, 2, 3, 4
	ORDER BY day, region COLLATE "C", answered_with_promise, shortest_promised_days NULLS FIRST`

// RequestCoverage implements report.Reader.
func (r *Reader) RequestCoverage(ctx context.Context, rg report.Range) ([]report.RequestBucket, error) {
	return collect(ctx, r.pool, requestCoverageSQL, func(rows pgx.Rows) (report.RequestBucket, error) {
		var b report.RequestBucket
		err := rows.Scan(&b.Day, &b.Region, &b.Answered, &b.ShortestDays, &b.Count)
		b.Day = b.Day.UTC()
		return b, err
	}, rg.From, rg.To)
}
