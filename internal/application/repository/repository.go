// Package repository holds the non-interface vocabulary of the ports: filters
// and small result structs. The ports package is interfaces only.
package repository

import "time"

// HoldKey identifies the units a hold nets: one item of one owner at one node
// (ADR 0017; OwnerRef "" is operator-owned). It is also the key of an
// AvailabilityView figure.
type HoldKey struct {
	NodeID   string
	ItemID   string
	OwnerRef string
}

// ConfirmedHold is the units one active (CONFIRMED or AT_RISK) promise has
// ordered of one item of one owner at the node of its witness, and when it was
// confirmed.
type ConfirmedHold struct {
	Key         HoldKey
	Qty         int
	ConfirmedAt time.Time
}

// BookFilter selects books for the REST list and the clock jobs.
type BookFilter struct {
	// NodeIDs restricts the books to these nodes; empty means every node.
	NodeIDs []string
	// CutoffFrom and CutoffUntil bound cutoffAt, [from, until).
	CutoffFrom  time.Time
	CutoffUntil time.Time
}

// PromiseRiskFilter selects the active (CONFIRMED or AT_RISK) promises whose
// witness path uses a departure or a wave.
type PromiseRiskFilter struct {
	DepartureID string
	WaveID      string
}
