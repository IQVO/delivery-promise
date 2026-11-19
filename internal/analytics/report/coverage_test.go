package report_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

func TestCoverageConstants(t *testing.T) {
	if got, want := report.CoverageDays(), []int{1, 2, 3, 5}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CoverageDays = %v, want %v", got, want)
	}
	if report.TopRegions() != 10 || report.OtherRegion() != "other" {
		t.Fatalf("TopRegions, OtherRegion = %d, %q; want 10, other", report.TopRegions(), report.OtherRegion())
	}
}

func TestRegionOfKeepsTheFirstTwoCharactersOnly(t *testing.T) {
	for in, want := range map[string]string{"": "", "7": "7", "78": "78", "787": "78", "78701": "78", "SW1A": "SW", "é1x": "é1"} {
		if got := report.RegionOf(in); got != want {
			t.Errorf("RegionOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPromisedDaysCountsLocalCalendarDays(t *testing.T) {
	// 19:05Z on 10-22 is 14:05 on 10-22 in Chicago (CDT).
	quoted := time.Date(2026, 10, 22, 19, 5, 0, 0, time.UTC)
	at := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, time.UTC) }
	cases := []struct {
		name   string
		latest time.Time
		zone   string
		want   int
	}{
		{"next day in the station's zone (02:00Z is 21:00 the evening before)", at(24, 2), "America/Chicago", 1},
		{"the same UTC day is two local days apart in UTC", at(24, 2), "", 2},
		{"same local day", at(23, 2), "America/Chicago", 0},
		{"a zone that cannot be loaded reads UTC", at(24, 2), "Mars/Olympus", 2},
		{"Local is never a station zone", at(24, 2), "Local", 2},
		{"five days out", at(27, 18), "America/Chicago", 5},
		{"a latest before the quote day is never negative", at(20, 12), "America/Chicago", 0},
		{"the day before is not negative either", at(21, 23), "", 0},
	}
	for _, c := range cases {
		got := report.PromisedDays(quoted, c.latest, c.zone)
		if got == nil || *got != c.want {
			t.Errorf("%s: PromisedDays = %v, want %d", c.name, got, c.want)
		}
	}
	if got := report.PromisedDays(quoted, time.Time{}, "America/Chicago"); got != nil {
		t.Errorf("no latest delivery: PromisedDays = %d, want nil", *got)
	}
}

var cov = time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)

func days(n int) *int { return &n }

func bucket(day time.Time, region string, promised *int, count int) report.CoverageBucket {
	return report.CoverageBucket{Day: day, Region: region, PromisedDays: promised, Count: count}
}

func counts(r report.RegionCoverage) []int {
	out := []int{}
	for _, w := range r.Within {
		out = append(out, w.Count)
	}
	return out
}

func TestPromiseCoverageIsCumulativeOverTheDocumentedDays(t *testing.T) {
	got := report.PromiseCoverage([]report.CoverageBucket{
		bucket(cov, "78", days(0), 1), bucket(cov, "78", days(1), 1), bucket(cov, "78", days(2), 1),
		bucket(cov, "78", days(3), 1), bucket(cov, "78", days(4), 1), bucket(cov, "78", days(5), 1),
		bucket(cov, "78", days(6), 1), bucket(cov, "78", nil, 2),
	})
	if len(got) != 1 || !got[0].Day.Equal(cov) || len(got[0].Regions) != 1 {
		t.Fatalf("coverage = %+v, want one day with one region", got)
	}
	r := got[0].Regions[0]
	if r.Region != "78" || r.Requests != 9 || r.AnsweredWithPromise != 7 {
		t.Fatalf("region = %+v, want 78: 9 requests, 7 answered", r)
	}
	// within 1: {0,1}, 2: +{2}, 3: +{3}, 5: +{4,5}; 6 and no promise never count.
	if want := []int{2, 3, 4, 6}; !reflect.DeepEqual(counts(r), want) {
		t.Fatalf("cumulative counts = %v, want %v", counts(r), want)
	}
	for i, n := range report.CoverageDays() {
		w := r.Within[i]
		if w.Days != n || w.Share == nil || *w.Share != float64(counts(r)[i])/9 {
			t.Errorf("within[%d] = %+v, want days %d share %d/9", i, w, n, counts(r)[i])
		}
	}
}

func TestPromiseCoverageMergesBucketsOfOneRegionAndDay(t *testing.T) {
	got := report.PromiseCoverage([]report.CoverageBucket{
		bucket(cov, "78", days(1), 3), bucket(cov, "78", days(1), 2), bucket(cov, "10", nil, 4),
	})
	r := got[0].Regions
	if len(r) != 2 || r[0].Region != "78" || r[0].Requests != 5 || counts(r[0])[0] != 5 {
		t.Fatalf("regions = %+v, want 78 with 5 requests all within 1 day first", r)
	}
	if r[1].Region != "10" || r[1].Requests != 4 || r[1].AnsweredWithPromise != 0 || !reflect.DeepEqual(counts(r[1]), []int{0, 0, 0, 0}) {
		t.Fatalf("region 10 = %+v, want 4 requests and no promise", r[1])
	}
	if s := r[1].Within[0].Share; s == nil || *s != 0 {
		t.Fatalf("share of 0 within 4 requests = %v, want 0 (not null)", s)
	}
}

func TestPromiseCoverageRanksByRequestsThenRegion(t *testing.T) {
	got := report.PromiseCoverage([]report.CoverageBucket{
		bucket(cov, "B1", days(1), 2), bucket(cov, "A1", days(1), 2), bucket(cov, "Z9", days(1), 5), bucket(cov, "C1", days(1), 1),
	})
	var order []string
	for _, r := range got[0].Regions {
		order = append(order, r.Region)
	}
	if want := []string{"Z9", "A1", "B1", "C1"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

func regionBuckets(day time.Time, n int, requests func(i int) int) []report.CoverageBucket {
	var out []report.CoverageBucket
	for i := range n {
		out = append(out, bucket(day, fmt.Sprintf("%02d", i), days(1), requests(i)))
	}
	return out
}

func TestPromiseCoverageFoldsRegionsPastTheTenthIntoOther(t *testing.T) {
	// 12 regions 00..11 with 12,11,...,1 requests: 10 and 11 (2 and 1) fold.
	got := report.PromiseCoverage(regionBuckets(cov, 12, func(i int) int { return 12 - i }))
	regions := got[0].Regions
	if len(regions) != 11 || regions[9].Region != "09" || regions[10].Region != "other" {
		t.Fatalf("regions = %+v, want ten regions then other", regions)
	}
	other := regions[10]
	if other.Requests != 3 || other.AnsweredWithPromise != 3 || !reflect.DeepEqual(counts(other), []int{3, 3, 3, 3}) {
		t.Fatalf("other = %+v, want the folded 2+1 requests", other)
	}
	if s := other.Within[0].Share; s == nil || *s != 1 {
		t.Fatalf("other share = %v, want 1", s)
	}
}

func TestPromiseCoverageHasNoOtherRowForTenRegionsOrFewer(t *testing.T) {
	for _, n := range []int{1, 9, 10} {
		regions := report.PromiseCoverage(regionBuckets(cov, n, func(int) int { return 1 }))[0].Regions
		if len(regions) != n {
			t.Errorf("%d regions -> %d rows %+v, want %d and no other", n, len(regions), regions, n)
		}
		for _, r := range regions {
			if r.Region == report.OtherRegion() {
				t.Errorf("%d regions produced an other row", n)
			}
		}
	}
}

func TestPromiseCoverageBoundsEachDayOnItsOwn(t *testing.T) {
	next := cov.Add(24 * time.Hour)
	in := append(regionBuckets(next, 11, func(int) int { return 1 }), regionBuckets(cov, 3, func(int) int { return 1 })...)
	got := report.PromiseCoverage(in)
	if len(got) != 2 || !got[0].Day.Equal(cov) || !got[1].Day.Equal(next) {
		t.Fatalf("days = %+v, want ascending %v then %v", got, cov, next)
	}
	if len(got[0].Regions) != 3 || len(got[1].Regions) != 11 || got[1].Regions[10].Region != "other" {
		t.Fatalf("regions per day = %d, %d; want 3 and 11 (ten + other)", len(got[0].Regions), len(got[1].Regions))
	}
}

func TestPromiseCoverageIgnoresTheLocationOfTheDay(t *testing.T) {
	loc := time.FixedZone("x", 3600)
	a := cov.In(loc)
	got := report.PromiseCoverage([]report.CoverageBucket{bucket(cov, "78", days(1), 1), bucket(a, "78", days(1), 1)})
	if len(got) != 1 || got[0].Regions[0].Requests != 2 || got[0].Day.Location() != time.UTC {
		t.Fatalf("coverage = %+v, want one UTC day with 2 requests", got)
	}
}

func TestPromiseCoverageOfNothingIsEmptyNonNil(t *testing.T) {
	for _, in := range [][]report.CoverageBucket{nil, {}} {
		if got := report.PromiseCoverage(in); got == nil || len(got) != 0 {
			t.Fatalf("PromiseCoverage(%v) = %#v, want empty non-nil", in, got)
		}
	}
}

func TestTheShareIsNullWithoutRequests(t *testing.T) {
	// A bucket with no offers counted (never produced by the store) must not divide by zero.
	got := report.PromiseCoverage([]report.CoverageBucket{bucket(cov, "78", days(1), 0)})
	for _, w := range got[0].Regions[0].Within {
		if w.Share != nil || w.Count != 0 {
			t.Fatalf("within = %+v, want a null share and no count", w)
		}
	}
}
