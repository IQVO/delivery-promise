package analyticsstore_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

// store is what both twins implement: the writer and the reader port.
type store interface {
	report.Projection
	report.Reader
}

// runContract is THE contract of the analytical store (ADR 0018). It runs
// against MemoryStore (memory_test.go) and against real Postgres
// (postgres_integration_test.go); newStore returns an EMPTY store.
func runContract(t *testing.T, newStore func(t *testing.T) store) {
	t.Run("an empty store answers empty, non-nil reports", func(t *testing.T) { contractEmpty(t, newStore(t)) })
	t.Run("a replayed id is a no-op", func(t *testing.T) { contractReplay(t, newStore(t)) })
	t.Run("promise reliability per day, tier and basis", func(t *testing.T) { contractReliability(t, newStore(t)) })
	t.Run("promise precision per day and tier", func(t *testing.T) { contractPrecision(t, newStore(t)) })
	t.Run("quote conversion per day and tier", func(t *testing.T) { contractConversion(t, newStore(t)) })
	t.Run("the freshness basis is the newest CloudEvents time", func(t *testing.T) { contractLastEvent(t, newStore(t)) })
	t.Run("an invalid fact is rejected and leaves nothing", func(t *testing.T) { contractRejected(t, newStore(t)) })
}

var (
	ctx       = context.Background()
	nov2      = time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	nov3      = nov2.Add(24 * time.Hour)
	nov4      = nov3.Add(24 * time.Hour)
	fullRange = report.Range{From: nov3, To: nov3.Add(48 * time.Hour)}
	// austin is the worked window of ADR 0011: 09:00-21:00 in America/Chicago
	// on 2026-10-31, 14:00Z .. 02:00Z of the next UTC day.
	austinFrom = time.Date(2026, 10, 31, 14, 0, 0, 0, time.UTC)
	austinTo   = austinFrom.Add(12 * time.Hour)
)

const chicago = "America/Chicago"

// seeder applies facts, each under a fresh event id.
type seeder struct {
	t   *testing.T
	s   store
	seq int
}

func (sd *seeder) apply(e report.Event) {
	sd.t.Helper()
	sd.seq++
	e.EventID = fmt.Sprintf("evt-%d", sd.seq)
	applied, err := sd.s.Apply(ctx, e)
	if err != nil || !applied {
		sd.t.Fatalf("apply %s %s: applied=%v err=%v", e.Kind, e.EventID, applied, err)
	}
}

func (sd *seeder) made(id, tier, basis string, at, earliest, latest time.Time, zone string) {
	sd.t.Helper()
	sd.apply(report.Event{Kind: report.KindPromiseMade, At: at, PromiseID: id, OfferID: "off-" + id, Tier: tier, Basis: basis,
		DeliverByEarliest: earliest, DeliverByLatest: latest, TimeZone: zone})
}

func (sd *seeder) kept(id string, at time.Time) {
	sd.t.Helper()
	sd.apply(report.Event{Kind: report.KindPromiseKept, At: at, PromiseID: id})
}

func (sd *seeder) broken(id, cause string, lateBy int, at time.Time) {
	sd.t.Helper()
	sd.apply(report.Event{Kind: report.KindPromiseBroken, At: at, PromiseID: id, Cause: cause, LateByMinutes: lateBy})
}

func (sd *seeder) revised(id string, no int, at time.Time) {
	sd.t.Helper()
	sd.apply(report.Event{Kind: report.KindPromiseRevised, At: at, PromiseID: id, RevisionNo: no})
}

func (sd *seeder) unkeepable(id string, at time.Time) {
	sd.t.Helper()
	sd.apply(report.Event{Kind: report.KindPromiseUnkeepable, At: at, PromiseID: id})
}

func (sd *seeder) quoted(offer, tier string, at time.Time) {
	sd.t.Helper()
	sd.apply(report.Event{Kind: report.KindDeliveryOfferQuoted, At: at, OfferID: offer, Tier: tier})
}

// seedJudgements writes the fixture of the reliability contract: promises
// made on nov2 (outside the precision range), judged on nov3 and nov4.
func seedJudgements(sd *seeder) {
	sd.made("A", "NEXT_DAY", "SCHEDULED", nov2, austinFrom, austinTo, chicago)
	sd.kept("A", nov3.Add(10*time.Hour))
	sd.made("B", "NEXT_DAY", "SCHEDULED", nov2, austinFrom, austinTo, chicago)
	sd.broken("B", report.CauseLate, 30, nov3.Add(12*time.Hour))
	sd.made("C", "NEXT_DAY", "SCHEDULED", nov2, austinFrom, austinTo, chicago)
	sd.broken("C", report.CauseLate, 90, nov4.Add(-time.Second))
	sd.made("D", "NEXT_DAY", "MEASURED", nov2, austinFrom, austinTo, chicago)
	sd.kept("D", nov3.Add(9*time.Hour))
	// E has no PromiseMade: UNKNOWN / UNKNOWN.
	sd.broken("E", report.CauseLost, 0, nov4.Add(8*time.Hour))
	// F's PromiseMade is projected AFTER its judgement (the two topics are independent).
	sd.broken("F", report.CauseReturned, 0, nov4.Add(9*time.Hour))
	sd.made("F", "STANDARD", "NOMINAL", nov2, austinFrom, austinTo, chicago)
	// Outside the range: exactly at To (exclusive) and one second before From.
	sd.made("G", "NEXT_DAY", "SCHEDULED", nov2, austinFrom, austinTo, chicago)
	sd.kept("G", fullRange.To)
	sd.kept("H", nov3.Add(-time.Second))
}

// seedPromises writes the fixture of the precision and conversion contracts.
func seedPromises(sd *seeder) {
	// P1: one Chicago day, revised twice, unkeepable.
	sd.made("P1", "NEXT_DAY", "SCHEDULED", nov3.Add(10*time.Hour), austinFrom, austinTo, chicago)
	sd.revised("P1", 1, nov3.Add(11*time.Hour))
	sd.revised("P1", 2, nov3.Add(12*time.Hour))
	sd.unkeepable("P1", nov3.Add(13*time.Hour))
	// P2: no zone -> read in UTC, where the same window straddles midnight.
	sd.made("P2", "NEXT_DAY", "SCHEDULED", nov3.Add(11*time.Hour), austinFrom, austinTo, "")
	// P3: two Chicago days; its revision is projected BEFORE its PromiseMade.
	sd.revised("P3", 1, nov4.Add(time.Hour))
	sd.made("P3", "NEXT_DAY", "MEASURED", nov3.Add(12*time.Hour), austinFrom, austinTo.Add(24*time.Hour), chicago)
	// P4: a four-hour UTC window on nov4.
	sd.made("P4", "SAME_DAY", "SCHEDULED", nov4.Add(9*time.Hour), nov4.Add(10*time.Hour), nov4.Add(14*time.Hour), "UTC")
	// Outside the range.
	sd.made("P5", "NEXT_DAY", "SCHEDULED", fullRange.To, austinFrom, austinTo, chicago)
	sd.made("P6", "NEXT_DAY", "SCHEDULED", nov3.Add(-time.Second), austinFrom, austinTo, chicago)
	sd.quoted("O1", "NEXT_DAY", nov3.Add(9*time.Hour))
	sd.quoted("O2", "NEXT_DAY", nov3.Add(9*time.Hour))
	sd.quoted("O3", "NEXT_DAY", nov3.Add(10*time.Hour))
	sd.quoted("O4", "SAME_DAY", nov3.Add(10*time.Hour))
	sd.quoted("O5", "TWO_DAY", nov4.Add(10*time.Hour))
	sd.quoted("O6", "NEXT_DAY", fullRange.To)
}

func contractEmpty(t *testing.T, s store) {
	rel, err := s.PromiseReliability(ctx, fullRange)
	if err != nil || rel == nil || len(rel) != 0 {
		t.Fatalf("reliability = %#v, %v; want empty non-nil", rel, err)
	}
	prec, err := s.PromisePrecision(ctx, fullRange)
	if err != nil || prec == nil || len(prec) != 0 {
		t.Fatalf("precision = %#v, %v; want empty non-nil", prec, err)
	}
	conv, err := s.QuoteConversion(ctx, fullRange)
	if err != nil || conv == nil || len(conv) != 0 {
		t.Fatalf("conversion = %#v, %v; want empty non-nil", conv, err)
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("last = %v, %v; want nil", last, err)
	}
}

func contractReplay(t *testing.T, s store) {
	e := report.Event{Kind: report.KindPromiseKept, EventID: "evt-1", PromiseID: "A", At: nov3}
	if applied, err := s.Apply(ctx, e); err != nil || !applied {
		t.Fatalf("first apply: %v %v", applied, err)
	}
	if applied, err := s.Apply(ctx, e); err != nil || applied {
		t.Fatalf("replay: applied=%v err=%v; want a no-op", applied, err)
	}
	rel, err := s.PromiseReliability(ctx, fullRange)
	if err != nil || len(rel) != 1 || rel[0].Kept != 1 {
		t.Fatalf("reliability after a replay = %+v, %v; want one kept", rel, err)
	}
	// A second revision fact with the same number (another id) adds no revision.
	sd := &seeder{t: t, s: s, seq: 100}
	sd.made("R", "NEXT_DAY", "SCHEDULED", nov3, austinFrom, austinTo, chicago)
	sd.revised("R", 1, nov3)
	sd.revised("R", 1, nov3)
	prec, err := s.PromisePrecision(ctx, fullRange)
	if err != nil || len(prec) != 1 || prec[0].Revised != 1 || prec[0].PromisesMade != 1 {
		t.Fatalf("precision after a duplicate revision = %+v, %v", prec, err)
	}
}

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// opt is a nullable figure as a comparable number (-1 = null).
func opt(p *float64) float64 {
	if p == nil {
		return -1
	}
	return *p
}

// row is a report row as its key and its figures, compared with near.
type row struct {
	key  string
	nums []float64
}

func sameRows(t *testing.T, what string, got, want []row) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %+v\nwant %+v", what, got, want)
	}
	for i := range want {
		if got[i].key != want[i].key || len(got[i].nums) != len(want[i].nums) {
			t.Errorf("%s row %d = %+v, want %+v", what, i, got[i], want[i])
			continue
		}
		for j := range want[i].nums {
			if !near(got[i].nums[j], want[i].nums[j]) {
				t.Errorf("%s row %d (%s) figure %d = %v, want %v", what, i, want[i].key, j, got[i].nums[j], want[i].nums[j])
			}
		}
	}
}

func dayKey(d time.Time, parts ...string) string {
	if d.Location() != time.UTC {
		return "not UTC: " + d.String()
	}
	k := report.FormatDay(d)
	for _, p := range parts {
		k += "/" + p
	}
	return k
}

func reliabilityRows(days []report.ReliabilityDay) []row {
	out := []row{}
	for _, d := range days {
		out = append(out, row{dayKey(d.Day, d.Tier, d.Basis), []float64{float64(d.Kept), float64(d.Broken), float64(d.BrokenLate),
			float64(d.BrokenLost), float64(d.BrokenReturned), opt(d.LateByP50), opt(d.LateByP90)}})
	}
	return out
}

func contractReliability(t *testing.T, s store) {
	seedJudgements(&seeder{t: t, s: s})
	got, err := s.PromiseReliability(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	// kept, broken, late, lost, returned, lateBy p50, p90
	sameRows(t, "reliability", reliabilityRows(got), []row{
		{"2026-11-03/NEXT_DAY/MEASURED", []float64{1, 0, 0, 0, 0, -1, -1}},
		{"2026-11-03/NEXT_DAY/SCHEDULED", []float64{1, 2, 2, 0, 0, 60, 84}},
		{"2026-11-04/STANDARD/NOMINAL", []float64{0, 1, 0, 0, 1, -1, -1}},
		{"2026-11-04/UNKNOWN/UNKNOWN", []float64{0, 1, 0, 1, 0, -1, -1}},
	})
	one, err := s.PromiseReliability(ctx, report.Range{From: nov4, To: fullRange.To})
	if err != nil || len(one) != 2 || !one[0].Day.Equal(nov4) {
		t.Fatalf("a one-day range = %+v, %v", one, err)
	}
}

func precisionRows(days []report.PrecisionDay) []row {
	out := []row{}
	for _, d := range days {
		out = append(out, row{dayKey(d.Day, d.Tier), []float64{float64(d.PromisesMade), d.WindowMinutesSum, d.WindowP50, d.WindowP90,
			float64(d.ExactDay), float64(d.ZoneUnknown), float64(d.Revised), float64(d.Unkeepable)}})
	}
	return out
}

func contractPrecision(t *testing.T, s store) {
	seedPromises(&seeder{t: t, s: s})
	got, err := s.PromisePrecision(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	// made, width sum, p50, p90, exact day, zone unknown, revised, unkeepable
	sameRows(t, "precision", precisionRows(got), []row{
		{"2026-11-03/NEXT_DAY", []float64{3, 3600, 720, 1872, 1, 1, 2, 1}},
		{"2026-11-04/SAME_DAY", []float64{1, 240, 240, 240, 1, 0, 0, 0}},
	})
}

func conversionRows(days []report.ConversionDay) []row {
	out := []row{}
	for _, d := range days {
		out = append(out, row{dayKey(d.Day, d.Tier), []float64{float64(d.OffersQuoted), float64(d.PromisesMade)}})
	}
	return out
}

func contractConversion(t *testing.T, s store) {
	seedPromises(&seeder{t: t, s: s})
	got, err := s.QuoteConversion(ctx, fullRange)
	if err != nil {
		t.Fatal(err)
	}
	// offers quoted, promises made
	sameRows(t, "conversion", conversionRows(got), []row{
		{"2026-11-03/NEXT_DAY", []float64{3, 3}},
		{"2026-11-03/SAME_DAY", []float64{1, 0}},
		{"2026-11-04/SAME_DAY", []float64{0, 1}},
		{"2026-11-04/TWO_DAY", []float64{1, 0}},
	})
}

func contractLastEvent(t *testing.T, s store) {
	sd := &seeder{t: t, s: s}
	sd.quoted("O1", "NEXT_DAY", nov3.Add(5*time.Hour))
	sd.kept("A", nov3.Add(2*time.Hour))
	last, err := s.LastEventAt(ctx)
	newest := nov3.Add(5 * time.Hour)
	if err != nil || last == nil || !last.Equal(newest) || last.Location() != time.UTC {
		t.Fatalf("last = %v, %v; want %v UTC (the newest, not the last applied)", last, err, newest)
	}
}

func contractRejected(t *testing.T, s store) {
	bad := []report.Event{
		{Kind: report.KindPromiseKept, EventID: "evt-bad-1", At: nov3},
		{Kind: "PromiseConfirmed", EventID: "evt-bad-2", PromiseID: "A", At: nov3},
		{Kind: report.KindPromiseBroken, EventID: "evt-bad-3", PromiseID: "A", At: nov3, Cause: "EARLY"},
		{Kind: report.KindPromiseMade, EventID: "evt-bad-4", PromiseID: "A", OfferID: "o", Tier: "NEXT_DAY", Basis: "SCHEDULED",
			At: nov3, DeliverByEarliest: austinTo, DeliverByLatest: austinFrom},
	}
	for _, e := range bad {
		if applied, err := s.Apply(ctx, e); applied || !errors.Is(err, report.ErrRejected) {
			t.Errorf("%s: applied=%v err=%v; want ErrRejected", e.EventID, applied, err)
		}
	}
	if last, err := s.LastEventAt(ctx); err != nil || last != nil {
		t.Fatalf("a rejected fact must leave no mark, last = %v, %v", last, err)
	}
}
