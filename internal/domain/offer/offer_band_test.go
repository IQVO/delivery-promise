package offer

import (
	"errors"
	"testing"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

func TestNewCarriesTheCapacityBandToTheOfferAndTheEvent(t *testing.T) {
	for _, band := range []shared.CapacityBand{shared.BandAmple, shared.BandTight, shared.BandScarce} {
		q := quote()
		q.CapacityBand = band
		o, evs, err := New(q, now)
		if err != nil {
			t.Fatal(err)
		}
		eq(t, "offer band", o.CapacityBand(), band)
		ev, ok := evs[0].(DeliveryOfferQuoted)
		if !ok {
			t.Fatalf("event is %T", evs[0])
		}
		eq(t, "event band", ev.CapacityBand, band)
	}
}

func TestNewWithoutABandOmitsIt(t *testing.T) {
	o, evs, err := New(quote(), now)
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "offer band", o.CapacityBand(), shared.CapacityBand(""))
	eq(t, "event band", evs[0].(DeliveryOfferQuoted).CapacityBand, shared.CapacityBand(""))
}

func TestNewRefusesAnUnknownBand(t *testing.T) {
	for _, bad := range []shared.CapacityBand{"ample", "FULL", " "} {
		q := quote()
		q.CapacityBand = bad
		if _, _, err := New(q, now); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%q: err = %v, want ErrValidation", bad, err)
		}
	}
}

// The band is a quote-time reading: it is not persisted, so a restored offer
// has none (ADR 0023).
func TestTheBandIsNotPersisted(t *testing.T) {
	q := quote()
	q.CapacityBand = shared.BandScarce
	o, _, err := New(q, now)
	if err != nil {
		t.Fatal(err)
	}
	back, err := Restore(o.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "restored band", back.CapacityBand(), shared.CapacityBand(""))
	eq(t, "snapshot equal", o.Snapshot().ID, back.Snapshot().ID)
}
