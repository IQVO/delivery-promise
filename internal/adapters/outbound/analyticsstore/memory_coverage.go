package analyticsstore

import (
	"context"
	"sort"
	"time"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// coverageKey groups the offers of the coverage report: UTC day, region and
// promised days (-1: no promise).
type coverageKey struct {
	day    int64
	region string
	days   int
}

// PromiseCoverage implements report.Reader: the offers quoted in the range that
// carry a region, counted per UTC day, region and promised days (ADR 0030),
// ordered as the Postgres reader.
func (m *MemoryStore) PromiseCoverage(_ context.Context, r report.Range) ([]report.CoverageBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := map[coverageKey]int{}
	for _, o := range m.offers {
		if o.region == "" || !inRange(o.at, r) {
			continue
		}
		k := coverageKey{day: utcDay(o.at).Unix(), region: o.region, days: -1}
		if o.days != nil {
			k.days = *o.days
		}
		counts[k]++
	}
	keys := make([]coverageKey, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		if a.day != b.day {
			return a.day < b.day
		}
		if a.region != b.region {
			return a.region < b.region
		}
		return a.days < b.days
	})
	out := make([]report.CoverageBucket, 0, len(keys))
	for _, k := range keys {
		b := report.CoverageBucket{Day: time.Unix(k.day, 0).UTC(), Region: k.region, Count: counts[k]}
		if k.days >= 0 {
			days := k.days
			b.PromisedDays = &days
		}
		out = append(out, b)
	}
	return out, nil
}
