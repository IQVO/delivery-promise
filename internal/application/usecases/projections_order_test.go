package usecases_test

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// Out-of-order capacity and scorecard facts (warehouse-docs delivery-promise
// domain-events.md: DepartureCapacityChanged and DeliveryWaveCapacityChanged
// "newer `time` wins", NodeScorecardUpdated "applied only if `computedAt` is
// newer"; ADR 0022 sets the precedent: an equal instant applies, an older one
// is dropped). A stale figure delivered late must not reopen a full departure
// or wave, nor bring back a superseded grade.

// metaAt is a fresh envelope carrying an explicit CloudEvents time.
func (e *env) metaAt(typ string, t time.Time) usecases.Meta {
	m := e.meta(typ)
	m.Time = t
	return m
}

func TestOutOfOrderDepartureCapacityNeverReopensAFullDeparture(t *testing.T) {
	cases := []struct {
		name   string
		offset time.Duration // of the second event against the first
		first  int
		second int
		want   int
	}{
		{"an older figure delivered late is dropped", -5 * time.Minute, 0, 7, 0},
		{"a newer figure applies", 5 * time.Minute, 0, 7, 7},
		{"the same instant applies (later delivery of the same fact)", 0, 0, 7, 7},
	}
	for _, c := range cases {
		e := newEnv(t)
		t0 := at(22, 14, 30)
		must(t, e.svc.OnDepartureCapacityChanged(ctx, e.metaAt("DepartureCapacityChanged", t0), usecases.DepartureCapacityChanged{DepartureID: "dep-AUS3-1800", RemainingPackages: c.first}))
		must(t, e.svc.OnDepartureCapacityChanged(ctx, e.metaAt("DepartureCapacityChanged", t0.Add(c.offset)), usecases.DepartureCapacityChanged{DepartureID: "dep-AUS3-1800", RemainingPackages: c.second}))
		d, err := e.store.Network().GetDeparture(ctx, "dep-AUS3-1800")
		must(t, err)
		if d.Remaining != c.want {
			t.Errorf("%s: remaining = %d, want %d", c.name, d.Remaining, c.want)
		}
	}
}

func TestOutOfOrderWaveCapacityNeverReopensAFullWave(t *testing.T) {
	cases := []struct {
		name   string
		offset time.Duration
		want   int
	}{
		{"an older figure delivered late is dropped", -5 * time.Minute, 0},
		{"a newer figure applies", 5 * time.Minute, 6},
		{"the same instant applies", 0, 6},
	}
	for _, c := range cases {
		e := newEnv(t)
		t0 := at(22, 14, 30)
		must(t, e.svc.OnWaveCapacityChanged(ctx, e.metaAt("DeliveryWaveCapacityChanged", t0), usecases.WaveCapacityChanged{WaveID: "W-DAU1-1023-A", RemainingStops: 0}))
		must(t, e.svc.OnWaveCapacityChanged(ctx, e.metaAt("DeliveryWaveCapacityChanged", t0.Add(c.offset)), usecases.WaveCapacityChanged{WaveID: "W-DAU1-1023-A", RemainingStops: 6}))
		w, err := e.store.Network().GetWave(ctx, "W-DAU1-1023-A")
		must(t, err)
		if w.Remaining != c.want {
			t.Errorf("%s: remaining = %d, want %d", c.name, w.Remaining, c.want)
		}
	}
}

func TestACapacityFactWithoutATimeStillApplies(t *testing.T) {
	// A zero CloudEvents time (an older producer) is tolerated: no ordering
	// information, so the fact applies as before.
	e := newEnv(t)
	must(t, e.svc.OnDepartureCapacityChanged(ctx, e.metaAt("DepartureCapacityChanged", at(22, 14, 30)), usecases.DepartureCapacityChanged{DepartureID: "dep-AUS3-1800", RemainingPackages: 0}))
	must(t, e.svc.OnDepartureCapacityChanged(ctx, e.metaAt("DepartureCapacityChanged", time.Time{}), usecases.DepartureCapacityChanged{DepartureID: "dep-AUS3-1800", RemainingPackages: 4}))
	d, err := e.store.Network().GetDeparture(ctx, "dep-AUS3-1800")
	must(t, err)
	if d.Remaining != 4 {
		t.Errorf("remaining = %d, want 4", d.Remaining)
	}
}

func TestOutOfOrderScorecardKeepsTheNewestGrade(t *testing.T) {
	cases := []struct {
		name   string
		offset time.Duration
		want   string
	}{
		{"an older scorecard delivered late is dropped", -time.Hour, "A"},
		{"a newer scorecard applies", time.Hour, "F"},
		{"the same computedAt applies", 0, "F"},
	}
	for _, c := range cases {
		e := newEnv(t)
		t0 := at(22, 14, 0)
		must(t, e.svc.OnScorecardUpdated(ctx, e.meta("NodeScorecardUpdated"), usecases.ScorecardUpdated{NodeID: "N-AUS3", Window: "30d", Grade: "A", Observations: 100, ComputedAt: t0}))
		must(t, e.svc.OnScorecardUpdated(ctx, e.meta("NodeScorecardUpdated"), usecases.ScorecardUpdated{NodeID: "N-AUS3", Window: "30d", Grade: "F", Observations: 100, ComputedAt: t0.Add(c.offset)}))
		sc, err := e.store.Network().GetScorecard(ctx, "N-AUS3")
		must(t, err)
		if sc.Grade != c.want {
			t.Errorf("%s: grade = %s, want %s", c.name, sc.Grade, c.want)
		}
	}
}
