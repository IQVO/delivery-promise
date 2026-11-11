package shared

import (
	"slices"
	"testing"
)

func TestConsolidatedTierRules(t *testing.T) {
	c := TierConsolidated
	if !c.Valid() || c.Rank() != 4 || c.MaxCalendarDays() != MaxConsolidatedCalendarDays || MaxConsolidatedCalendarDays != 14 || !c.AllowsNominal() {
		t.Fatalf("CONSOLIDATED: valid=%v rank=%d days=%d nominal=%v", c.Valid(), c.Rank(), c.MaxCalendarDays(), c.AllowsNominal())
	}
	if got := c.SlowerOrEqual(); !slices.Equal(got, []Tier{TierConsolidated}) {
		t.Fatalf("SlowerOrEqual(CONSOLIDATED) = %v", got)
	}
}

func TestStandardNeverFallsThroughToConsolidated(t *testing.T) {
	if got := TierStandard.SlowerOrEqual(); !slices.Equal(got, []Tier{TierStandard}) {
		t.Fatalf("SlowerOrEqual(STANDARD) = %v", got)
	}
	for _, tier := range AllTiers() {
		if slices.Contains(tier.SlowerOrEqual(), TierConsolidated) {
			t.Fatalf("%s falls through to CONSOLIDATED", tier)
		}
	}
}

func TestTheDefaultQuoteSetStaysTheFourClassicTiers(t *testing.T) {
	if got := AllTiers(); len(got) != 4 || slices.Contains(got, TierConsolidated) {
		t.Fatalf("AllTiers() = %v", got)
	}
	known := KnownTiers()
	if len(known) != 5 || known[4] != TierConsolidated {
		t.Fatalf("KnownTiers() = %v", known)
	}
	known[0] = "X"
	if KnownTiers()[0] != TierSameDay {
		t.Fatal("KnownTiers must return a copy")
	}
}
