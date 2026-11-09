package search

import (
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func owner(ref string, qty int) shared.OwnerStock {
	return shared.OwnerStock{OwnerRef: ref, Available: qty}
}

// TestTwoOwnersNeverAddUpToOneLine is ADR 0017 rule 3: two sellers with 3
// units each at the only candidate node cannot cover a line of 5, so the node is
// no candidate and the quote is ITEM_QUANTITY_NOT_AVAILABLE.
func TestTwoOwnersNeverAddUpToOneLine(t *testing.T) {
	in := fixture().owners("N-AUS3", "ci-1001", owner("S-A", 3), owner("S-B", 3))
	in.Lines = []shared.Line{{CatalogItemID: "ci-1001", Quantity: 5}}
	r := Run(in)
	if len(r.Groups) != 0 {
		t.Fatalf("groups = %+v, want none", r.Groups)
	}
	for _, tier := range shared.AllTiers() {
		if r.WholeQuote[tier] != ItemQuantityNotAvailable {
			t.Errorf("%s: %q, want ITEM_QUANTITY_NOT_AVAILABLE", tier, r.WholeQuote[tier])
		}
	}
	// the operator and a seller do not add up either
	in = fixture().owners("N-AUS3", "ci-1001", owner("", 3), owner("S-A", 3))
	in.Lines = []shared.Line{{CatalogItemID: "ci-1001", Quantity: 5}}
	if r := Run(in); len(r.Groups) != 0 || r.WholeQuote[shared.TierNextDay] != ItemQuantityNotAvailable {
		t.Fatalf("operator 3 + seller 3 covered 5: %+v", r)
	}
}

// TestCandidateCarriesTheChosenOwner proves the candidate names, per line, the
// owner the OwnerChoice rule picks at its node.
func TestCandidateCarriesTheChosenOwner(t *testing.T) {
	cases := []struct {
		name  string
		stock []shared.OwnerStock
		qty   int
		want  string
	}{
		{"operator covers first", []shared.OwnerStock{owner("S-B", 30), owner("", 2)}, 2, ""},
		{"operator short: the biggest seller", []shared.OwnerStock{owner("", 1), owner("S-A", 6), owner("S-B", 8)}, 5, "S-B"},
		{"tied sellers: the lowest ownerRef", []shared.OwnerStock{owner("S-B", 8), owner("S-A", 8)}, 5, "S-A"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixture().owners("N-AUS3", "ci-1001", c.stock...)
			in.Lines = []shared.Line{{CatalogItemID: "ci-1001", Quantity: c.qty}}
			got := best(t, onlyGroup(t, Run(in)), shared.TierNextDay)
			want := []shared.Line{{CatalogItemID: "ci-1001", Quantity: c.qty, OwnerRef: c.want}}
			if !sameOwners(got.Lines, want) {
				t.Fatalf("candidate lines = %+v, want %+v", got.Lines, want)
			}
		})
	}
}

// TestEachLineHasItsOwnOwner proves the choice is per line: one group, two
// items, two owners.
func TestEachLineHasItsOwnOwner(t *testing.T) {
	in := fixture().owners("N-AUS3", "ci-1001", owner("", 5)).owners("N-AUS3", "ci-2002", owner("S-A", 1), owner("S-C", 4))
	in.Lines = []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}, {CatalogItemID: "ci-2002", Quantity: 3}}
	got := best(t, onlyGroup(t, Run(in)), shared.TierNextDay)
	want := []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}, {CatalogItemID: "ci-2002", Quantity: 3, OwnerRef: "S-C"}}
	if !sameOwners(got.Lines, want) {
		t.Fatalf("candidate lines = %+v, want %+v", got.Lines, want)
	}
	// the group's own lines are the caller's: no owner is written back
	if g := onlyGroup(t, Run(in)); g.Lines[1].OwnerRef != "" {
		t.Fatalf("group lines were mutated: %+v", g.Lines)
	}
}

// TestNilStockKnowsNoListing keeps a search without a stock lookup safe.
func TestNilStockKnowsNoListing(t *testing.T) {
	in := fixture()
	in.Stock = nil
	if r := Run(in); len(r.Groups) != 0 || r.WholeQuote[shared.TierNextDay] != ItemQuantityNotAvailable {
		t.Fatalf("a nil stock covered a line: %+v", r)
	}
}

func sameOwners(a, b []shared.Line) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
