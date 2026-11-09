package analyticsstore

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// MemoryStore is the in-memory twin of Projection and Reader, for unit tests
// of the consumer and the reports. contract_test.go runs the SAME suite
// against it and against Postgres, so its behaviour cannot drift from the real
// store. The shipped binaries never construct it.
type MemoryStore struct {
	mu         sync.Mutex
	processed  map[string]time.Time
	offers     map[string]memOffer
	made       map[string]memMade
	revisions  map[memRevision]bool
	unkeepable map[string]bool
	judgements map[string]memJudgement
	// Fail, when set, is returned by the next Apply (and cleared), to drive
	// the consumer's transient and rejection paths.
	Fail error
}

type memOffer struct {
	tier string
	at   time.Time
}

type memMade struct {
	tier, basis string
	at          time.Time
	window      report.Window
}

type memRevision struct {
	promise string
	no      int
}

type memJudgement struct {
	verdict, cause string
	lateBy         int
	at             time.Time
}

// NewMemoryStore returns an empty twin.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{processed: map[string]time.Time{}, offers: map[string]memOffer{}, made: map[string]memMade{},
		revisions: map[memRevision]bool{}, unkeepable: map[string]bool{}, judgements: map[string]memJudgement{}}
}

var (
	_ report.Projection = (*MemoryStore)(nil)
	_ report.Reader     = (*MemoryStore)(nil)
)

// Apply implements report.Projection with the Postgres store's semantics: an
// event the schema's CHECKs would refuse is rejected before anything is
// recorded, a replay is a no-op, offers, promises and judgements are upserted,
// revisions and unkeepability keep their first fact.
func (m *MemoryStore) Apply(_ context.Context, e report.Event) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Fail != nil {
		err := m.Fail
		m.Fail = nil
		return false, err
	}
	if err := e.Validate(); err != nil {
		return false, fmt.Errorf("%w: %w", report.ErrRejected, err)
	}
	if _, seen := m.processed[e.EventID]; seen {
		return false, nil
	}
	m.processed[e.EventID] = e.At.UTC()
	at := e.At.UTC()
	switch e.Kind {
	case report.KindDeliveryOfferQuoted:
		m.offers[e.OfferID] = memOffer{tier: e.Tier, at: at}
	case report.KindPromiseMade:
		m.made[e.PromiseID] = memMade{tier: e.Tier, basis: e.Basis, at: at,
			window: report.WindowOf(e.DeliverByEarliest, e.DeliverByLatest, e.TimeZone)}
	case report.KindPromiseRevised:
		m.revisions[memRevision{e.PromiseID, e.RevisionNo}] = true
	case report.KindPromiseUnkeepable:
		m.unkeepable[e.PromiseID] = true
	default:
		verdict, _ := report.VerdictOf(e.Kind) // Validate admitted only the verdict kinds here
		m.judgements[e.PromiseID] = memJudgement{verdict: verdict, cause: e.Cause, lateBy: e.LateByMinutes, at: at}
	}
	return true, nil
}

func utcDay(t time.Time) time.Time { return t.UTC().Truncate(24 * time.Hour) }

func inRange(t time.Time, r report.Range) bool { return t.Compare(r.From) >= 0 && t.Before(r.To) }

// sortedKeys is the keys of a map in byte order, for a deterministic
// aggregation order.
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// dayKey groups the rows of a report: day, tier and (reliability only) basis.
type dayKey struct {
	day         time.Time
	tier, basis string
}

func lessKey(a, b dayKey) bool {
	if !a.day.Equal(b.day) {
		return a.day.Before(b.day)
	}
	if a.tier != b.tier {
		return a.tier < b.tier
	}
	return a.basis < b.basis
}

// PromiseReliability implements report.Reader.
func (m *MemoryStore) PromiseReliability(_ context.Context, r report.Range) ([]report.ReliabilityDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	days := map[dayKey]*report.ReliabilityDay{}
	lateness := map[dayKey][]float64{}
	for _, id := range sortedKeys(m.judgements) {
		j := m.judgements[id]
		if !inRange(j.at, r) {
			continue
		}
		k := dayKey{day: utcDay(j.at), tier: report.UnknownTier(), basis: report.UnknownBasis()}
		if made, ok := m.made[id]; ok {
			k.tier, k.basis = made.tier, made.basis
		}
		d := days[k]
		if d == nil {
			d = &report.ReliabilityDay{Day: k.day, Tier: k.tier, Basis: k.basis}
			days[k] = d
		}
		countJudgement(d, j)
		if j.cause == report.CauseLate {
			lateness[k] = append(lateness[k], float64(j.lateBy))
		}
	}
	out := make([]report.ReliabilityDay, 0, len(days))
	for k, d := range days {
		d.LateByP50 = report.PercentileOrNil(lateness[k], report.P50())
		d.LateByP90 = report.PercentileOrNil(lateness[k], report.P90())
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		return lessKey(dayKey{out[i].Day, out[i].Tier, out[i].Basis}, dayKey{out[j].Day, out[j].Tier, out[j].Basis})
	})
	return out, nil
}

// countJudgement adds one judgement to its day.
func countJudgement(d *report.ReliabilityDay, j memJudgement) {
	if j.verdict == report.VerdictKept {
		d.Kept++
		return
	}
	d.Broken++
	switch j.cause {
	case report.CauseLate:
		d.BrokenLate++
	case report.CauseLost:
		d.BrokenLost++
	default:
		d.BrokenReturned++
	}
}

// promiseRevised reports whether id has at least one revision.
func (m *MemoryStore) promiseRevised(id string) bool {
	for rev := range m.revisions {
		if rev.promise == id {
			return true
		}
	}
	return false
}

// PromisePrecision implements report.Reader.
func (m *MemoryStore) PromisePrecision(_ context.Context, r report.Range) ([]report.PrecisionDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	days := map[dayKey]*report.PrecisionDay{}
	widths := map[dayKey][]float64{}
	for _, id := range sortedKeys(m.made) {
		p := m.made[id]
		if !inRange(p.at, r) {
			continue
		}
		k := dayKey{day: utcDay(p.at), tier: p.tier}
		d := days[k]
		if d == nil {
			d = &report.PrecisionDay{Day: k.day, Tier: k.tier}
			days[k] = d
		}
		d.PromisesMade++
		d.WindowMinutesSum += p.window.Minutes
		widths[k] = append(widths[k], p.window.Minutes)
		d.ExactDay += count(p.window.ExactDay)
		d.ZoneUnknown += count(!p.window.ZoneKnown)
		d.Revised += count(m.promiseRevised(id))
		d.Unkeepable += count(m.unkeepable[id])
	}
	out := make([]report.PrecisionDay, 0, len(days))
	for k, d := range days {
		d.WindowP50 = report.Percentile(widths[k], report.P50())
		d.WindowP90 = report.Percentile(widths[k], report.P90())
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		return lessKey(dayKey{out[i].Day, out[i].Tier, ""}, dayKey{out[j].Day, out[j].Tier, ""})
	})
	return out, nil
}

// count is 1 for true, 0 for false.
func count(b bool) int {
	if b {
		return 1
	}
	return 0
}

// QuoteConversion implements report.Reader.
func (m *MemoryStore) QuoteConversion(_ context.Context, r report.Range) ([]report.ConversionDay, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	days := map[dayKey]*report.ConversionDay{}
	row := func(at time.Time, tier string) *report.ConversionDay {
		k := dayKey{day: utcDay(at), tier: tier}
		if days[k] == nil {
			days[k] = &report.ConversionDay{Day: k.day, Tier: tier}
		}
		return days[k]
	}
	for _, o := range m.offers {
		if inRange(o.at, r) {
			row(o.at, o.tier).OffersQuoted++
		}
	}
	for _, p := range m.made {
		if inRange(p.at, r) {
			row(p.at, p.tier).PromisesMade++
		}
	}
	out := make([]report.ConversionDay, 0, len(days))
	for _, d := range days {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		return lessKey(dayKey{out[i].Day, out[i].Tier, ""}, dayKey{out[j].Day, out[j].Tier, ""})
	})
	return out, nil
}

// LastEventAt implements report.Reader.
func (m *MemoryStore) LastEventAt(context.Context) (*time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var last *time.Time
	for _, at := range m.processed {
		if last == nil || at.After(*last) {
			a := at
			last = &a
		}
	}
	return last, nil
}

// Ping is the twin's readiness check: always ready.
func (m *MemoryStore) Ping(context.Context) error { return nil }
