package analyticsstore

import (
	"context"
	"sort"
	"time"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// memRequest is one request fact (ADR 0031), as requests_answered keeps it.
type memRequest struct {
	at       time.Time
	region   string
	answered bool
	days     *int // shortest promised days, nil without a promise
}

// applyRequest keeps the first fact of a request id, as the Postgres store's
// ON CONFLICT DO NOTHING does.
func (m *MemoryStore) applyRequest(e report.Event) {
	if _, seen := m.requests[e.RequestID]; seen {
		return
	}
	m.requests[e.RequestID] = memRequest{at: e.At.UTC(), region: e.Region, answered: e.Answered, days: e.ShortestDays}
}

// requestKey groups the request facts of the coverage report: UTC day, region,
// whether a promise was answered and the shortest promised days (-1: none).
type requestKey struct {
	day      int64
	region   string
	answered bool
	days     int
}

// RequestCoverage implements report.Reader: the request facts answered in the
// range, counted per UTC day, region, answered flag and shortest promised
// days (ADR 0031), ordered as the Postgres reader.
func (m *MemoryStore) RequestCoverage(_ context.Context, r report.Range) ([]report.RequestBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	counts := map[requestKey]int{}
	for _, q := range m.requests {
		if !inRange(q.at, r) {
			continue
		}
		k := requestKey{day: utcDay(q.at).Unix(), region: q.region, answered: q.answered, days: -1}
		if q.days != nil {
			k.days = *q.days
		}
		counts[k]++
	}
	keys := make([]requestKey, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return lessRequestKey(keys[i], keys[j]) })
	out := make([]report.RequestBucket, 0, len(keys))
	for _, k := range keys {
		b := report.RequestBucket{Day: time.Unix(k.day, 0).UTC(), Region: k.region, Answered: k.answered, Count: counts[k]}
		if k.days >= 0 {
			days := k.days
			b.ShortestDays = &days
		}
		out = append(out, b)
	}
	return out, nil
}

// lessRequestKey is the order of the Postgres query: day, region (byte order),
// not answered before answered, no promised days before some.
func lessRequestKey(a, b requestKey) bool {
	if a.day != b.day {
		return a.day < b.day
	}
	if a.region != b.region {
		return a.region < b.region
	}
	if a.answered != b.answered {
		return !a.answered
	}
	return a.days < b.days
}
