// Package requestfact holds the pure rules of the request-level promise
// coverage fact (ADR 0032): what one answered delivery request (a quote or an
// estimate) reduces to, and the analytics-only event that carries it. It is a
// best-effort fact, not an aggregate: nothing here is stored by the request
// path, and its loss never changes a response.
package requestfact

import (
	"slices"
	"time"
)

// Kind is which request the fact describes.
type Kind string

// The two requests that answer a delivery date.
const (
	KindQuote    Kind = "QUOTE"
	KindEstimate Kind = "ESTIMATE"
)

// Valid reports whether k is one of the two kinds.
func (k Kind) Valid() bool { return k == KindQuote || k == KindEstimate }

// regionWidth is how many leading characters of the destination postal prefix
// the fact keeps: it never carries more of the destination than that.
func regionWidth() int { return 2 }

// RegionOf is the region of a destination postal prefix: its first two
// characters, as sent (all of it when shorter).
func RegionOf(prefix string) string {
	r := []rune(prefix)
	return string(r[:min(regionWidth(), len(r))])
}

// Option is one promised option of the answer: the latest delivery instant of
// its window and the IANA zone whose calendar the window's days belong to
// (ADR 0011; empty when unknown).
type Option struct {
	Latest   time.Time
	TimeZone string
}

// Fact is the small value the request path hands over once per answered
// request: the destination region (two characters, no address, no shopper),
// the kind of request, how many items it asked about, whether any promise
// came back, the shortest promised days across the answered options (nil when
// none) and the instant the request was answered.
type Fact struct {
	Region       string
	Kind         Kind
	Items        int
	Answered     bool
	ShortestDays *int
	At           time.Time
}

// New reduces an answered request to its Fact. ok is false when the request
// cannot be described: an unknown kind, no destination region, no item or no
// answer instant. Options without a latest delivery are not promises and are
// ignored.
func New(kind Kind, destinationPrefix string, items int, options []Option, at time.Time) (Fact, bool) {
	region := RegionOf(destinationPrefix)
	if !kind.Valid() || region == "" || items < 1 || at.IsZero() {
		return Fact{}, false
	}
	f := Fact{Region: region, Kind: kind, Items: items, At: at.UTC()}
	var promised []int
	for _, o := range options {
		if days := PromisedDays(at, o.Latest, o.TimeZone); days != nil {
			promised = append(promised, *days)
		}
	}
	if len(promised) > 0 {
		shortest := slices.Min(promised)
		f.Answered, f.ShortestDays = true, &shortest
	}
	return f, true
}

// hoursPerDay converts a whole number of hours between two local midnights to
// days.
func hoursPerDay() int { return 24 }

// localMidnight is the start of the calendar day t falls on in loc, expressed
// as a UTC instant so two of them differ by a whole number of days.
func localMidnight(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// zoneOf loads the IANA zone of a delivery station; an absent zone, "Local"
// (the host's, never a station's) or one the zone database cannot load is UTC.
func zoneOf(zone string) *time.Location {
	if zone == "" || zone == "Local" {
		return time.UTC
	}
	loc, err := time.LoadLocation(zone)
	if err != nil {
		return time.UTC
	}
	return loc
}

// PromisedDays is the number of local calendar days from the day of the answer
// to the day of the latest delivery, both read in zone, never below 0. nil when
// there is no latest delivery: it is not a promise. It is the same rule as the
// analytics read side applies to DeliveryOfferQuoted (ADR 0030, section 3).
func PromisedDays(answeredAt, latest time.Time, zone string) *int {
	if latest.IsZero() {
		return nil
	}
	loc := zoneOf(zone)
	hours := int(localMidnight(latest, loc).Sub(localMidnight(answeredAt, loc)).Hours())
	days := max(0, hours/hoursPerDay())
	return &days
}
