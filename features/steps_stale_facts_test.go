package features_test

import (
	"time"

	"github.com/cucumber/godog"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// registerStaleFactSteps registers the steps of stale_facts_rules.feature: a
// fact stamped relative to the previous fact of the scenario (the "full" one
// of the capacity Givens, the step before for node and listing facts), so a
// late delivery is an OLDER event time.
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
	sc.Step(`^the node "([^"]*)" is (activated|suspended|retired) by an event that is (\d+) minutes (older|newer) than the previous one$`,
		w.stampedNodeState)
	sc.Step(`^the listing "([^"]*)" is (suppressed|restored) by an event that is (\d+) minutes (older|newer) than the previous one$`,
		w.stampedListingToggle)
}

// stampedNodeState is a NodeActivated, NodeSuspended or NodeRetired whose
// CloudEvents time is the previous fact's instant moved by the given minutes.
func (w *world) stampedNodeState(node, change string, minutes int, dir string) error {
	state := readmodel.NodeActive
	switch change {
	case "suspended":
		state = readmodel.NodeSuspended
	case "retired":
		state = readmodel.NodeRetired
	}
	return w.svc.OnNodeStateChanged(w.ctx(), w.metaStamped("Node"+change, minutes, dir), usecases.NodeStateChanged{NodeID: node}, state)
}

// stampedListingToggle is a ListingSuppressed or ListingRestored stamped the
// same way.
func (w *world) stampedListingToggle(id, change string, minutes int, dir string) error {
	l, err := w.store.Network().GetListing(w.ctx(), id)
	if err != nil {
		return err
	}
	name := "ListingRestored"
	if change == "suppressed" {
		name = "ListingSuppressed"
	}
	return w.svc.OnListingToggled(w.ctx(), w.metaStamped(name, minutes, dir), usecases.ListingToggled{ListingID: id, NodeID: l.NodeID}, change == "suppressed")
}

// metaStamped is an envelope whose CloudEvents time is the clock's now (the
// instant of the previous fact) moved by the given minutes.
func (w *world) metaStamped(typ string, minutes int, dir string) usecases.Meta {
	m := w.meta(typ)
	d := time.Duration(minutes) * time.Minute
	if dir == "older" {
		d = -d
	}
	m.Time = m.Time.Add(d)
	return m
}
