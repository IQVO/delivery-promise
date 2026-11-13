package policy

import (
	"testing"
	"time"
)

func TestCarrierWindowDefaultsAndBounds(t *testing.T) {
	if got := Default().CarrierWindowHours(); got != 24*time.Hour {
		t.Fatalf("the default policy answers 24h, got %v", got)
	}
	hours := 96
	p, _, err := Publish(0, Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100, CarrierWindowHours: &hours}, t0)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.CarrierWindowHours(); got != 96*time.Hour {
		t.Fatalf("a published width answers verbatim, got %v", got)
	}
	// the snapshot round-trips the resolved width, and a snapshot persisted
	// before ADR 0026 (nil) restores with the default
	s := p.Snapshot()
	if s.CarrierWindowHours == nil || *s.CarrierWindowHours != 96 {
		t.Fatalf("snapshot width = %+v", s.CarrierWindowHours)
	}
	restored, err := Restore(s)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.CarrierWindowHours(); got != 96*time.Hour {
		t.Fatalf("restored width = %v", got)
	}
	old := Snapshot{Version: 1, OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100, PublishedAt: t0}
	r, err := Restore(old)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.CarrierWindowHours(); got != 24*time.Hour {
		t.Fatalf("a pre-0026 snapshot restores with the default 24h, got %v", got)
	}
	for _, bad := range []int{0, -1, 97} {
		v := bad
		if _, _, err := Publish(0, Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100, CarrierWindowHours: &v}, t0); err == nil {
			t.Fatalf("carrierWindowHours %d must be refused", bad)
		}
	}
	// the published event carries the resolved width
	_, evs, _ := Publish(0, Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 1, SharePercent: 100}, t0)
	if got := evs[0].(PromisePolicyPublished).CarrierWindowHours; got != 24 {
		t.Fatalf("the event carries the resolved width, got %d", got)
	}
}
