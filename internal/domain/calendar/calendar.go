// Package calendar is business-time arithmetic: operating hours per weekday,
// whole-day holidays and a time zone (the node's operational profile), and the
// node's pickup cutoffs. It is pure: every function takes the instants it
// works on.
//
// "ready = addBusinessTime(now, handlingTime, hours, holidays)" of the
// promise model is Schedule.AddBusinessMinutes; the latest instant a handling
// can still start so that it ends by a cutoff is Schedule.SubBusinessMinutes.
package calendar

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// Horizon bounds every day-by-day walk, in days.
const Horizon = 400

// DayHours are the opening hours of one weekday, in minutes since local
// midnight. Start < End.
type DayHours struct {
	Start int
	End   int
}

// Schedule is the operating calendar of one building.
type Schedule struct {
	Loc      *time.Location
	Hours    map[time.Weekday]DayHours
	Holidays map[string]bool // local dates, YYYY-MM-DD
}

var weekdays = map[string]time.Weekday{
	"sunday": time.Sunday, "monday": time.Monday, "tuesday": time.Tuesday, "wednesday": time.Wednesday,
	"thursday": time.Thursday, "friday": time.Friday, "saturday": time.Saturday,
}

// ParseWeekday maps a lower-case English weekday name to a time.Weekday.
func ParseWeekday(s string) (time.Weekday, bool) {
	d, ok := weekdays[strings.ToLower(s)]
	return d, ok
}

// ParseClock parses a local time of day "HH:mm" into minutes since midnight.
func ParseClock(s string) (int, error) {
	if len(s) != 5 || s[2] != ':' {
		return 0, fmt.Errorf("clock %q is not HH:mm", s)
	}
	h, m := 0, 0
	for i, c := range []byte(s) {
		if i == 2 {
			continue
		}
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("clock %q is not HH:mm", s)
		}
		if i < 2 {
			h = h*10 + int(c-'0')
		} else {
			m = m*10 + int(c-'0')
		}
	}
	if h > 23 || m > 59 {
		return 0, fmt.Errorf("clock %q is out of range", s)
	}
	return h*60 + m, nil
}

// Clock formats minutes since midnight as "HH:mm".
func Clock(min int) string { return fmt.Sprintf("%02d:%02d", min/60, min%60) }

func dateKey(t time.Time) string { return t.Format("2006-01-02") }

// at returns the instant of minute-of-day min on the local date of day.
func (s Schedule) at(day time.Time, min int) time.Time {
	y, m, d := day.In(s.Loc).Date()
	return time.Date(y, m, d, min/60, min%60, 0, 0, s.Loc)
}

// nextDay returns noon of the local day after (delta=1) or before (-1) day;
// noon keeps the date arithmetic clear of daylight-saving jumps.
func (s Schedule) shiftDay(day time.Time, delta int) time.Time {
	y, m, d := day.In(s.Loc).Date()
	return time.Date(y, m, d+delta, 12, 0, 0, 0, s.Loc)
}

// Open returns the opening hours of the local date of day, and false when the
// building is closed that day (no hours, or a holiday).
func (s Schedule) Open(day time.Time) (DayHours, bool) {
	local := day.In(s.Loc)
	if s.Holidays[dateKey(local)] {
		return DayHours{}, false
	}
	h, ok := s.Hours[local.Weekday()]
	return h, ok && h.End > h.Start
}

// AddBusinessMinutes returns the instant at which minutes of operating time
// have elapsed after from. Time outside the opening hours and on holidays
// does not count. It returns false when the building never opens within the
// horizon.
func (s Schedule) AddBusinessMinutes(from time.Time, minutes int) (time.Time, bool) {
	cur := from.In(s.Loc)
	left := time.Duration(minutes) * time.Minute
	day := cur
	for i := 0; i < Horizon; i++ {
		var end time.Time
		var done bool
		left, end, done = s.spendForward(day, cur, left)
		if done {
			return end, true
		}
		day = s.shiftDay(day, 1)
		cur = s.at(day, 0)
	}
	return time.Time{}, false
}

// spendForward spends left operating time inside the day of `day`, starting at
// cur. It returns what is left, and the end instant when the time ran out that
// day.
func (s Schedule) spendForward(day, cur time.Time, left time.Duration) (time.Duration, time.Time, bool) {
	h, ok := s.Open(day)
	if !ok {
		return left, time.Time{}, false
	}
	open, closeAt := s.at(day, h.Start), s.at(day, h.End)
	if cur.Before(open) {
		cur = open
	}
	if !cur.Before(closeAt) {
		return left, time.Time{}, false
	}
	avail := closeAt.Sub(cur)
	if left <= avail {
		return left, cur.Add(left).UTC(), true
	}
	return left - avail, time.Time{}, false
}

// SubBusinessMinutes is the inverse of AddBusinessMinutes: the latest instant
// t such that AddBusinessMinutes(t, minutes) is not after to. It returns false
// when no such instant exists within the horizon.
func (s Schedule) SubBusinessMinutes(to time.Time, minutes int) (time.Time, bool) {
	cur := to.In(s.Loc)
	left := time.Duration(minutes) * time.Minute
	day := cur
	for i := 0; i < Horizon; i++ {
		var start time.Time
		var done bool
		left, start, done = s.spendBackward(day, cur, left)
		if done {
			return start, true
		}
		day = s.shiftDay(day, -1)
		cur = s.at(day, 24*60-1).Add(time.Minute)
	}
	return time.Time{}, false
}

// spendBackward is spendForward walking back from cur to the opening.
func (s Schedule) spendBackward(day, cur time.Time, left time.Duration) (time.Duration, time.Time, bool) {
	h, ok := s.Open(day)
	if !ok {
		return left, time.Time{}, false
	}
	open, closeAt := s.at(day, h.Start), s.at(day, h.End)
	if cur.After(closeAt) {
		cur = closeAt
	}
	if !cur.After(open) {
		return left, time.Time{}, false
	}
	avail := cur.Sub(open)
	if left <= avail {
		return left, cur.Add(-left).UTC(), true
	}
	return left - avail, time.Time{}, false
}

// OperatingMinutesBetween returns the minutes of operating time in [from, to).
func (s Schedule) OperatingMinutesBetween(from, to time.Time) int {
	if !to.After(from) {
		return 0
	}
	total := time.Duration(0)
	day := from
	for i := 0; i < Horizon; i++ {
		if s.at(day, 0).After(to) {
			break
		}
		if h, ok := s.Open(day); ok {
			lo, hi := s.at(day, h.Start), s.at(day, h.End)
			if from.After(lo) {
				lo = from
			}
			if to.Before(hi) {
				hi = to
			}
			if hi.After(lo) {
				total += hi.Sub(lo)
			}
		}
		day = s.shiftDay(day, 1)
	}
	return int(total / time.Minute)
}

// DayMinutes returns the opening minutes of the local date of day (0 closed).
func (s Schedule) DayMinutes(day time.Time) int {
	h, ok := s.Open(day)
	if !ok {
		return 0
	}
	return h.End - h.Start
}

// LocalDate returns the local calendar date of t at 12:00, a stable value to
// compare and subtract dates with.
func (s Schedule) LocalDate(t time.Time) time.Time { return s.shiftDay(t, 0) }

// DaysBetween returns the whole calendar days from the local date of from to
// the local date of to (negative when to is earlier).
func (s Schedule) DaysBetween(from, to time.Time) int {
	a, b := s.LocalDate(from), s.LocalDate(to)
	return int(b.Sub(a).Round(24*time.Hour) / (24 * time.Hour))
}

// Cutoff is one pickup cutoff slot of a profile.
type Cutoff struct {
	Minute    int            // minutes since local midnight
	Days      []time.Weekday // empty means every operating day
	MaxOrders int            // 0 means unset
}

// Instance is a cutoff on a concrete date.
type Instance struct {
	At        time.Time
	MaxOrders int
}

func (c Cutoff) appliesOn(s Schedule, day time.Time) bool {
	if _, ok := s.Open(day); !ok {
		return false
	}
	return len(c.Days) == 0 || slices.Contains(c.Days, day.In(s.Loc).Weekday())
}

// CutoffsOn returns the cutoff instances of the local date of day, earliest
// first. A closed day has none.
func (s Schedule) CutoffsOn(cutoffs []Cutoff, day time.Time) []Instance {
	var out []Instance
	for _, c := range cutoffs {
		if c.appliesOn(s, day) {
			out = append(out, Instance{At: s.at(day, c.Minute).UTC(), MaxOrders: c.MaxOrders})
		}
	}
	slices.SortFunc(out, func(a, b Instance) int { return a.At.Compare(b.At) })
	return out
}

// NextCutoffs returns up to n cutoff instances at or after t, earliest first.
func (s Schedule) NextCutoffs(cutoffs []Cutoff, t time.Time, n int) []Instance {
	var out []Instance
	day := t
	for i := 0; i < Horizon && len(out) < n; i++ {
		for _, in := range s.CutoffsOn(cutoffs, day) {
			if !in.At.Before(t) && len(out) < n {
				out = append(out, in)
			}
		}
		day = s.shiftDay(day, 1)
	}
	return out
}

// PreviousCutoff returns the latest cutoff instant strictly before t.
func (s Schedule) PreviousCutoff(cutoffs []Cutoff, t time.Time) (time.Time, bool) {
	day := t
	for i := 0; i < Horizon; i++ {
		in := s.CutoffsOn(cutoffs, day)
		for j := len(in) - 1; j >= 0; j-- {
			if in[j].At.Before(t) {
				return in[j].At, true
			}
		}
		day = s.shiftDay(day, -1)
	}
	return time.Time{}, false
}
