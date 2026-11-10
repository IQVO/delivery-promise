package promise

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func consolidatedMaking(eligible *bool) Making {
	in := making()
	in.Terms.Tier = shared.TierConsolidated
	in.Terms.ConsolidationEligible = eligible
	return in
}

func flagOf(b bool) *bool { return &b }

func TestAConsolidatedPromiseCarriesConsolidationEligibleToTheEventAndTheSnapshot(t *testing.T) {
	for _, want := range []bool{true, false} {
		p, evs, err := Make(consolidatedMaking(flagOf(want)), t0)
		if err != nil {
			t.Fatal(err)
		}
		if got := evs[0].(PromiseMade).Terms.ConsolidationEligible; got == nil || *got != want {
			t.Fatalf("event consolidationEligible = %v, want %v", got, want)
		}
		raw, err := json.Marshal(p.Snapshot())
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
		if got := back.Terms().ConsolidationEligible; got == nil || *got != want || back.Terms().Tier != shared.TierConsolidated {
			t.Fatalf("restored consolidationEligible = %v, want %v", got, want)
		}
	}
}

func TestConsolidationEligibleIsOmittedOnEveryOtherTier(t *testing.T) {
	p, _, err := Make(making(), t0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Terms().ConsolidationEligible != nil {
		t.Fatal("a NEXT_DAY promise has no consolidationEligible")
	}
	in := making()
	in.Terms.ConsolidationEligible = flagOf(true)
	if _, _, err := Make(in, t0); !errors.Is(err, ErrInvalidPromiseTerms) {
		t.Fatalf("the flag on NEXT_DAY terms: err = %v, want ErrInvalidPromiseTerms", err)
	}
}

func TestAConsolidatedPromiseMayBeNominal(t *testing.T) {
	in := consolidatedMaking(flagOf(true))
	in.Terms.Basis = shared.BasisNominal
	in.Witness = shared.Witness{NodeID: "n1"}
	if _, _, err := Make(in, t0); err != nil {
		t.Fatalf("CONSOLIDATED allows the nominal fallback: %v", err)
	}
}

func TestARevisionOfAConsolidatedPromiseNeverBecomesFaster(t *testing.T) {
	in := consolidatedMaking(flagOf(true))
	p, _, err := Make(in, t0)
	if err != nil {
		t.Fatal(err)
	}
	mustEvents(t, 1)(p.Confirm("ord-1", 1, t0))
	mustEvents(t, 1)(p.FlagAtRisk("late linehaul", t0))
	faster := in.Terms
	faster.Tier, faster.ConsolidationEligible = shared.TierStandard, nil
	faster.Window.Earliest, faster.Window.Latest = faster.Window.Earliest.Add(time.Hour), faster.Window.Latest.Add(time.Hour)
	_, err = p.Revise(Revising{Terms: faster, Witness: in.Witness, BookID: in.BookID, Reason: "late linehaul", Lines: in.Lines}, t0)
	if !errors.Is(err, ErrInvalidPromiseTerms) {
		t.Fatalf("CONSOLIDATED to STANDARD is faster: err = %v", err)
	}
}
