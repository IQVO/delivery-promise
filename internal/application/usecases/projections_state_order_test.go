package usecases_test

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/application/usecases"
)

// Out-of-order node status and listing suppression (ADR 0029). Same rule as
// ADR 0028 / ADR 0022: the newer CloudEvents time wins, an equal instant
// applies (the later delivery of the same fact), an older one is dropped, a
// zero instant carries no ordering. RETIRED is terminal: nothing leaves it.

type nodeStep struct {
	state  string
	offset time.Duration // of the event time against t0
}

func TestNodeStatusNewerTimeWins(t *testing.T) {
	const (
		act = readmodel.NodeActive
		sus = readmodel.NodeSuspended
		ret = readmodel.NodeRetired
	)
	m := time.Minute
	cases := []struct {
		name  string
		steps []nodeStep
		want  string
	}{
		{"an older activation delivered late does not undo a suspension", []nodeStep{{sus, 0}, {act, -5 * m}}, sus},
		{"an older suspension delivered late does not undo an activation", []nodeStep{{act, 0}, {sus, -5 * m}}, act},
		{"a newer activation applies", []nodeStep{{sus, 0}, {act, 5 * m}}, act},
		{"a newer suspension applies", []nodeStep{{act, 0}, {sus, 5 * m}}, sus},
		{"the same instant applies (later delivery of the same fact)", []nodeStep{{sus, 0}, {act, 0}}, act},
		{"a retirement is terminal against a newer activation", []nodeStep{{ret, 0}, {act, 5 * m}}, ret},
		{"a retirement is terminal against the same instant", []nodeStep{{ret, 0}, {act, 0}}, ret},
		{"a retirement is terminal against an older activation", []nodeStep{{ret, 0}, {act, -5 * m}}, ret},
		{"a retirement is terminal against a newer suspension", []nodeStep{{ret, 0}, {sus, 5 * m}}, ret},
		{"a retirement delivered late still retires (it is terminal, whatever its time)", []nodeStep{{act, 0}, {ret, -5 * m}}, ret},
		{"a retirement delivered late still retires a suspended node", []nodeStep{{sus, 0}, {ret, -5 * m}}, ret},
		{"a retirement redelivered stays retired", []nodeStep{{ret, 0}, {ret, 5 * m}}, ret},
		{"an activation after a late retirement does not revive the node", []nodeStep{{act, 0}, {ret, -5 * m}, {act, 10 * m}}, ret},
	}
	for _, c := range cases {
		e := newEnv(t)
		t0 := at(22, 14, 30)
		for _, s := range c.steps {
			must(t, e.svc.OnNodeStateChanged(ctx, e.metaAt("NodeStateChanged", t0.Add(s.offset)), usecases.NodeStateChanged{NodeID: "N-ORD"}, s.state))
		}
		n, err := e.store.Network().GetNode(ctx, "N-ORD")
		must(t, err)
		if n.State != c.want {
			t.Errorf("%s: state = %s, want %s", c.name, n.State, c.want)
		}
	}
}

func TestNodeStatusWithoutATimeStillApplies(t *testing.T) {
	// A zero CloudEvents time (an older producer) carries no ordering, so the
	// fact applies as before; a retired node still stays retired.
	e := newEnv(t)
	must(t, e.svc.OnNodeStateChanged(ctx, e.metaAt("NodeSuspended", at(22, 14, 30)), usecases.NodeStateChanged{NodeID: "N-ORD"}, readmodel.NodeSuspended))
	must(t, e.svc.OnNodeStateChanged(ctx, e.metaAt("NodeActivated", time.Time{}), usecases.NodeStateChanged{NodeID: "N-ORD"}, readmodel.NodeActive))
	n, err := e.store.Network().GetNode(ctx, "N-ORD")
	must(t, err)
	if n.State != readmodel.NodeActive {
		t.Errorf("state = %s, want ACTIVE", n.State)
	}
	must(t, e.svc.OnNodeStateChanged(ctx, e.metaAt("NodeRetired", time.Time{}), usecases.NodeStateChanged{NodeID: "N-ORD"}, readmodel.NodeRetired))
	must(t, e.svc.OnNodeStateChanged(ctx, e.metaAt("NodeActivated", time.Time{}), usecases.NodeStateChanged{NodeID: "N-ORD"}, readmodel.NodeActive))
	n, err = e.store.Network().GetNode(ctx, "N-ORD")
	must(t, err)
	if n.State != readmodel.NodeRetired {
		t.Errorf("state = %s, want RETIRED", n.State)
	}
}

func TestANodeRetiredBeforeItsRegistrationStaysRetired(t *testing.T) {
	// node-topology events of one node can arrive in any order across
	// partitions: the registration must not bring a retired node back.
	e := newEnv(t)
	must(t, e.svc.OnNodeStateChanged(ctx, e.metaAt("NodeRetired", at(22, 14, 30)), usecases.NodeStateChanged{NodeID: "N-ORD"}, readmodel.NodeRetired))
	must(t, e.svc.OnNodeRegistered(ctx, e.metaAt("NodeRegistered", at(22, 14, 0)), usecases.NodeRegistered{NodeID: "N-ORD", NodeType: "FULFILLMENT_CENTER", Timezone: "America/Chicago"}))
	n, err := e.store.Network().GetNode(ctx, "N-ORD")
	must(t, err)
	if n.State != readmodel.NodeRetired {
		t.Errorf("state = %s, want RETIRED", n.State)
	}
}

func TestListingSuppressionNewerTimeWins(t *testing.T) {
	m := time.Minute
	type step struct {
		suppressed bool
		offset     time.Duration
	}
	cases := []struct {
		name  string
		steps []step
		want  bool // suppressed
	}{
		{"an older restore delivered late does not undo a suppression", []step{{true, 0}, {false, -5 * m}}, true},
		{"an older suppression delivered late does not undo a restore", []step{{false, 0}, {true, -5 * m}}, false},
		{"a newer restore applies", []step{{true, 0}, {false, 5 * m}}, false},
		{"a newer suppression applies", []step{{false, 0}, {true, 5 * m}}, true},
		{"the same instant applies (later delivery of the same fact)", []step{{true, 0}, {false, 0}}, false},
		{"the newest of three wins whatever the delivery order", []step{{false, 10 * m}, {true, 5 * m}, {true, 0}}, false},
	}
	for _, c := range cases {
		e := newEnv(t)
		t0 := at(22, 14, 30)
		for _, s := range c.steps {
			must(t, e.svc.OnListingToggled(ctx, e.metaAt("ListingToggled", t0.Add(s.offset)), usecases.ListingToggled{ListingID: "lst-ord", NodeID: "N-AUS3"}, s.suppressed))
		}
		l, err := e.store.Network().GetListing(ctx, "lst-ord")
		must(t, err)
		if l.Suppressed != c.want {
			t.Errorf("%s: suppressed = %v, want %v", c.name, l.Suppressed, c.want)
		}
	}
}

func TestListingSuppressionWithoutATimeStillApplies(t *testing.T) {
	e := newEnv(t)
	must(t, e.svc.OnListingToggled(ctx, e.metaAt("ListingSuppressed", at(22, 14, 30)), usecases.ListingToggled{ListingID: "lst-ord", NodeID: "N-AUS3"}, true))
	must(t, e.svc.OnListingToggled(ctx, e.metaAt("ListingRestored", time.Time{}), usecases.ListingToggled{ListingID: "lst-ord", NodeID: "N-AUS3"}, false))
	l, err := e.store.Network().GetListing(ctx, "lst-ord")
	must(t, err)
	if l.Suppressed {
		t.Error("a restore without a time must apply")
	}
}

func TestAnAvailabilityFigureKeepsTheSuppressionOrdering(t *testing.T) {
	// A stock figure rewrites the row but must not reset the instant of the
	// suppression fact it carries, or an older restore could slip in after it.
	e := newEnv(t)
	t0 := at(22, 14, 30)
	must(t, e.svc.OnListingToggled(ctx, e.metaAt("ListingSuppressed", t0), usecases.ListingToggled{ListingID: "lst-ord", NodeID: "N-AUS3"}, true))
	must(t, e.svc.OnListingAvailabilityChanged(ctx, e.metaAt("ListingAvailabilityChanged", t0.Add(time.Minute)),
		usecases.ListingAvailabilityChanged{ListingID: "lst-ord", CatalogItemID: "ci-1001", NodeID: "N-AUS3", AvailableQty: 9, AsOf: t0}))
	must(t, e.svc.OnListingToggled(ctx, e.metaAt("ListingRestored", t0.Add(-5*time.Minute)), usecases.ListingToggled{ListingID: "lst-ord", NodeID: "N-AUS3"}, false))
	l, err := e.store.Network().GetListing(ctx, "lst-ord")
	must(t, err)
	if !l.Suppressed || l.AvailableQty != 9 {
		t.Errorf("suppressed = %v qty = %d, want true and 9", l.Suppressed, l.AvailableQty)
	}
}
