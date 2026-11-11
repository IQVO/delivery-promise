package shared

import (
	"slices"
	"time"
)

// Tier is the service tier of an offer or a promise (promise.Tier in the
// ubiquitous language).
type Tier string

// The tiers of the model: the four classic ones, fastest first, then the
// opt-in CONSOLIDATED tier (ADR 0024).
const (
	TierSameDay      Tier = "SAME_DAY"
	TierNextDay      Tier = "NEXT_DAY"
	TierTwoDay       Tier = "TWO_DAY"
	TierStandard     Tier = "STANDARD"
	TierConsolidated Tier = "CONSOLIDATED"
)

// MaxConsolidatedCalendarDays is the horizon of the CONSOLIDATED tier: its
// window may end up to this many local days after the quote (ADR 0024).
const MaxConsolidatedCalendarDays = 14

var (
	allTiers   = []Tier{TierSameDay, TierNextDay, TierTwoDay, TierStandard}
	knownTiers = []Tier{TierSameDay, TierNextDay, TierTwoDay, TierStandard, TierConsolidated}
)

// AllTiers returns the four classic tiers, fastest first: the default quote
// set. CONSOLIDATED is never part of it; a caller opts in (ADR 0024).
func AllTiers() []Tier { return slices.Clone(allTiers) }

// KnownTiers returns the five tiers, fastest first, CONSOLIDATED last.
func KnownTiers() []Tier { return slices.Clone(knownTiers) }

// Valid reports whether t is one of the five tiers.
func (t Tier) Valid() bool { return slices.Contains(knownTiers, t) }

// MaxCalendarDays is the model's service_tiers.max_calendar_days.
func (t Tier) MaxCalendarDays() int {
	switch t {
	case TierSameDay:
		return 0
	case TierNextDay:
		return 1
	case TierTwoDay:
		return 2
	case TierConsolidated:
		return MaxConsolidatedCalendarDays
	default:
		return 7
	}
}

// AllowsNominal reports whether the tier may be promised from nominal lane
// times (service_tiers.nominal_fallback): STANDARD and CONSOLIDATED.
func (t Tier) AllowsNominal() bool { return t == TierStandard || t == TierConsolidated }

// Rank orders the tiers fastest (0) to slowest (4); an invalid tier ranks -1.
func (t Tier) Rank() int { return slices.Index(knownTiers, t) }

// SlowerOrEqual returns t and every slower tier, fastest first: the tiers a
// re-promise may fall back through. STANDARD and CONSOLIDATED fall through to
// nothing but themselves: a standard promise never becomes a consolidated one
// (ADR 0024).
func (t Tier) SlowerOrEqual() []Tier {
	if t == TierConsolidated {
		return []Tier{TierConsolidated}
	}
	i := slices.Index(allTiers, t)
	if i < 0 {
		return nil
	}
	return slices.Clone(allTiers[i:])
}

// Basis says what a window was computed from.
type Basis string

// The three bases of the model.
const (
	BasisScheduled Basis = "SCHEDULED"
	BasisNominal   Basis = "NOMINAL"
	BasisMeasured  Basis = "MEASURED"
)

// Valid reports whether b is SCHEDULED, NOMINAL or MEASURED.
func (b Basis) Valid() bool {
	return b == BasisScheduled || b == BasisNominal || b == BasisMeasured
}

// Domain bounds. They are plain literals on purpose (ADR 0002, A29).
const (
	// MaxQuantity is the Postgres integer ceiling for a line quantity.
	MaxQuantity = 2147483647
	// MaxLines bounds the lines of one shipment group.
	MaxLines = 100
	// MaxPostalPrefix bounds a destination postal prefix.
	MaxPostalPrefix = 10
	// MaxReason bounds a free-text reason.
	MaxReason = 200
)

// Line is one line of a shipment group. OwnerRef is the owner whose stock
// covers the line at the witness node (the OwnerChoice of ADR 0017):
// OperatorOwned ("") for an operator-owned listing, and on every line a caller
// sends, where the owner is not the caller's to choose.
type Line struct {
	CatalogItemID string
	Quantity      int
	OwnerRef      string
}

// ValidateLines checks 1..MaxLines lines with valid, distinct item ids, a
// quantity between 1 and MaxQuantity and an ownerRef that is empty or a valid
// id.
func ValidateLines(lines []Line) error {
	if len(lines) == 0 || len(lines) > MaxLines {
		return Invalid("a group needs between 1 and %d lines", MaxLines)
	}
	seen := make(map[string]bool, len(lines))
	for _, l := range lines {
		if !ValidID(l.CatalogItemID) {
			return Invalid("catalogItemId %q is not a valid id", l.CatalogItemID)
		}
		if seen[l.CatalogItemID] {
			return Invalid("catalogItemId %q appears twice", l.CatalogItemID)
		}
		seen[l.CatalogItemID] = true
		if l.Quantity < 1 || l.Quantity > MaxQuantity {
			return Invalid("quantity of %q must be between 1 and %d", l.CatalogItemID, MaxQuantity)
		}
		if l.OwnerRef != OperatorOwned && !ValidID(l.OwnerRef) {
			return Invalid("ownerRef %q of %q is not a valid id", l.OwnerRef, l.CatalogItemID)
		}
	}
	return nil
}

// SameItems reports whether a and b hold the same items and quantities in the
// same order, whatever their owners: the lines of one group re-netted under
// another owner choice (ADR 0017).
func SameItems(a, b []Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].CatalogItemID != b[i].CatalogItemID || a[i].Quantity != b[i].Quantity {
			return false
		}
	}
	return true
}

// SameLines reports whether a and b hold the same items and quantities,
// whatever the order.
func SameLines(a, b []Line) bool {
	if len(a) != len(b) {
		return false
	}
	m := make(map[string]int, len(a))
	for _, l := range a {
		m[l.CatalogItemID] = l.Quantity
	}
	for _, l := range b {
		if q, ok := m[l.CatalogItemID]; !ok || q != l.Quantity {
			return false
		}
	}
	return true
}

// ValidPostalPrefix reports whether s is 1..MaxPostalPrefix ASCII letters
// or digits.
func ValidPostalPrefix(s string) bool {
	if s == "" || len(s) > MaxPostalPrefix {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isAlnum(s[i]) {
			return false
		}
	}
	return true
}

// Window is a delivery window: two instants and the IANA time zone of the
// delivery station serving the destination (ADR 0011). The zone names the
// local calendar the window's days belong to; it is empty only on a window
// persisted before the zone was carried.
type Window struct {
	Earliest time.Time
	Latest   time.Time
	TimeZone string
}

// MaxTimeZone is the longest accepted IANA time zone name.
const MaxTimeZone = 64

// Validate checks both instants are set, earliest <= latest, and that the
// time zone, when present, is a well-formed IANA name.
func (w Window) Validate() error {
	if w.Earliest.IsZero() || w.Latest.IsZero() || w.Latest.Before(w.Earliest) {
		return Invalid("a delivery window needs earliest <= latest")
	}
	if w.TimeZone != "" && !ValidTimeZone(w.TimeZone) {
		return Invalid("a delivery window time zone %q is not an IANA name", w.TimeZone)
	}
	return nil
}

// UTC returns the window with both instants in UTC and the zone kept.
func (w Window) UTC() Window {
	return Window{Earliest: UTC(w.Earliest), Latest: UTC(w.Latest), TimeZone: w.TimeZone}
}

// ValidTimeZone reports whether s has the shape of an IANA time zone name:
// 1..MaxTimeZone ASCII letters, digits, '/', '_', '-' or '+', neither starting
// nor ending with '/'. It does not consult a zone database.
func ValidTimeZone(s string) bool {
	if s == "" || len(s) > MaxTimeZone || s[0] == '/' || s[len(s)-1] == '/' {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !isAlnum(c) && c != '/' && c != '_' && c != '-' && c != '+' {
			return false
		}
	}
	return true
}

// Witness is the concrete path that proves a window is feasible (WitnessPath).
// A NOMINAL witness carries no departure or wave ids (contracts-delta E8).
type Witness struct {
	NodeID            string
	PickupDepartureID string
	Legs              []string
	DeliveryWaveID    string
}

// Validate checks the witness against its basis. A MEASURED witness carries
// the same path as a SCHEDULED one (ADR 0014); only NOMINAL is empty
// (contracts-delta E8).
func (w Witness) Validate(b Basis) error {
	if !ValidID(w.NodeID) {
		return Invalid("witness nodeId %q is not a valid id", w.NodeID)
	}
	if b == BasisNominal {
		if w.PickupDepartureID != "" || w.DeliveryWaveID != "" || len(w.Legs) > 0 {
			return Invalid("a NOMINAL witness carries no departure or wave ids")
		}
		return nil
	}
	if !ValidID(w.PickupDepartureID) || !ValidID(w.DeliveryWaveID) {
		return Invalid("a %s witness needs a pickup departure and a delivery wave", b)
	}
	for _, l := range w.Legs {
		if !ValidID(l) {
			return Invalid("witness leg %q is not a valid id", l)
		}
	}
	return nil
}

// Clone returns a deep copy.
func (w Witness) Clone() Witness {
	w.Legs = slices.Clone(w.Legs)
	return w
}
