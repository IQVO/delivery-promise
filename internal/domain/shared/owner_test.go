package shared

import (
	"errors"
	"testing"
)

// TestChooseOwner is the OwnerChoice table of ADR 0017: operator first when it
// covers, else the covering seller with the most units, ties to the lowest
// ownerRef in byte order, and never a sum across owners.
func TestChooseOwner(t *testing.T) {
	cases := []struct {
		name   string
		stock  []OwnerStock
		qty    int
		owner  string
		covers bool
	}{
		{"no stock at all", nil, 1, "", false},
		{"operator alone covers", []OwnerStock{{"", 5}}, 5, "", true},
		{"operator alone falls short", []OwnerStock{{"", 4}}, 5, "", false},
		{"operator first even with less than a seller", []OwnerStock{{"S-B", 50}, {"", 5}}, 5, "", true},
		{"operator short: the covering seller", []OwnerStock{{"", 4}, {"S-B", 6}}, 5, "S-B", true},
		{"highest seller wins", []OwnerStock{{"S-A", 6}, {"S-C", 9}, {"S-B", 7}}, 5, "S-C", true},
		{"a non-covering seller never wins on units", []OwnerStock{{"S-A", 4}, {"S-B", 5}}, 5, "S-B", true},
		{"tie goes to the lowest ownerRef", []OwnerStock{{"S-B", 7}, {"S-A", 7}}, 5, "S-A", true},
		{"tie in byte order, upper before lower case", []OwnerStock{{"s-a", 7}, {"S-a", 7}}, 5, "S-a", true},
		{"tie in byte order, prefix first", []OwnerStock{{"S-AA", 7}, {"S-A", 7}}, 5, "S-A", true},
		{"a later higher seller beats an earlier tie", []OwnerStock{{"S-A", 7}, {"S-B", 7}, {"S-C", 8}}, 5, "S-C", true},
		{"two owners with 3 each never cover 5", []OwnerStock{{"S-A", 3}, {"S-B", 3}}, 5, "", false},
		{"operator and a seller never add up", []OwnerStock{{"", 3}, {"S-A", 3}}, 5, "", false},
		{"exactly the quantity covers", []OwnerStock{{"S-A", 5}}, 5, "S-A", true},
		{"zero units cover nothing", []OwnerStock{{"", 0}, {"S-A", 0}}, 1, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			owner, ok := ChooseOwner(c.stock, c.qty)
			if owner != c.owner || ok != c.covers {
				t.Fatalf("ChooseOwner(%v, %d) = (%q, %v), want (%q, %v)", c.stock, c.qty, owner, ok, c.owner, c.covers)
			}
		})
	}
}

// TestChooseOwnerIgnoresTheOrderOfTheEntries proves the rule is deterministic:
// every permutation of the same stock picks the same owner.
func TestChooseOwnerIgnoresTheOrderOfTheEntries(t *testing.T) {
	perms := [][]OwnerStock{
		{{"S-A", 7}, {"S-B", 7}, {"S-C", 6}},
		{{"S-B", 7}, {"S-C", 6}, {"S-A", 7}},
		{{"S-C", 6}, {"S-B", 7}, {"S-A", 7}},
	}
	for _, p := range perms {
		if owner, ok := ChooseOwner(p, 6); owner != "S-A" || !ok {
			t.Errorf("ChooseOwner(%v) = (%q, %v), want S-A", p, owner, ok)
		}
	}
}

func TestValidateLinesOwnerRef(t *testing.T) {
	if err := ValidateLines([]Line{{"a", 1, ""}, {"b", 1, "S-1"}}); err != nil {
		t.Fatalf("operator-owned and a seller line refused: %v", err)
	}
	if err := ValidateLines([]Line{{"a", 1, "-bad"}}); !errors.Is(err, ErrValidation) {
		t.Fatalf("a malformed ownerRef: err = %v, want ErrValidation", err)
	}
}

func TestSameItems(t *testing.T) {
	a := []Line{{"a", 1, ""}, {"b", 2, "S-1"}}
	cases := []struct {
		name string
		b    []Line
		want bool
	}{
		{"same owners", []Line{{"a", 1, ""}, {"b", 2, "S-1"}}, true},
		{"other owners", []Line{{"a", 1, "S-2"}, {"b", 2, ""}}, true},
		{"other order", []Line{{"b", 2, "S-1"}, {"a", 1, ""}}, false},
		{"other quantity", []Line{{"a", 1, ""}, {"b", 3, "S-1"}}, false},
		{"other item", []Line{{"a", 1, ""}, {"c", 2, "S-1"}}, false},
		{"shorter", []Line{{"a", 1, ""}}, false},
	}
	for _, c := range cases {
		if got := SameItems(a, c.b); got != c.want {
			t.Errorf("%s: SameItems = %v, want %v", c.name, got, c.want)
		}
	}
}
