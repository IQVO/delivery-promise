package analyticsstore

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// coverageSQL: the offers quoted in the range that carry a region (the first
// two characters of the destination prefix, ADR 0030), per UTC day, region and
// promised days (NULL: no promise). The top regions, the cumulative counts and
// the shares come from internal/analytics/report.
//
//	$1 from, $2 to
const coverageSQL = `
	SELECT (quoted_at AT TIME ZONE 'UTC')::date AS day, region, promised_days, count(*) AS n
	FROM offers_quoted
	WHERE quoted_at >= $1 AND quoted_at < $2 AND region <> ''
	GROUP BY 1, 2, 3
	ORDER BY day, region COLLATE "C", promised_days NULLS FIRST`

// PromiseCoverage implements report.Reader.
func (r *Reader) PromiseCoverage(ctx context.Context, rg report.Range) ([]report.CoverageBucket, error) {
	return collect(ctx, r.pool, coverageSQL, func(rows pgx.Rows) (report.CoverageBucket, error) {
		var b report.CoverageBucket
		err := rows.Scan(&b.Day, &b.Region, &b.PromisedDays, &b.Count)
		b.Day = b.Day.UTC()
		return b, err
	}, rg.From, rg.To)
}
