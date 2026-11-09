package usecases_test

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

func loadArrived(load, departure, dest string, arrivedAt time.Time) usecases.LoadArrived {
	return usecases.LoadArrived{LoadID: load, DepartureID: departure, DestinationNodeID: dest, ArrivedAt: arrivedAt}
}

func TestLoadArrivedWithADepartureIdFeedsTheArrivalModel(t *testing.T) {
	e, counter, _ := newLateEnv(t)
	e.arrived("ld-1", "dep-AUS3-1800", "N-SAU1", at(22, 19, 10))
	got, err := e.store.DepartureArrivals().DepartureArrived(ctx, "dep-AUS3-1800")
	must(t, err)
	eq(t, "recorded", got, true)
	eq(t, "nothing ignored", counter.ignored, 0)
}

func TestLoadArrivedWithoutADepartureIdIsIgnoredAndCounted(t *testing.T) {
	e, counter, _ := newLateEnv(t)
	e.arrived("ld-1", "", "N-SAU1", at(22, 19, 10))
	eq(t, "counted", counter.ignored, 1)
	got, err := e.store.DepartureArrivals().DepartureArrived(ctx, "")
	must(t, err)
	eq(t, "no row", got, false)
}

func TestLoadArrivedRedeliveryChangesNothing(t *testing.T) {
	e, counter, _ := newLateEnv(t)
	m := e.meta("LoadArrived")
	for range 2 { // the same CloudEvents id twice
		must(t, e.svc.OnLoadArrived(ctx, m, loadArrived("ld-1", "", "N-SAU1", at(22, 19, 10))))
	}
	eq(t, "counted once", counter.ignored, 1)
}
