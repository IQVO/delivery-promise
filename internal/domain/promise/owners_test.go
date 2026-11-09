package promise

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// operator is the stock of one item held only by the operator.
func operator(item string, qty int) map[string][]shared.OwnerStock {
	return map[string][]shared.OwnerStock{item: {{OwnerRef: shared.OperatorOwned, Available: qty}}}
}

func sellers(item string, stock ...shared.OwnerStock) map[string][]shared.OwnerStock {
	return map[string][]shared.OwnerStock{item: stock}
}

// TestMakeTakesTheOwnerTheRuleChoosesNow is ADR 0017 rule 5: MakePromise
// re-validates with the OwnerChoice rule. The quote chose S-A; S-A no longer
// covers but S-B does, so the promise takes S-B without a re-quote.
func TestMakeTakesTheOwnerTheRuleChoosesNow(t *testing.T) {
	in := making()
	in.Lines = []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2, OwnerRef: "S-A"}}
	in.Stock = sellers("ci-1001", shared.OwnerStock{OwnerRef: "S-A", Available: 1}, shared.OwnerStock{OwnerRef: "S-B", Available: 4})
	p, evs, err := Make(in, t0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "owner", p.Lines()[0].OwnerRef, "S-B")
	eq(t, "PromiseMade owner", evs[0].(PromiseMade).Lines[0].OwnerRef, "S-B")
	eq(t, "snapshot owner", p.Snapshot().Lines[0].OwnerRef, "S-B")
	eq(t, "the caller's lines are untouched", in.Lines[0].OwnerRef, "S-A")
	// the operator covers again: the rule picks it first
	in.Stock = sellers("ci-1001", shared.OwnerStock{OwnerRef: "S-A", Available: 9}, shared.OwnerStock{OwnerRef: "", Available: 2})
	p, _, err = Make(in, t0)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "operator owner", p.Lines()[0].OwnerRef, "")
}

// TestMakeNeverSumsOwners: two owners with 1 each do not cover a line of 2.
func TestMakeNeverSumsOwners(t *testing.T) {
	in := making()
	in.Stock = sellers("ci-1001", shared.OwnerStock{OwnerRef: "", Available: 1}, shared.OwnerStock{OwnerRef: "S-A", Available: 1})
	if _, _, err := Make(in, t0); !errors.Is(err, ErrGroupSpansNodes) {
		t.Fatalf("err = %v, want ErrGroupSpansNodes", err)
	}
}

func TestChooseOwnersPerLine(t *testing.T) {
	lines := []shared.Line{{CatalogItemID: "a", Quantity: 2}, {CatalogItemID: "b", Quantity: 3}}
	stock := map[string][]shared.OwnerStock{
		"a": {{OwnerRef: "", Available: 2}},
		"b": {{OwnerRef: "S-B", Available: 3}, {OwnerRef: "S-A", Available: 3}},
	}
	got, err := ChooseOwners(lines, stock)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "a", got[0].OwnerRef, "")
	eq(t, "b", got[1].OwnerRef, "S-A")
	eq(t, "input untouched", lines[1].OwnerRef, "")
	if _, err := ChooseOwners(lines, map[string][]shared.OwnerStock{"a": stock["a"]}); !errors.Is(err, ErrGroupSpansNodes) {
		t.Fatalf("an uncovered line: err = %v", err)
	}
}

func TestRehomeReownsTheLines(t *testing.T) {
	p := confirmed(t)
	w := shared.Witness{NodeID: "n2", PickupDepartureID: "d9", Legs: []string{"d2"}, DeliveryWaveID: "w1"}
	if err := p.Rehome(w, "n2:d9", []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2, OwnerRef: "S-9"}}); err != nil {
		t.Fatal(err)
	}
	eq(t, "owner", p.Lines()[0].OwnerRef, "S-9")
	eq(t, "node", p.Witness().NodeID, "n2")
	// nil keeps the owners
	if err := p.Rehome(w, "n2:d9", nil); err != nil {
		t.Fatal(err)
	}
	eq(t, "kept owner", p.Lines()[0].OwnerRef, "S-9")
}

func TestRehomeRefusesOtherItemsAndChangesNothing(t *testing.T) {
	bad := map[string][]shared.Line{
		"other quantity": {{CatalogItemID: "ci-1001", Quantity: 3}},
		"other item":     {{CatalogItemID: "ci-9", Quantity: 2}},
		"bad owner":      {{CatalogItemID: "ci-1001", Quantity: 2, OwnerRef: "-x"}},
		"no lines":       {},
	}
	for name, lines := range bad {
		p := confirmed(t)
		v := p.Version()
		err := p.Rehome(shared.Witness{NodeID: "n2", PickupDepartureID: "d9", DeliveryWaveID: "w1"}, "n2:d9", lines)
		if !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
		eq(t, name+" node", p.Witness().NodeID, "n1")
		eq(t, name+" version", p.Version(), v)
	}
}

func TestReviseReownsTheLines(t *testing.T) {
	p := atRisk(t)
	in := revising()
	in.Lines = []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2, OwnerRef: "S-2"}}
	if _, err := p.Revise(in, t0.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	eq(t, "owner", p.Lines()[0].OwnerRef, "S-2")
	bad := atRisk(t)
	in.Lines = []shared.Line{{CatalogItemID: "ci-1001", Quantity: 1}}
	if _, err := bad.Revise(in, t0.Add(2*time.Minute)); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("other quantities: err = %v", err)
	}
	eq(t, "state unchanged", bad.State(), StateAtRisk)
}
