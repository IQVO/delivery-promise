package calendar

import (
	"testing"
	"time"
)

var cdt = time.FixedZone("CDT", -5*3600)

// at builds an instant on 2026-10-<day> at h:m in the test zone. 2026-10-22 is
// a Thursday.
func at(day, h, m int) time.Time { return time.Date(2026, 10, day, h, m, 0, 0, cdt) }

func testSchedule() Schedule {
	wk := DayHours{Start: 6 * 60, End: 22 * 60}
	return Schedule{
		Loc: cdt,
		Hours: map[time.Weekday]DayHours{
			time.Monday: wk, time.Tuesday: wk, time.Wednesday: wk, time.Thursday: wk, time.Friday: wk,
			time.Saturday: {Start: 8 * 60, End: 12 * 60},
		},
		Holidays: map[string]bool{"2026-10-26": true},
	}
}

func TestAddBusinessMinutes(t *testing.T) {
	s := testSchedule()
	cases := []struct {
		name string
		from time.Time
		n    int
		want time.Time
	}{
		{"worked example", at(22, 14, 5), 95, at(22, 15, 40)},
		{"spills into the next morning", at(22, 21, 30), 60, at(23, 6, 30)},
		{"friday night to saturday", at(23, 21, 30), 60, at(24, 8, 30)},
		{"saturday, sunday, holiday monday", at(24, 11, 30), 60, at(27, 6, 30)},
		{"before opening", at(22, 3, 0), 60, at(22, 7, 0)},
		{"ends exactly at closing", at(22, 21, 0), 60, at(22, 22, 0)},
		{"zero while closed waits for opening", at(22, 23, 0), 0, at(23, 6, 0)},
		{"zero while open", at(22, 10, 0), 0, at(22, 10, 0)},
		{"starts exactly at closing", at(22, 22, 0), 30, at(23, 6, 30)},
		{"several days", at(22, 6, 0), 16*60*2 + 10, at(24, 8, 10)},
	}
	for _, c := range cases {
		got, ok := s.AddBusinessMinutes(c.from, c.n)
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s: got %v (%v), want %v", c.name, got.In(cdt), ok, c.want)
		}
	}
	if got, _ := s.AddBusinessMinutes(at(22, 14, 5), 95); got.Location() != time.UTC {
		t.Error("results are UTC")
	}
	never := Schedule{Loc: cdt}
	if _, ok := never.AddBusinessMinutes(at(22, 10, 0), 5); ok {
		t.Error("a building that never opens has no answer")
	}
	inverted := Schedule{Loc: cdt, Hours: map[time.Weekday]DayHours{time.Thursday: {Start: 600, End: 600}}}
	if _, ok := inverted.AddBusinessMinutes(at(22, 10, 0), 5); ok {
		t.Error("empty opening hours never open")
	}
}

func TestSubBusinessMinutes(t *testing.T) {
	s := testSchedule()
	cases := []struct {
		name string
		to   time.Time
		n    int
		want time.Time
	}{
		{"same day", at(22, 18, 0), 95, at(22, 16, 25)},
		{"into the previous evening", at(23, 6, 30), 60, at(22, 21, 30)},
		{"over the weekend and a holiday", at(27, 6, 30), 60, at(24, 11, 30)},
		{"after closing is capped at closing", at(22, 23, 0), 60, at(22, 21, 0)},
		{"zero minutes at closing", at(22, 22, 0), 0, at(22, 22, 0)},
	}
	for _, c := range cases {
		got, ok := s.SubBusinessMinutes(c.to, c.n)
		if !ok || !got.Equal(c.want) {
			t.Errorf("%s: got %v (%v), want %v", c.name, got.In(cdt), ok, c.want)
		}
	}
	if _, ok := (Schedule{Loc: cdt}).SubBusinessMinutes(at(22, 10, 0), 5); ok {
		t.Error("a building that never opens has no answer")
	}
}

func TestSubIsTheInverseOfAdd(t *testing.T) {
	s := testSchedule()
	for _, n := range []int{1, 59, 95, 600, 1000, 3000} {
		for step := 0; step < 24*9; step++ {
			to := at(21, 0, 0).Add(time.Duration(step) * 37 * time.Minute)
			start, ok := s.SubBusinessMinutes(to, n)
			if !ok {
				t.Fatalf("Sub(%v,%d) failed", to, n)
			}
			end, _ := s.AddBusinessMinutes(start, n)
			if end.After(to) {
				t.Fatalf("Add(Sub(%v,%d)) = %v is after the target", to, n, end)
			}
			later, _ := s.AddBusinessMinutes(start.Add(time.Minute), n)
			if !later.After(to) {
				t.Fatalf("Sub(%v,%d) = %v is not the latest start: a minute later still ends at %v", to, n, start, later)
			}
		}
	}
}

func TestOperatingMinutesBetween(t *testing.T) {
	s := testSchedule()
	cases := []struct {
		name     string
		from, to time.Time
		want     int
	}{
		{"one day span", at(22, 14, 5), at(23, 14, 5), 475 + 485},
		{"inside one day", at(22, 10, 0), at(22, 11, 30), 90},
		{"empty", at(22, 10, 0), at(22, 10, 0), 0},
		{"reversed", at(22, 11, 0), at(22, 10, 0), 0},
		{"weekend and holiday", at(24, 8, 0), at(27, 6, 30), 240 + 30},
		{"only closed hours", at(22, 22, 0), at(23, 6, 0), 0},
	}
	for _, c := range cases {
		if got := s.OperatingMinutesBetween(c.from, c.to); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestDayMinutesAndDates(t *testing.T) {
	s := testSchedule()
	if s.DayMinutes(at(22, 1, 0)) != 960 || s.DayMinutes(at(24, 1, 0)) != 240 || s.DayMinutes(at(25, 1, 0)) != 0 || s.DayMinutes(at(26, 1, 0)) != 0 {
		t.Fatal("DayMinutes: thursday 960, saturday 240, sunday and holiday 0")
	}
	if got := s.DaysBetween(at(22, 23, 0), at(23, 1, 0)); got != 1 {
		t.Fatalf("DaysBetween across midnight = %d", got)
	}
	if got := s.DaysBetween(at(22, 1, 0), at(22, 23, 59)); got != 0 {
		t.Fatalf("DaysBetween same date = %d", got)
	}
	if got := s.DaysBetween(at(23, 1, 0), at(22, 1, 0)); got != -1 {
		t.Fatalf("DaysBetween backwards = %d", got)
	}
	if got := s.DaysBetween(at(22, 1, 0), at(29, 23, 0)); got != 7 {
		t.Fatalf("DaysBetween a week = %d", got)
	}
	// 2026-11-01 is the DST fall-back day in America/Chicago: the date arithmetic must still count whole dates.
	if chi, err := time.LoadLocation("America/Chicago"); err == nil {
		c := Schedule{Loc: chi}
		if got := c.DaysBetween(time.Date(2026, 10, 31, 23, 30, 0, 0, chi), time.Date(2026, 11, 2, 0, 30, 0, 0, chi)); got != 2 {
			t.Fatalf("DaysBetween over DST = %d, want 2", got)
		}
	}
}

func TestCutoffs(t *testing.T) {
	s := testSchedule()
	all := []Cutoff{{Minute: 18 * 60, MaxOrders: 2500}, {Minute: 11 * 60}}
	got := s.NextCutoffs(all, at(22, 14, 5), 3)
	want := []time.Time{at(22, 18, 0), at(23, 11, 0), at(23, 18, 0)}
	if len(got) != 3 {
		t.Fatalf("got %d cutoffs", len(got))
	}
	for i := range want {
		if !got[i].At.Equal(want[i]) {
			t.Errorf("cutoff %d = %v, want %v", i, got[i].At.In(cdt), want[i])
		}
	}
	if got[0].MaxOrders != 2500 || got[1].MaxOrders != 0 {
		t.Errorf("maxOrders must follow the slot: %+v", got)
	}
	// A cutoff exactly at t is included.
	if in := s.NextCutoffs(all, at(22, 18, 0), 1); !in[0].At.Equal(at(22, 18, 0)) {
		t.Errorf("a cutoff at t counts: %v", in[0].At)
	}
	// Closed days (saturday afternoon is open, sunday and the holiday are closed) have no cutoffs.
	if in := s.CutoffsOn(all, at(25, 10, 0)); len(in) != 0 {
		t.Errorf("sunday has none: %v", in)
	}
	if in := s.CutoffsOn(all, at(26, 10, 0)); len(in) != 0 {
		t.Errorf("a holiday has none: %v", in)
	}
	fri := []Cutoff{{Minute: 16 * 60, Days: []time.Weekday{time.Friday}}}
	next := s.NextCutoffs(fri, at(22, 9, 0), 2)
	if len(next) != 2 || !next[0].At.Equal(at(23, 16, 0)) || !next[1].At.Equal(at(30, 16, 0)) {
		t.Errorf("a friday-only cutoff: %v", next)
	}
	if len(s.NextCutoffs(nil, at(22, 9, 0), 2)) != 0 {
		t.Error("no cutoffs, none returned")
	}
}

func TestPreviousCutoff(t *testing.T) {
	s := testSchedule()
	all := []Cutoff{{Minute: 18 * 60}, {Minute: 11 * 60}}
	if p, ok := s.PreviousCutoff(all, at(23, 11, 0)); !ok || !p.Equal(at(22, 18, 0)) {
		t.Errorf("previous of fri 11:00 = %v", p)
	}
	if p, ok := s.PreviousCutoff(all, at(22, 18, 0)); !ok || !p.Equal(at(22, 11, 0)) {
		t.Errorf("strictly before: %v", p)
	}
	if p, ok := s.PreviousCutoff(all, at(27, 11, 0)); !ok || !p.Equal(at(24, 11, 0)) && !p.Equal(at(24, 18, 0)) {
		t.Errorf("over the holiday weekend: %v", p)
	}
	if _, ok := s.PreviousCutoff(nil, at(23, 11, 0)); ok {
		t.Error("no cutoffs, no previous")
	}
}

func TestParseClockAndWeekday(t *testing.T) {
	good := map[string]int{"00:00": 0, "06:30": 390, "23:59": 1439, "18:00": 1080}
	for in, want := range good {
		if got, err := ParseClock(in); err != nil || got != want {
			t.Errorf("ParseClock(%q) = %d, %v", in, got, err)
		}
		if Clock(want) != in {
			t.Errorf("Clock(%d) = %q", want, Clock(want))
		}
	}
	for _, in := range []string{"", "6:30", "24:00", "12:60", "12-30", "ab:cd", "12:3a", "1a:30", "123:45"} {
		if _, err := ParseClock(in); err == nil {
			t.Errorf("ParseClock(%q) must fail", in)
		}
	}
	if d, ok := ParseWeekday("Friday"); !ok || d != time.Friday {
		t.Error("weekday names are case-insensitive")
	}
	if _, ok := ParseWeekday("someday"); ok {
		t.Error("unknown weekday")
	}
}
