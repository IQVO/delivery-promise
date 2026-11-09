package report_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/analytics/report"
)

func TestCausesAreThePromiseBrokenVocabulary(t *testing.T) {
	want := []string{"LATE", "LOST", "RETURNED"}
	if got := report.Causes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Causes = %v, want %v", got, want)
	}
	for _, c := range want {
		if !report.IsCause(c) {
			t.Errorf("IsCause(%s) = false, want true", c)
		}
	}
	for _, c := range []string{"", "late", "EARLY", "CANCELLED"} {
		if report.IsCause(c) {
			t.Errorf("IsCause(%q) = true, want false", c)
		}
	}
}

func TestRanksAndUnknowns(t *testing.T) {
	if report.P50() != 0.5 || report.P90() != 0.9 {
		t.Fatalf("ranks = %v, %v; want 0.5, 0.9", report.P50(), report.P90())
	}
	if report.UnknownTier() != "UNKNOWN" || report.UnknownBasis() != "UNKNOWN" {
		t.Fatalf("unknowns = %q, %q", report.UnknownTier(), report.UnknownBasis())
	}
}

func TestVerdictOf(t *testing.T) {
	cases := []struct {
		kind report.Kind
		want string
		ok   bool
	}{
		{report.KindPromiseKept, report.VerdictKept, true},
		{report.KindPromiseBroken, report.VerdictBroken, true},
		{report.KindPromiseMade, "", false},
		{report.KindPromiseRevised, "", false},
		{report.KindPromiseUnkeepable, "", false},
		{report.KindDeliveryOfferQuoted, "", false},
	}
	for _, c := range cases {
		got, ok := report.VerdictOf(c.kind)
		if got != c.want || ok != c.ok {
			t.Errorf("VerdictOf(%s) = %q, %v; want %q, %v", c.kind, got, ok, c.want, c.ok)
		}
	}
}

// The worked example of ADR 0011: 09:00-21:00 in Austin on 2026-10-31 is
// 14:00Z .. 02:00Z the next UTC day.
var (
	austinEarliest = time.Date(2026, 10, 31, 14, 0, 0, 0, time.UTC)
	austinLatest   = time.Date(2026, 11, 1, 2, 0, 0, 0, time.UTC)
)

func TestWindowOfReadsTheWindowInItsOwnZone(t *testing.T) {
	got := report.WindowOf(austinEarliest, austinLatest, "America/Chicago")
	if got != (report.Window{Minutes: 720, ExactDay: true, ZoneKnown: true}) {
		t.Fatalf("Austin window = %+v", got)
	}
	// The same instants in UTC straddle midnight.
	if got := report.WindowOf(austinEarliest, austinLatest, "UTC"); got != (report.Window{Minutes: 720, ExactDay: false, ZoneKnown: true}) {
		t.Fatalf("UTC window = %+v", got)
	}
	twoDays := report.WindowOf(austinEarliest, austinLatest.Add(24*time.Hour), "America/Chicago")
	if twoDays != (report.Window{Minutes: 2160, ExactDay: false, ZoneKnown: true}) {
		t.Fatalf("two-day window = %+v", twoDays)
	}
}

func TestWindowOfWithoutAZoneReadsUTC(t *testing.T) {
	sameUTCDay := time.Date(2026, 10, 31, 2, 0, 0, 0, time.UTC)
	for _, zone := range []string{"", "Local", "Nowhere/Atlantis"} {
		if got := report.WindowOf(sameUTCDay, austinEarliest, zone); got != (report.Window{Minutes: 720, ExactDay: true, ZoneKnown: false}) {
			t.Errorf("zone %q: %+v, want a UTC exact day with an unknown zone", zone, got)
		}
		if got := report.WindowOf(austinEarliest, austinLatest, zone); got.ExactDay || got.ZoneKnown {
			t.Errorf("zone %q: a UTC-straddling window = %+v", zone, got)
		}
	}
	if got := report.WindowOf(austinEarliest, austinEarliest, ""); got.Minutes != 0 || !got.ExactDay {
		t.Fatalf("an instant window = %+v", got)
	}
}

var at = time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)

func validMade() report.Event {
	return report.Event{Kind: report.KindPromiseMade, EventID: "e1", At: at, PromiseID: "prm-1", OfferID: "off-1",
		Tier: "NEXT_DAY", Basis: "SCHEDULED", DeliverByEarliest: austinEarliest, DeliverByLatest: austinLatest}
}

func validQuote() report.Event {
	return report.Event{Kind: report.KindDeliveryOfferQuoted, EventID: "e2", At: at, OfferID: "off-1", Tier: "NEXT_DAY"}
}

func validBroken() report.Event {
	return report.Event{Kind: report.KindPromiseBroken, EventID: "e3", At: at, PromiseID: "prm-1", Cause: "LATE", LateByMinutes: 30}
}

func validRevised() report.Event {
	return report.Event{Kind: report.KindPromiseRevised, EventID: "e4", At: at, PromiseID: "prm-1", RevisionNo: 1}
}

func TestValidateAcceptsCompleteFacts(t *testing.T) {
	kept := report.Event{Kind: report.KindPromiseKept, EventID: "e5", At: at, PromiseID: "prm-1"}
	unkeepable := report.Event{Kind: report.KindPromiseUnkeepable, EventID: "e6", At: at, PromiseID: "prm-1"}
	instant := validMade()
	instant.DeliverByLatest = instant.DeliverByEarliest
	lost := validBroken()
	lost.Cause, lost.LateByMinutes = "LOST", 0
	for _, e := range []report.Event{validMade(), validQuote(), validBroken(), validRevised(), kept, unkeepable, instant, lost} {
		if err := e.Validate(); err != nil {
			t.Errorf("%s: %v", e.Kind, err)
		}
	}
}

type invalidCase struct {
	name   string
	mutate func(*report.Event)
	base   func() report.Event
}

var invalidCases = []invalidCase{
	{"id", func(e *report.Event) { e.EventID = "" }, validMade},
	{"time", func(e *report.Event) { e.At = time.Time{} }, validMade},
	{"promiseId", func(e *report.Event) { e.PromiseID = "" }, validMade},
	{"offerId", func(e *report.Event) { e.OfferID = "" }, validMade},
	{"tier", func(e *report.Event) { e.Tier = "" }, validMade},
	{"basis", func(e *report.Event) { e.Basis = "" }, validMade},
	{"deliverByEarliest", func(e *report.Event) { e.DeliverByEarliest = time.Time{} }, validMade},
	{"deliverByLatest", func(e *report.Event) { e.DeliverByLatest = time.Time{} }, validMade},
	{"a window that does not end before it starts", func(e *report.Event) { e.DeliverByLatest = e.DeliverByEarliest.Add(-time.Second) }, validMade},
	{"offerId", func(e *report.Event) { e.OfferID = "" }, validQuote},
	{"tier", func(e *report.Event) { e.Tier = "" }, validQuote},
	{"promiseId", func(e *report.Event) { e.PromiseID = "" }, validRevised},
	{"a revisionNo of at least 1", func(e *report.Event) { e.RevisionNo = 0 }, validRevised},
	{"promiseId", func(e *report.Event) { e.PromiseID = "" }, validBroken},
	{"a known cause", func(e *report.Event) { e.Cause = "EARLY" }, validBroken},
	{"a known cause", func(e *report.Event) { e.Cause = "" }, validBroken},
	{"a non-negative lateByMinutes", func(e *report.Event) { e.LateByMinutes = -1 }, validBroken},
	{"promiseId", func(e *report.Event) { e.Kind, e.PromiseID = report.KindPromiseKept, "" }, validBroken},
	{"promiseId", func(e *report.Event) { e.Kind, e.PromiseID = report.KindPromiseUnkeepable, "" }, validBroken},
	{"a known kind", func(e *report.Event) { e.Kind = "PromiseConfirmed" }, validMade},
}

func TestValidateNamesTheFirstMissingField(t *testing.T) {
	for _, c := range invalidCases {
		e := c.base()
		c.mutate(&e)
		err := e.Validate()
		if !errors.Is(err, report.ErrInvalidEvent) {
			t.Errorf("%s: err = %v, want ErrInvalidEvent", c.name, err)
			continue
		}
		if !strings.HasSuffix(err.Error(), ": "+c.name+" is required") {
			t.Errorf("%s: err = %q does not name the field", c.name, err)
		}
	}
}
