package promise

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func TestThePromiseTermsCarryTheConfidenceToPromiseMadeAndTheSnapshot(t *testing.T) {
	in := making()
	in.Terms.Confidence = shared.NewConfidence(0.951)
	p, evs, err := Make(in, t0)
	if err != nil {
		t.Fatal(err)
	}
	if p.Terms().Confidence.Value() != 0.951 || evs[0].(PromiseMade).Terms.Confidence != p.Terms().Confidence {
		t.Fatalf("confidence = %+v, event %+v", p.Terms().Confidence, evs[0])
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
	if back.Terms().Confidence != p.Terms().Confidence {
		t.Fatalf("restored confidence = %+v", back.Terms().Confidence)
	}
}

func TestARevisionKeepsTheConfidenceOfThePreviousTerms(t *testing.T) {
	in := making()
	in.Terms.Confidence = shared.NewConfidence(0.9)
	p, _, _ := Make(in, t0)
	mustEvents(t, 1)(p.Confirm("ord-1", 1, t0))
	mustEvents(t, 1)(p.FlagAtRisk("late linehaul", t0))
	next := in.Terms
	next.Window.Latest = next.Window.Latest.Add(12 * time.Hour)
	next.Window.Earliest = next.Window.Earliest.Add(12 * time.Hour)
	next.Confidence = shared.NewConfidence(0.8)
	if _, err := p.Revise(Revising{Terms: next, Witness: in.Witness, BookID: in.BookID, Reason: "late linehaul", Lines: in.Lines}, t0); err != nil {
		t.Fatal(err)
	}
	rev := p.Revisions()
	if len(rev) != 1 || rev[0].PreviousTerms.Confidence.Value() != 0.9 || p.Terms().Confidence.Value() != 0.8 {
		t.Fatalf("revision = %+v, now %+v", rev, p.Terms().Confidence)
	}
}
