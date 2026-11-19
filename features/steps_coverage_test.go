package features_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/adapters/inbound/reportshttp"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/analyticsstore"
	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// coverageWorld is the state of promise_coverage.feature: the in-memory
// analytical store (the twin the contract suite proves equal to Postgres)
// behind the REAL reports router, the same one cmd/dp-reports serves.
type coverageWorld struct {
	store *analyticsstore.MemoryStore
	seq   int
	body  map[string]any
}

func (c *coverageWorld) reset() { c.store, c.seq, c.body = analyticsstore.NewMemoryStore(), 0, nil }

// quote applies count DeliveryOfferQuoted at 10:00Z of day for a destination
// prefix whose window ends the given number of UTC days later (-1: no window).
func (c *coverageWorld) quote(day, prefix string, days, count int) error {
	d, err := time.Parse("2006-01-02", day)
	if err != nil {
		return err
	}
	at := d.Add(10 * time.Hour)
	for range count {
		c.seq++
		e := report.Event{Kind: report.KindDeliveryOfferQuoted, EventID: fmt.Sprintf("evt-%d", c.seq), At: at,
			OfferID: fmt.Sprintf("off-%d", c.seq), Tier: "NEXT_DAY", Region: report.RegionOf(prefix)}
		if days >= 0 {
			e.DeliverByLatest = d.AddDate(0, 0, days).Add(12 * time.Hour)
		}
		if _, err := c.store.Apply(context.Background(), e); err != nil {
			return err
		}
	}
	return nil
}

func (c *coverageWorld) read(from, to string) error {
	srv := &reportshttp.Server{Reader: c.store, Now: func() time.Time { return time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC) }}
	h := reportshttp.NewRouter(srv, slog.New(slog.NewTextHandler(io.Discard, nil)), "")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet,
		"/reports/quote-conversion?from="+from+"&to="+to, nil))
	if rec.Code != http.StatusOK {
		return fmt.Errorf("status %d: %s", rec.Code, rec.Body.String())
	}
	c.body = map[string]any{}
	return json.Unmarshal(rec.Body.Bytes(), &c.body)
}

// regions are the region objects of a day of promiseCoverage.
func (c *coverageWorld) regions(day string) ([]map[string]any, error) {
	list, ok := c.body["promiseCoverage"].([]any)
	if !ok {
		return nil, fmt.Errorf("promiseCoverage = %#v, want a list", c.body["promiseCoverage"])
	}
	for _, item := range list {
		d := item.(map[string]any)
		if d["day"] == day {
			out := []map[string]any{}
			for _, r := range d["regions"].([]any) {
				out = append(out, r.(map[string]any))
			}
			return out, nil
		}
	}
	return nil, fmt.Errorf("no promiseCoverage for %s in %v", day, list)
}

func (c *coverageWorld) region(day, name string) (map[string]any, error) {
	regions, err := c.regions(day)
	if err != nil {
		return nil, err
	}
	for _, r := range regions {
		if r["region"] == name {
			return r, nil
		}
	}
	return nil, fmt.Errorf("no region %q on %s in %v", name, day, regions)
}

func names(regions []map[string]any) []string {
	out := []string{}
	for _, r := range regions {
		out = append(out, r["region"].(string))
	}
	return out
}

func ints(csv string) []int {
	out := []int{}
	for _, f := range strings.FieldsFunc(csv, func(r rune) bool { return r == ',' || r == ' ' || r == 'a' || r == 'n' || r == 'd' }) {
		if n, err := strconv.Atoi(f); err == nil {
			out = append(out, n)
		}
	}
	return out
}

func (c *coverageWorld) offersTable(t *godog.Table) error {
	for _, row := range t.Rows[1:] {
		days := -1
		if row.Cells[2].Value != "-" {
			days, _ = strconv.Atoi(row.Cells[2].Value)
		}
		count, _ := strconv.Atoi(row.Cells[3].Value)
		if err := c.quote(row.Cells[0].Value, row.Cells[1].Value, days, count); err != nil {
			return err
		}
	}
	return nil
}

func (c *coverageWorld) manyRegions(n int, day, counts string) error {
	per := ints(counts)
	if len(per) != n {
		return fmt.Errorf("%d counts for %d regions", len(per), n)
	}
	for i, count := range per {
		if err := c.quote(day, fmt.Sprintf("%02d", i), 1, count); err != nil {
			return err
		}
	}
	return nil
}

func (c *coverageWorld) listsRegions(day, want string) error {
	regions, err := c.regions(day)
	if err != nil {
		return err
	}
	if got := names(regions); !slices.Equal(got, strings.Split(want, ", ")) {
		return fmt.Errorf("regions of %s = %v, want %s", day, got, want)
	}
	return nil
}

func (c *coverageWorld) listsNRegions(day string, n int, last string) error {
	regions, err := c.regions(day)
	if err != nil {
		return err
	}
	if got := names(regions); len(got) != n || got[len(got)-1] != last {
		return fmt.Errorf("regions of %s = %v, want %d ending with %s", day, got, n, last)
	}
	return nil
}

func (c *coverageWorld) hasRequests(name, day string, req, answered int) error {
	r, err := c.region(day, name)
	if err != nil {
		return err
	}
	if r["requests"] != float64(req) || r["answeredWithPromise"] != float64(answered) {
		return fmt.Errorf("region %s = %v, want %d requests and %d answered", name, r, req, answered)
	}
	return nil
}

func (c *coverageWorld) isWithin(name, day string, a, b, cc, d int) error {
	r, err := c.region(day, name)
	if err != nil {
		return err
	}
	got := []int{}
	for _, w := range r["answeredWithinDays"].([]any) {
		got = append(got, int(w.(map[string]any)["count"].(float64)))
	}
	if want := []int{a, b, cc, d}; !reflect.DeepEqual(got, want) {
		return fmt.Errorf("region %s counts = %v, want %v", name, got, want)
	}
	return nil
}

func (c *coverageWorld) shareIs(name, day string, days int, share float64) error {
	r, err := c.region(day, name)
	if err != nil {
		return err
	}
	for _, w := range r["answeredWithinDays"].([]any) {
		if m := w.(map[string]any); m["days"] == float64(days) {
			if m["share"] != share {
				return fmt.Errorf("share within %d days = %v, want %v", days, m["share"], share)
			}
			return nil
		}
	}
	return fmt.Errorf("no %d day threshold in %v", days, r["answeredWithinDays"])
}

func (c *coverageWorld) noRegionLongerThan(limit int) error {
	for _, item := range c.body["promiseCoverage"].([]any) {
		for _, r := range item.(map[string]any)["regions"].([]any) {
			if name := r.(map[string]any)["region"].(string); len(name) > limit {
				return fmt.Errorf("region %q is longer than %d", name, limit)
			}
		}
	}
	return nil
}

func (c *coverageWorld) coverageIsEmpty() error {
	if list, ok := c.body["promiseCoverage"].([]any); !ok || len(list) != 0 {
		return fmt.Errorf("promiseCoverage = %#v, want []", c.body["promiseCoverage"])
	}
	return nil
}

func (c *coverageWorld) dayRowsAreEmpty() error {
	if list, ok := c.body["days"].([]any); !ok || len(list) != 0 {
		return fmt.Errorf("days = %#v, want []", c.body["days"])
	}
	return nil
}

func (c *coverageWorld) dayRowsList(tier, day string, offers, promises int) error {
	want := []any{map[string]any{"day": day, "tier": tier, "offersQuoted": float64(offers), "promisesMade": float64(promises), "conversion": 0.0}}
	if !reflect.DeepEqual(c.body["days"], want) {
		return fmt.Errorf("days = %v, want %v", c.body["days"], want)
	}
	return nil
}

// registerCoverageSteps registers the steps of promise_coverage.feature.
func registerCoverageSteps(sc *godog.ScenarioContext) {
	c := &coverageWorld{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		c.reset()
		return ctx, nil
	})
	sc.Step(`^these offers were quoted:$`, c.offersTable)
	sc.Step(`^(\d+) regions were quoted on "([^"]*)" with ([\d, and]+) offers within 1 day$`, c.manyRegions)
	sc.Step(`^I read the quote-conversion report from "([^"]*)" to "([^"]*)"$`, c.read)
	sc.Step(`^the promise coverage of "([^"]*)" lists the regions "([^"]*)"$`, c.listsRegions)
	sc.Step(`^the promise coverage of "([^"]*)" lists (\d+) regions ending with "([^"]*)"$`, c.listsNRegions)
	sc.Step(`^region "([^"]*)" of "([^"]*)" has (\d+) requests and (\d+) answered with a promise$`, c.hasRequests)
	sc.Step(`^region "([^"]*)" of "([^"]*)" is within 1, 2, 3 and 5 days for (\d+), (\d+), (\d+) and (\d+) offers$`, c.isWithin)
	sc.Step(`^the share of region "([^"]*)" of "([^"]*)" within (\d+) days is ([\d.]+)$`, c.shareIs)
	sc.Step(`^no region of the report is longer than (\d+) characters$`, c.noRegionLongerThan)
	sc.Step(`^the promise coverage is an empty list$`, c.coverageIsEmpty)
	sc.Step(`^the day rows are an empty list$`, c.dayRowsAreEmpty)
	sc.Step(`^the day rows list the tier "([^"]*)" on "([^"]*)" with (\d+) offers quoted and (\d+) promises made$`, c.dayRowsList)
}
