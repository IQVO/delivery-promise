package shared

import (
	"slices"
	"time"
)

// Tier is the service tier of an offer or a promise (promise.Tier in the
// ubiquitous language).
type Tier string

// The four tiers of the model, fastest first.
const (
	TierSameDay  Tier = "SAME_DAY"
	TierNextDay  Tier = "NEXT_DAY"
	TierTwoDay   Tier = "TWO_DAY"
	TierStandard Tier = "STANDARD"
)

var allTiers = []Tier{TierSameDay, TierNextDay, TierTwoDay, TierStandard}

// AllTiers returns the four tiers, fastest first.
func AllTiers() []Tier { return slices.Clone(allTiers) }

// Valid reports whether t is one of the four tiers.
func (t Tier) Valid() bool { return slices.Contains(allTiers, t) }

// MaxCalendarDays is the model's service_tiers.max_calendar_days.
func (t Tier) MaxCalendarDays() int {
	switch t {
	case TierSameDay:
		return 0
	case TierNextDay:
		return 1
	case TierTwoDay:
		return 2
	default:
		return 7
	}
}

// AllowsNominal reports whether the tier may be promised from nominal lane
// times (service_tiers.nominal_fallback): only STANDARD.
func (t Tier) AllowsNominal() bool { return t == TierStandard }

// Rank orders the tiers fastest (0) to slowest (3); an invalid tier ranks -1.
func (t Tier) Rank() int { return slices.Index(allTiers, t) }

// SlowerOrEqual returns t and every slower tier, fastest first: the tiers a
// re-promise may fall back through.
func (t Tier) SlowerOrEqual() []Tier {
	i := t.Rank()
	if i < 0 {
		return nil
	}
	return slices.Clone(allTiers[i:])
}

// Basis says what a window was computed from.
type Basis string

// The two bases of the model.
const (
	BasisScheduled Basis = "SCHEDULED"
	BasisNominal   Basis = "NOMINAL"
)

// Valid reports whether b is SCHEDULED or NOMINAL.
func (b Basis) Valid() bool { return b == BasisScheduled || b == BasisNominal }

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

// Line is one line of a shipment group.
type Line struct {
	CatalogItemID string
	Quantity      int
}

// ValidateLines checks 1..MaxLines lines with valid, distinct item ids and a
// quantity between 1 and MaxQuantity.
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
	}
	return nil
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

// Window is a delivery window.
type Window struct {
	Earliest time.Time
	Latest   time.Time
}

// Validate checks both instants are set and earliest <= latest.
func (w Window) Validate() error {
	if w.Earliest.IsZero() || w.Latest.IsZero() || w.Latest.Before(w.Earliest) {
		return Invalid("a delivery window needs earliest <= latest")
	}
	return nil
}

// Witness is the concrete path that proves a window is feasible (WitnessPath).
// A NOMINAL witness carries no departure or wave ids (contracts-delta E8).
type Witness struct {
	NodeID            string
	PickupDepartureID string
	Legs              []string
	DeliveryWaveID    string
}

// Validate checks the witness against its basis.
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
		return Invalid("a SCHEDULED witness needs a pickup departure and a delivery wave")
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
