package report

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

// The KPI rules of ADR 0018. They are functions, not package variables, so the
// vocabularies cannot be mutated by a caller and mutation testing reaches them.

// Cause values of PromiseBroken (the as-built vocabulary, apis/asyncapi.yaml).
const (
	CauseLate     = "LATE"
	CauseLost     = "LOST"
	CauseReturned = "RETURNED"
)

// Causes is the PromiseBroken cause vocabulary; a cause outside it is poison.
func Causes() []string { return []string{CauseLate, CauseLost, CauseReturned} }

// IsCause reports whether cause is in Causes.
func IsCause(cause string) bool { return slices.Contains(Causes(), cause) }

// P50 and P90 are the continuous-percentile ranks of the window widths and of
// the lateness of the broken LATE promises.
func P50() float64 { return 0.5 }

// P90 is the 90th-percentile rank.
func P90() float64 { return 0.9 }

// UnknownTier is the tier a judgement reports under while the promise's
// PromiseMade has not been projected.
func UnknownTier() string { return "UNKNOWN" }

// UnknownBasis is the basis a judgement reports under while the promise's
// PromiseMade has not been projected.
func UnknownBasis() string { return "UNKNOWN" }

// VerdictOf is the verdict a Kind records, and false for the kinds that record
// none.
func VerdictOf(k Kind) (string, bool) {
	if k == KindPromiseKept {
		return VerdictKept, true
	}
	if k == KindPromiseBroken {
		return VerdictBroken, true
	}
	return "", false
}

// Window is what the projection derives from a promise's window when it applies
// PromiseMade: its width in minutes, whether it starts and ends on the same
// local calendar day, and whether that day was read in the window's own zone.
type Window struct {
	Minutes   float64
	ExactDay  bool
	ZoneKnown bool
}

// dayOf is the calendar date of t in loc.
func dayOf(t time.Time, loc *time.Location) string { return t.In(loc).Format(dayLayout) }

// WindowOf derives the Window of [earliest, latest] named in zone (ADR 0011:
// the IANA zone of the delivery station). An absent zone, or one the zone
// database cannot load, reads the window in UTC and is not ZoneKnown. "Local"
// is never a delivery station's zone (it would be the projector host's).
func WindowOf(earliest, latest time.Time, zone string) Window {
	loc, known := time.UTC, false
	if zone != "" && zone != "Local" {
		if l, err := time.LoadLocation(zone); err == nil {
			loc, known = l, true
		}
	}
	return Window{
		Minutes:   latest.Sub(earliest).Minutes(),
		ExactDay:  dayOf(earliest, loc) == dayOf(latest, loc),
		ZoneKnown: known,
	}
}

// ErrInvalidEvent marks a fact that can never be projected: a required field is
// missing or a value is outside its contract. It is deterministic, so the
// projector dead-letters it.
var ErrInvalidEvent = errors.New("report: invalid analytics event")

// Validate checks the fields every fact and the fields its Kind needs.
func (e Event) Validate() error {
	missing := firstMissing(
		required{"id", e.EventID != ""},
		required{"time", !e.At.IsZero()},
	)
	if missing == "" {
		missing = e.kindMissing()
	}
	if missing != "" {
		return fmt.Errorf("%w: %s %s: %s is required", ErrInvalidEvent, e.Kind, e.EventID, missing)
	}
	return nil
}

// kindMissing names the first missing field of the Kind's own payload, or
// rejects a Kind the projection does not know.
func (e Event) kindMissing() string {
	if e.Kind == KindDeliveryOfferQuoted {
		return firstMissing(required{"offerId", e.OfferID != ""}, required{"tier", e.Tier != ""})
	}
	if e.Kind == KindPromiseMade {
		return e.madeMissing()
	}
	if e.Kind == KindPromiseRevised {
		return firstMissing(required{"promiseId", e.PromiseID != ""}, required{"a revisionNo of at least 1", e.RevisionNo >= 1})
	}
	if e.Kind == KindPromiseBroken {
		return firstMissing(required{"promiseId", e.PromiseID != ""}, required{"a known cause", IsCause(e.Cause)},
			required{"a non-negative lateByMinutes", e.LateByMinutes >= 0})
	}
	if e.Kind == KindPromiseUnkeepable || e.Kind == KindPromiseKept {
		return firstMissing(required{"promiseId", e.PromiseID != ""})
	}
	return "a known kind"
}

// madeMissing names the first missing field of a PromiseMade.
func (e Event) madeMissing() string {
	return firstMissing(
		required{"promiseId", e.PromiseID != ""},
		required{"offerId", e.OfferID != ""},
		required{"tier", e.Tier != ""},
		required{"basis", e.Basis != ""},
		required{"deliverByEarliest", !e.DeliverByEarliest.IsZero()},
		required{"deliverByLatest", !e.DeliverByLatest.IsZero()},
		required{"a window that does not end before it starts", e.DeliverByLatest.Compare(e.DeliverByEarliest) >= 0},
	)
}

// required is one field check: name and whether it is present.
type required struct {
	name    string
	present bool
}

// firstMissing names the first absent field, or "" when every one is present.
func firstMissing(fields ...required) string {
	for _, f := range fields {
		if !f.present {
			return f.name
		}
	}
	return ""
}
