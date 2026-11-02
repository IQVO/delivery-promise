package calendar

import (
	"testing"
	"time"
)

func everyDay(start, end int) map[time.Weekday]DayHours {
	m := map[time.Weekday]DayHours{}
	for d := time.Sunday; d <= time.Saturday; d++ {
		m[d] = DayHours{Start: start, End: end}
	}
	return m
}

// TestADayWhoseWindowIsEmptyIsClosed pins Open on a day whose hours end where
// they start: it is closed, and DayMinutes counts nothing.
func TestADayWhoseWindowIsEmptyIsClosed(t *testing.T) {
	s := Schedule{Loc: cdt, Hours: map[time.Weekday]DayHours{time.Wednesday: {Start: 9 * 60, End: 9 * 60}}}
	if _, ok := s.Open(at(21, 12, 0)); ok { // Wednesday 2026-10-21
		t.Fatal("an empty window is a closed day")
	}
	if s.DayMinutes(at(21, 12, 0)) != 0 {
		t.Fatal("an empty window has no operating minutes")
	}
}

// TestSubBusinessMinutesExactFitSpendsOnlyThatDay: 480 minutes back from the
// 17:00 close of a 09:00-17:00 day lands exactly on the 09:00 opening of the
// same day, without reaching into the previous day.
func TestSubBusinessMinutesExactFitSpendsOnlyThatDay(t *testing.T) {
	s := Schedule{Loc: cdt, Hours: everyDay(9*60, 17*60)}
	got, ok := s.SubBusinessMinutes(at(22, 17, 0), 480)
	if !ok || !got.Equal(at(22, 9, 0)) {
		t.Fatalf("exact fit = %v %v, want %v true", got, ok, at(22, 9, 0))
	}
}

// TestSubBusinessMinutesAcrossMidnightWindows walks back over a day whose
// window ends at 25:00 (01:00 the next day): after the 60 minutes of Tuesday
// are spent, the remaining minute comes from Monday's window, landing 23:59.
func TestSubBusinessMinutesAcrossMidnightWindows(t *testing.T) {
	late := DayHours{Start: 23 * 60, End: 25 * 60}
	s := Schedule{Loc: cdt, Hours: map[time.Weekday]DayHours{time.Monday: late, time.Tuesday: late}}
	got, ok := s.SubBusinessMinutes(at(21, 0, 0), 61) // Wednesday 00:00
	if !ok || !got.Equal(at(19, 23, 59)) {
		t.Fatalf("61 minutes back = %v %v, want Monday 23:59", got, ok)
	}
}

// TestAddBusinessMinutesRunsOutAtTheHorizon: with one operating minute every
// Saturday, 57 minutes are reachable within the 400-day horizon and the 58th
// (the Saturday of day 400) is not.
func TestAddBusinessMinutesRunsOutAtTheHorizon(t *testing.T) {
	s := Schedule{Loc: cdt, Hours: map[time.Weekday]DayHours{time.Saturday: {Start: 0, End: 1}}}
	if _, ok := s.AddBusinessMinutes(at(23, 12, 0), 57); !ok { // Friday start
		t.Fatal("57 Saturdays are inside the horizon")
	}
	if _, ok := s.AddBusinessMinutes(at(23, 12, 0), 58); ok {
		t.Fatal("the 58th Saturday is past the horizon")
	}
}

// TestSubBusinessMinutesRunsOutAtTheHorizon is the backward mirror: from a
// Sunday, the 58th one-minute Saturday backwards sits exactly on day 400.
func TestSubBusinessMinutesRunsOutAtTheHorizon(t *testing.T) {
	s := Schedule{Loc: cdt, Hours: map[time.Weekday]DayHours{time.Saturday: {Start: 0, End: 1}}}
	if _, ok := s.SubBusinessMinutes(at(25, 12, 0), 57); !ok { // Sunday start
		t.Fatal("57 Saturdays are inside the horizon")
	}
	if _, ok := s.SubBusinessMinutes(at(25, 12, 0), 58); ok {
		t.Fatal("the 58th Saturday back is past the horizon")
	}
}

// TestNextCutoffsReturnsAtMostN pins the count even when the same day carries
// a later cutoff: asking for one from 09:00 yields the 11:00 alone.
func TestNextCutoffsReturnsAtMostN(t *testing.T) {
	s := Schedule{Loc: cdt, Hours: everyDay(6*60, 22*60)}
	cutoffs := []Cutoff{{Minute: 11 * 60}, {Minute: 18 * 60}}
	got := s.NextCutoffs(cutoffs, at(22, 9, 0), 1)
	if len(got) != 1 || !got[0].At.Equal(at(22, 11, 0)) {
		t.Fatalf("next cutoffs = %+v, want just the 11:00", got)
	}
}

// TestNextCutoffsWeeklyCutoffStopsAtTheHorizon: a Friday-only cutoff from a
// Thursday reaches 57 Fridays inside the 400-day horizon, not 58.
func TestNextCutoffsWeeklyCutoffStopsAtTheHorizon(t *testing.T) {
	s := Schedule{Loc: cdt, Hours: map[time.Weekday]DayHours{time.Friday: {Start: 0, End: 24*60 - 1}}}
	cutoffs := []Cutoff{{Minute: 18 * 60, Days: []time.Weekday{time.Friday}}}
	if got := s.NextCutoffs(cutoffs, at(22, 14, 0), 58); len(got) != 57 {
		t.Fatalf("weekly cutoffs inside the horizon = %d, want 57", len(got))
	}
}

// TestDaysBetweenCountsWholeLocalDays pins the day arithmetic both ways.
func TestDaysBetweenCountsWholeLocalDays(t *testing.T) {
	s := Schedule{Loc: cdt}
	if got := s.DaysBetween(at(22, 9, 0), at(24, 9, 0)); got != 2 {
		t.Fatalf("two days forward = %d", got)
	}
	if got := s.DaysBetween(at(24, 9, 0), at(22, 9, 0)); got != -2 {
		t.Fatalf("two days back = %d", got)
	}
}
