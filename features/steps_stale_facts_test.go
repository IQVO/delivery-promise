package features_test

import (
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// registerStaleFactSteps registers the steps of stale_facts_rules.feature: a
// capacity fact stamped relative to the previous capacity fact (the "full"
// one of the Given before it), so a late delivery is an OLDER event time.
func registerStaleFactSteps(sc *godog.ScenarioContext, w *world) {
	sc.Step(`^the pickup departure reports (\d+) packages left that is (\d+) minutes (older|newer) than the full one$`,
		func(n, minutes int, dir string) error {
			return w.svc.OnDepartureCapacityChanged(w.ctx(), w.metaStamped("DepartureCapacityChanged", minutes, dir),
				usecases.DepartureCapacityChanged{DepartureID: "dep-AUS3-1800", RemainingPackages: n})
		})
	sc.Step(`^the delivery wave reports (\d+) stops left that is (\d+) minutes (older|newer) than the full one$`,
		func(n, minutes int, dir string) error {
			return w.svc.OnWaveCapacityChanged(w.ctx(), w.metaStamped("DeliveryWaveCapacityChanged", minutes, dir),
				usecases.WaveCapacityChanged{WaveID: "W-DAU1-1023-A", RemainingStops: n})
		})
}

// metaStamped is an envelope whose CloudEvents time is the clock's now (the
// instant of the "full" fact) moved by the given minutes.
func (w *world) metaStamped(typ string, minutes int, dir string) usecases.Meta {
	m := w.meta(typ)
	d := time.Duration(minutes) * time.Minute
	if dir == "older" {
		d = -d
	}
	m.Time = m.Time.Add(d)
	return m
}
