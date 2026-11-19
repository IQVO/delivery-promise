package report

import (
	"cmp"
	"maps"
	"slices"
	"time"
)

// The promise coverage rules of ADR 0030: per UTC day and region, how many of
// the offers quoted carry a promise of N days or less. Like the other rules
// they are functions, so mutation testing reaches them.

// CoverageDays is the documented list of N: the report counts the offers whose
// promised days are within each of them (cumulative).
func CoverageDays() []int { return []int{1, 2, 3, 5} }

// TopRegions is how many regions a day keeps; the rest fold into OtherRegion.
func TopRegions() int { return 10 }

// OtherRegion is the region of the row that folds every region past TopRegions.
// A real region has at most regionWidth characters, so it cannot collide.
func OtherRegion() string { return "other" }

// regionWidth is how many leading characters of the destination postal prefix
// are kept: the report never holds more of the destination.
func regionWidth() int { return 2 }

// RegionOf is the region of a destination postal prefix: its first two
// characters (all of it when shorter).
func RegionOf(prefix string) string {
	r := []rune(prefix)
	return string(r[:min(regionWidth(), len(r))])
}

// secondsPerDay converts a Unix instant to a day number.
func secondsPerDay() int64 { return 24 * 60 * 60 }

// calendarDay is the number of the calendar day t falls on in loc, counted from
// the Unix epoch day.
func calendarDay(t time.Time, loc *time.Location) int64 {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / secondsPerDay()
}

// PromisedDays is the number of local calendar days from the day of the quote
// to the day of the latest delivery, both read in the window's zone (UTC when
// the zone is absent or cannot be loaded, as WindowOf), never below 0. nil when
// the offer carries no latest delivery: it is not an answer with a promise.
func PromisedDays(quotedAt, latest time.Time, zone string) *int {
	if latest.IsZero() {
		return nil
	}
	loc, _ := locationOf(zone)
	days := int(max(0, calendarDay(latest, loc)-calendarDay(quotedAt, loc)))
	return &days
}

// CoverageBucket is what the store counts for one UTC day, one region (never
// empty) and one number of promised days (nil: no promise): the offers quoted.
type CoverageBucket struct {
	Day          time.Time
	Region       string
	PromisedDays *int
	Count        int
}

// Within is the cumulative count of the offers promised within Days days and
// its share of the region's requests (nil when it has none).
type Within struct {
	Days  int
	Count int
	Share *float64
}

// RegionCoverage is one region of one day: the offers quoted (Requests), those
// that carry a promise, and the cumulative counts per CoverageDays.
type RegionCoverage struct {
	Region              string
	Requests            int
	AnsweredWithPromise int
	Within              []Within
}

// CoverageDay is the coverage of one UTC day: the TopRegions regions with most
// requests (ties by region in byte order), then OtherRegion when any region was
// folded.
type CoverageDay struct {
	Day     time.Time
	Regions []RegionCoverage
}

// tally accumulates the counts of one region.
type tally struct {
	region   string
	requests int
	answered int
	within   []int
}

func newTally(region string) *tally {
	return &tally{region: region, within: make([]int, len(CoverageDays()))}
}

// add counts n offers promised days (nil: no promise).
func (t *tally) add(days *int, n int) {
	t.requests += n
	if days == nil {
		return
	}
	t.answered += n
	for i, limit := range CoverageDays() {
		if *days <= limit {
			t.within[i] += n
		}
	}
}

// merge adds every count of o.
func (t *tally) merge(o *tally) {
	t.requests += o.requests
	t.answered += o.answered
	for i, n := range o.within {
		t.within[i] += n
	}
}

// row is the tally as a RegionCoverage, with the shares.
func (t *tally) row() RegionCoverage {
	within := make([]Within, 0, len(t.within))
	for i, limit := range CoverageDays() {
		within = append(within, Within{Days: limit, Count: t.within[i], Share: RateOrNil(t.within[i], t.requests)})
	}
	return RegionCoverage{Region: t.region, Requests: t.requests, AnsweredWithPromise: t.answered, Within: within}
}

// PromiseCoverage derives the report of ADR 0030 from the store's buckets: per
// day (ascending) the top regions by requests, the rest folded into
// OtherRegion, each with its cumulative counts and shares. A nil input yields
// an empty, non-nil slice.
func PromiseCoverage(buckets []CoverageBucket) []CoverageDay {
	byDay := map[int64]map[string]*tally{} // keyed by the Unix instant of the day: time.Time is not a safe map key
	for _, b := range buckets {
		key := b.Day.Unix()
		if byDay[key] == nil {
			byDay[key] = map[string]*tally{}
		}
		t := byDay[key][b.Region]
		if t == nil {
			t = newTally(b.Region)
			byDay[key][b.Region] = t
		}
		t.add(b.PromisedDays, b.Count)
	}
	out := make([]CoverageDay, 0)
	for _, key := range slices.Sorted(maps.Keys(byDay)) {
		out = append(out, CoverageDay{Day: time.Unix(key, 0).UTC(), Regions: rankRegions(byDay[key])})
	}
	return out
}

// rankRegions orders the tallies of one day by requests (most first, ties by
// region), keeps TopRegions and folds the remainder into OtherRegion.
func rankRegions(tallies map[string]*tally) []RegionCoverage {
	ranked := slices.Collect(maps.Values(tallies))
	slices.SortFunc(ranked, func(a, b *tally) int {
		return cmp.Or(cmp.Compare(b.requests, a.requests), cmp.Compare(a.region, b.region))
	})
	keep := min(TopRegions(), len(ranked))
	out := make([]RegionCoverage, 0)
	for _, t := range ranked[:keep] {
		out = append(out, t.row())
	}
	if rest := ranked[keep:]; len(rest) > 0 {
		other := newTally(OtherRegion())
		for _, t := range rest {
			other.merge(t)
		}
		out = append(out, other.row())
	}
	return out
}
