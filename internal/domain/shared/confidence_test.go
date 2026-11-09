package shared_test

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func TestConfidenceRoundsToThreeDecimalsAndClamps(t *testing.T) {
	cases := []struct {
		in   float64
		want float64
	}{{0.9504, 0.95}, {0.9996, 1}, {0.0004, 0}, {0.5555, 0.556}, {-1, 0}, {2, 1}, {math.NaN(), 0}}
	for _, c := range cases {
		got := shared.NewConfidence(c.in)
		if !got.Known() || got.Value() != c.want {
			t.Errorf("NewConfidence(%v) = %v known=%v, want %v", c.in, got.Value(), got.Known(), c.want)
		}
	}
}

func TestZeroConfidenceIsUnknownNotZero(t *testing.T) {
	var unknown shared.Confidence
	if unknown.Known() || unknown.Ptr() != nil {
		t.Fatal("the zero value must be unknown")
	}
	zero := shared.NewConfidence(0)
	if !zero.Known() || zero.Ptr() == nil || *zero.Ptr() != 0 {
		t.Fatal("a known 0 is not unknown")
	}
	if unknown == zero {
		t.Fatal("unknown and a known 0 must differ")
	}
	if shared.ConfidenceOf(nil).Known() || *shared.ConfidenceOf(zero.Ptr()).Ptr() != 0 {
		t.Fatal("ConfidenceOf must round-trip the optional wire form")
	}
}

func TestConfidenceJSONRoundTripKeepsUnknownAndKnown(t *testing.T) {
	type holder struct{ C shared.Confidence }
	for _, c := range []shared.Confidence{{}, shared.NewConfidence(0), shared.NewConfidence(0.973)} {
		raw, err := json.Marshal(holder{C: c})
		if err != nil {
			t.Fatal(err)
		}
		var back holder
		if err := json.Unmarshal(raw, &back); err != nil {
			t.Fatal(err)
		}
		if back.C != c {
			t.Errorf("%s round-trips to %+v, want %+v", raw, back.C, c)
		}
	}
	var old holder // a snapshot persisted before the field existed
	if err := json.Unmarshal([]byte(`{}`), &old); err != nil || old.C.Known() {
		t.Fatalf("a missing field must decode as unknown: %v %+v", err, old)
	}
	if err := json.Unmarshal([]byte(`{"C":"x"}`), &old); err == nil {
		t.Fatal("a non-number must be refused")
	}
}
