package offer

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func flag(b bool) *bool { return &b }

func consolidatedQuote(eligible *bool) Quote {
	q := quote()
	q.Tier = shared.TierConsolidated
	q.ConsolidationEligible = eligible
	return q
}

func TestAConsolidatedOfferCarriesConsolidationEligibleToTheEventAndTheSnapshot(t *testing.T) {
	for _, want := range []bool{true, false} {
		o, evs, err := New(consolidatedQuote(flag(want)), now)
		if err != nil {
			t.Fatal(err)
		}
		if got := o.ConsolidationEligible(); got == nil || *got != want {
			t.Fatalf("offer consolidationEligible = %v, want %v", got, want)
		}
		if got := evs[0].(DeliveryOfferQuoted).ConsolidationEligible; got == nil || *got != want {
			t.Fatalf("event consolidationEligible = %v, want %v", got, want)
		}
		raw, err := json.Marshal(o.Snapshot())
		if err != nil {
			t.Fatal(err)
		}
		var s Snapshot
		if err := json.Unmarshal(raw, &s); err != nil {
			t.Fatal(err)
		}
		back, err := Restore(s)
		if err != nil {
			t.Fatal(err)
		}
		if got := back.ConsolidationEligible(); got == nil || *got != want {
			t.Fatalf("restored consolidationEligible = %v, want %v", got, want)
		}
	}
}

func TestAConsolidatedOfferWithoutTheFlagKeepsItUnknown(t *testing.T) {
	o, _, err := New(consolidatedQuote(nil), now)
	if err != nil {
		t.Fatal(err)
	}
	if o.ConsolidationEligible() != nil {
		t.Fatal("an unknown flag stays nil, never false")
	}
}

func TestConsolidationEligibleBelongsToTheConsolidatedTierOnly(t *testing.T) {
	q := quote()
	q.ConsolidationEligible = flag(true)
	if _, _, err := New(q, now); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("STANDARD offer with the flag: err = %v, want ErrValidation", err)
	}
	o, _, err := New(consolidatedQuote(flag(true)), now)
	if err != nil {
		t.Fatal(err)
	}
	s := o.Snapshot()
	s.Tier = shared.TierStandard
	if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
		t.Fatalf("a stored STANDARD offer with the flag is corrupt: %v", err)
	}
}

func TestTheOfferDoesNotAliasTheFlagOfItsQuote(t *testing.T) {
	v := true
	o, _, err := New(consolidatedQuote(&v), now)
	if err != nil {
		t.Fatal(err)
	}
	v = false
	if got := o.ConsolidationEligible(); got == nil || !*got {
		t.Fatal("the offer keeps its own copy")
	}
}

func TestANominalConsolidatedOfferIsAllowed(t *testing.T) {
	q := consolidatedQuote(flag(true))
	q.Basis = shared.BasisNominal
	q.Witness = shared.Witness{NodeID: "N-AUS3"}
	if _, _, err := New(q, now); err != nil {
		t.Fatalf("CONSOLIDATED allows the nominal fallback: %v", err)
	}
}
