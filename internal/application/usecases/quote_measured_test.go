package usecases_test

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

// loadObserved pairs one load of the (origin, dest) lane through the two
// consumer events, minutes of actual transit.
func (e *env) loadObserved(loadID, origin, dest string, minutes int, at time.Time) {
	e.t.Helper()
	must(e.t, e.svc.OnLoadDeparted(ctx, e.meta("LoadDeparted"), usecases.LoadDeparted{LoadID: loadID, OriginNodeID: origin, DepartedAt: at}))
	must(e.t, e.svc.OnLoadArrived(ctx, e.meta("LoadArrived"),
		usecases.LoadArrived{LoadID: loadID, DestinationNodeID: dest, ArrivedAt: at.Add(time.Duration(minutes) * time.Minute)}))
}

// TestAQuoteOnAMeasuredLaneCarriesBasisMEASURED closes the loop of ADR 0014:
// twenty observed transits of 135 minutes on the sortation lane (75 scheduled)
// stretch the linehaul's arrival by an hour, still inside the wave's induct
// cutoff, and the offer and its analytics event say MEASURED.
func TestAQuoteOnAMeasuredLaneCarriesBasisMEASURED(t *testing.T) {
	e := newEnv(t)
	for i := range 20 {
		e.loadObserved("ld-q-"+string(rune('a'+i)), "N-SAU1", "N-DAU1", 135, at(21, 20, 0).Add(time.Duration(i)*time.Hour))
	}
	res := e.quote(shared.TierNextDay)
	if len(res.Offers) != 1 {
		t.Fatalf("offers = %+v unavailable %+v", res.Offers, res.Unavailable)
	}
	o := res.Offers[0]
	if o.Basis != string(shared.BasisMeasured) {
		t.Fatalf("basis = %s, want MEASURED", o.Basis)
	}
	if !o.Window.Latest.Equal(at(23, 21, 0)) { // the wave still proves the window
		t.Fatalf("window = %+v", o.Window)
	}
	quoted := 0
	for _, ev := range e.store.Outbox().Events() {
		if q, ok := ev.(offer.DeliveryOfferQuoted); ok {
			quoted++
			if q.Basis != shared.BasisMeasured {
				t.Fatalf("quoted event basis = %s", q.Basis)
			}
		}
	}
	if quoted != 1 {
		t.Fatalf("DeliveryOfferQuoted events = %d", quoted)
	}
}

// TestAQuoteOnALaneBelowMinObservationsKeepsSCHEDULED pins the other side:
// nineteen observations are one short of the default twenty.
func TestAQuoteOnALaneBelowMinObservationsKeepsSCHEDULED(t *testing.T) {
	e := newEnv(t)
	for i := range 19 {
		e.loadObserved("ld-u-"+string(rune('a'+i)), "N-SAU1", "N-DAU1", 135, at(21, 20, 0).Add(time.Duration(i)*time.Hour))
	}
	res := e.quote(shared.TierNextDay)
	if len(res.Offers) != 1 || res.Offers[0].Basis != string(shared.BasisScheduled) {
		t.Fatalf("offers = %+v unavailable %+v", res.Offers, res.Unavailable)
	}
}
