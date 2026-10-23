// Package repository holds the non-interface vocabulary of the ports: filters
// and small result structs. The ports package is interfaces only.
package repository

import "time"

// HoldKey identifies the units a provisional promise holds: one item at one
// node.
type HoldKey struct {
	NodeID string
	ItemID string
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
