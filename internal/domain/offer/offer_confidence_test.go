package offer

import (
	"encoding/json"
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func TestAConfidentQuoteKeepsItsConfidenceThroughTheEventAndTheSnapshot(t *testing.T) {
	q := quote()
	q.Confidence = shared.NewConfidence(0.973)
	o, evs, err := New(q, now)
	if err != nil {
		t.Fatal(err)
	}
	if o.Confidence().Value() != 0.973 || !o.Confidence().Known() {
		t.Fatalf("confidence = %+v", o.Confidence())
	}
	if ev := evs[0].(DeliveryOfferQuoted); ev.Confidence != o.Confidence() {
		t.Fatalf("event confidence = %+v", ev.Confidence)
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
	if back.Confidence() != o.Confidence() {
		t.Fatalf("restored confidence = %+v, want %+v", back.Confidence(), o.Confidence())
	}
}

func TestAQuoteWithoutConfidenceIsUnknownNotZero(t *testing.T) {
	o, evs, _ := New(quote(), now)
	if o.Confidence().Known() || o.Confidence().Ptr() != nil || evs[0].(DeliveryOfferQuoted).Confidence.Known() {
		t.Fatal("a quote from a segment below the minimum must carry an unknown confidence")
	}
	raw, _ := json.Marshal(o.Snapshot())
	var legacy map[string]json.RawMessage
	_ = json.Unmarshal(raw, &legacy)
	delete(legacy, "Confidence") // a row persisted before ADR 0020
	old, _ := json.Marshal(legacy)
	var s Snapshot
	if err := json.Unmarshal(old, &s); err != nil {
		t.Fatal(err)
	}
	got, err := Restore(s)
	if err != nil || got.Confidence().Known() {
		t.Fatalf("a legacy row restores with an unknown confidence: %v %+v", err, got)
	}
}
