package requestfact_test

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/requestfact"
)

func utc(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, time.UTC)
}

func ptr(n int) *int { return &n }

func TestKindValid(t *testing.T) {
	for k, want := range map[requestfact.Kind]bool{requestfact.KindQuote: true, requestfact.KindEstimate: true, "": false, "quote": false, "OTHER": false} {
		if got := k.Valid(); got != want {
			t.Errorf("Kind(%q).Valid() = %v, want %v", k, got, want)
		}
	}
	if requestfact.KindQuote != "QUOTE" || requestfact.KindEstimate != "ESTIMATE" {
		t.Fatal("the wire values of the kinds changed")
	}
}

func TestRegionOfKeepsTheFirstTwoCharactersOnly(t *testing.T) {
	for in, want := range map[string]string{"": "", "7": "7", "78": "78", "787": "78", "78701": "78", "AB1": "AB", "éa1": "éa"} {
		if got := requestfact.RegionOf(in); got != want {
			t.Errorf("RegionOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPromisedDays(t *testing.T) {
	chicago := "America/Chicago"
	cases := []struct {
		name         string
		at, latest   time.Time
		zone         string
		want         *int
		wantNoAnswer bool
	}{
		{name: "no latest delivery is not a promise", at: utc(2026, 11, 10, 12, 0), wantNoAnswer: true},
		{name: "same day is 0", at: utc(2026, 11, 10, 8, 0), latest: utc(2026, 11, 10, 22, 0), want: ptr(0)},
		{name: "next day is 1", at: utc(2026, 11, 10, 8, 0), latest: utc(2026, 11, 11, 2, 0), want: ptr(1)},
		{name: "five days", at: utc(2026, 11, 10, 23, 59), latest: utc(2026, 11, 15, 0, 1), want: ptr(5)},
		{name: "a latest before the answer is never negative", at: utc(2026, 11, 10, 8, 0), latest: utc(2026, 11, 8, 8, 0), want: ptr(0)},
		// 2026-11-11 03:00 UTC is still 2026-11-10 21:00 in Chicago: the zone moves the day.
		{name: "the zone moves the day", at: utc(2026, 11, 10, 15, 0), latest: utc(2026, 11, 11, 3, 0), zone: chicago, want: ptr(0)},
		{name: "the same instants read in UTC", at: utc(2026, 11, 10, 15, 0), latest: utc(2026, 11, 11, 3, 0), want: ptr(1)},
		// the answer is read in the zone too: 2026-11-10 03:00 UTC is 2026-11-09 21:00 in Chicago.
		{name: "the answer day is read in the zone", at: utc(2026, 11, 10, 3, 0), latest: utc(2026, 11, 10, 20, 0), zone: chicago, want: ptr(1)},
		{name: "a daylight-saving change does not shift the count", at: utc(2026, 11, 1, 3, 0), latest: utc(2026, 11, 3, 18, 0), zone: chicago, want: ptr(3)},
		{name: "an unknown zone reads in UTC", at: utc(2026, 11, 10, 15, 0), latest: utc(2026, 11, 11, 3, 0), zone: "Mars/Olympus", want: ptr(1)},
		{name: "Local is never a station zone", at: utc(2026, 11, 10, 15, 0), latest: utc(2026, 11, 11, 3, 0), zone: "Local", want: ptr(1)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := requestfact.PromisedDays(c.at, c.latest, c.zone)
			switch {
			case c.wantNoAnswer && got != nil:
				t.Fatalf("PromisedDays = %d, want nil", *got)
			case !c.wantNoAnswer && (got == nil || *got != *c.want):
				t.Fatalf("PromisedDays = %v, want %d", got, *c.want)
			}
		})
	}
}

func TestPromisedDaysLocalIsReadInUTCEvenWhenTheHostIsNot(t *testing.T) {
	old := time.Local
	time.Local = time.FixedZone("host", -10*3600)
	t.Cleanup(func() { time.Local = old })
	at, latest := utc(2026, 11, 10, 15, 0), utc(2026, 11, 11, 3, 0)
	if got := requestfact.PromisedDays(at, latest, "Local"); got == nil || *got != 1 {
		t.Fatalf("PromisedDays with Local = %v, want 1 (UTC)", got)
	}
}

func TestNewReducesTheAnsweredRequest(t *testing.T) {
	at := utc(2026, 11, 10, 12, 30)
	opt := func(day int) requestfact.Option { return requestfact.Option{Latest: utc(2026, 11, day, 20, 0)} }
	cases := []struct {
		name         string
		options      []requestfact.Option
		answered     bool
		shortestDays *int
	}{
		{name: "no option is an answer without a promise"},
		{name: "options without a latest delivery are no promise", options: []requestfact.Option{{}, {TimeZone: "America/Chicago"}}},
		{name: "one option", options: []requestfact.Option{opt(13)}, answered: true, shortestDays: ptr(3)},
		{name: "the shortest wins, first", options: []requestfact.Option{opt(11), opt(15), opt(13)}, answered: true, shortestDays: ptr(1)},
		{name: "the shortest wins, last", options: []requestfact.Option{opt(15), opt(13), opt(11)}, answered: true, shortestDays: ptr(1)},
		{name: "the shortest wins, middle", options: []requestfact.Option{opt(15), opt(12), opt(14)}, answered: true, shortestDays: ptr(2)},
		{name: "an empty option does not hide a promise", options: []requestfact.Option{{}, opt(12)}, answered: true, shortestDays: ptr(2)},
		{name: "a same-day promise is 0", options: []requestfact.Option{opt(10), opt(12)}, answered: true, shortestDays: ptr(0)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, ok := requestfact.New(requestfact.KindEstimate, "78701", 7, c.options, at)
			if !ok {
				t.Fatal("New refused a describable request")
			}
			if f.Region != "78" || f.Kind != requestfact.KindEstimate || f.Items != 7 || !f.At.Equal(at) || f.Answered != c.answered {
				t.Fatalf("fact = %+v", f)
			}
			if (f.ShortestDays == nil) != (c.shortestDays == nil) || (f.ShortestDays != nil && *f.ShortestDays != *c.shortestDays) {
				t.Fatalf("ShortestDays = %v, want %v", f.ShortestDays, c.shortestDays)
			}
		})
	}
}

func TestNewKeepsTheInstantInUTC(t *testing.T) {
	loc := time.FixedZone("X", 3*3600)
	f, ok := requestfact.New(requestfact.KindQuote, "7", 1, nil, time.Date(2026, 11, 10, 12, 0, 0, 0, loc))
	if !ok || f.At.Location() != time.UTC || f.Region != "7" {
		t.Fatalf("fact = %+v ok=%v", f, ok)
	}
}

func TestNewRefusesWhatItCannotDescribe(t *testing.T) {
	at := utc(2026, 11, 10, 12, 0)
	cases := map[string]func() (requestfact.Fact, bool){
		"unknown kind": func() (requestfact.Fact, bool) { return requestfact.New("quote", "787", 1, nil, at) },
		"empty kind":   func() (requestfact.Fact, bool) { return requestfact.New("", "787", 1, nil, at) },
		"no region":    func() (requestfact.Fact, bool) { return requestfact.New(requestfact.KindQuote, "", 1, nil, at) },
		"no item":      func() (requestfact.Fact, bool) { return requestfact.New(requestfact.KindQuote, "787", 0, nil, at) },
		"negative":     func() (requestfact.Fact, bool) { return requestfact.New(requestfact.KindQuote, "787", -1, nil, at) },
		"no instant": func() (requestfact.Fact, bool) {
			return requestfact.New(requestfact.KindQuote, "787", 1, nil, time.Time{})
		},
	}
	for name, run := range cases {
		if f, ok := run(); ok || f.Region != "" {
			t.Errorf("%s: got %+v ok=%v, want the zero fact and false", name, f, ok)
		}
	}
	if _, ok := requestfact.New(requestfact.KindQuote, "787", 1, nil, at); !ok {
		t.Error("one item is describable")
	}
}

func TestEventOfAFact(t *testing.T) {
	loc := time.FixedZone("X", 3*3600)
	f := requestfact.Fact{Region: "78", Kind: requestfact.KindQuote, Items: 3, Answered: true, ShortestDays: ptr(2), At: time.Date(2026, 11, 10, 12, 0, 0, 0, loc)}
	e := f.Event("req-1")
	if e.EventName() != "DeliveryRequestAnswered" || e.Entity() != "deliveryrequest" || e.SubjectID() != "req-1" {
		t.Fatalf("identity = %s %s %s", e.EventName(), e.Entity(), e.SubjectID())
	}
	if e.AggregateVersion() != 1 || !e.OccurredAt().Equal(f.At) || e.OccurredAt().Location() != time.UTC {
		t.Fatalf("header = %d %v", e.AggregateVersion(), e.OccurredAt())
	}
	if e.Region != "78" || e.Kind != requestfact.KindQuote || e.Items != 3 || !e.Answered || e.ShortestPromisedDays == nil || *e.ShortestPromisedDays != 2 {
		t.Fatalf("payload fields = %+v", e)
	}
}

func TestEventOfAnUnansweredFact(t *testing.T) {
	at := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	none := requestfact.Fact{Region: "78", Kind: requestfact.KindEstimate, Items: 1, At: at}.Event("req-2")
	if none.Answered || none.ShortestPromisedDays != nil || none.Kind != requestfact.KindEstimate {
		t.Fatalf("an unanswered fact = %+v", none)
	}
}
