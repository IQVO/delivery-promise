package repocontract

import (
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/application/repository"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

var promiseCases = []named{
	{"a saved promise round-trips with revisions and judgement", promiseRoundTrip},
	{"a miss is ErrNotFound", promiseMiss},
	{"a duplicate create, a stale update and a second promise for an offer are ErrConcurrentUpdate", promiseConcurrency},
	{"GetByOffer, FindByOrderGroup and ListByOrder", promiseFinders},
	{"ListHoldsExpired and ListAtRisk", promiseSweeps},
	{"ListActiveByPath matches the pickup, a leg or the wave of active promises", promiseByPath},
	{"HeldQuantities sums PROVISIONAL promises per node and item", promiseHeld},
}

// confirmedPromise stores a promise and confirms it in a second save, as the
// use cases do (one command per save), and returns it as stored.
func confirmedPromise(t *testing.T, f Fixture, id, offerID, order string, group int) *promise.Promise {
	t.Helper()
	return confirmedPromiseAt(t, f, id, offerID, "dep-1800", order, group)
}

func confirmedPromiseAt(t *testing.T, f Fixture, id, offerID, pickup, order string, group int) *promise.Promise {
	t.Helper()
	noErr(t, f.Promises.Save(ctx, NewPromise(t, id, offerID, "N-AUS3", pickup)))
	p, err := f.Promises.Get(ctx, promise.ID(id))
	noErr(t, err)
	_, err = p.Confirm(order, group, T0.Add(time.Minute))
	noErr(t, err)
	noErr(t, f.Promises.Save(ctx, p))
	return p
}

func promiseRoundTrip(t *testing.T, f Fixture) {
	noErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-1", "off-1", "N-AUS3", "dep-1800")))
	steps := []func(*promise.Promise) error{
		func(p *promise.Promise) error { _, err := p.Confirm("ord-1", 1, T0.Add(time.Minute)); return err },
		func(p *promise.Promise) error {
			_, err := p.FlagAtRisk("DEPARTURE_CANCELLED", T0.Add(time.Hour))
			return err
		},
		func(p *promise.Promise) error { _, err := p.Revise(nominalRevision(), T0.Add(2*time.Hour)); return err },
		func(p *promise.Promise) error {
			_, err := p.Judge(promise.Verdict{DeliveredAt: T0.Add(55 * time.Hour)}, T0.Add(56*time.Hour))
			return err
		},
	}
	for _, step := range steps {
		p, err := f.Promises.Get(ctx, "prm-1")
		noErr(t, err)
		noErr(t, step(p))
		noErr(t, f.Promises.Save(ctx, p))
	}
	got, err := f.Promises.Get(ctx, "prm-1")
	noErr(t, err)
	eq(t, "state", got.State(), promise.StateKept)
	eq(t, "order", got.OrderID(), "ord-1")
	eq(t, "group", got.GroupNo(), 1)
	eq(t, "version", got.Version(), int64(5))
	eq(t, "book", got.BookID(), "N-DFW7:nominal-x")
	eq(t, "basis", got.Terms().Basis, shared.BasisNominal)
	eq(t, "tier", got.Terms().Tier, shared.TierStandard)
	eq(t, "witness node", got.Witness().NodeID, "N-DFW7")
	eq(t, "witness has no pickup", got.Witness().PickupDepartureID, "")
	eq(t, "revisions", len(got.Revisions()), 1)
	eq(t, "previous tier", got.Revisions()[0].PreviousTerms.Tier, shared.TierNextDay)
	eq(t, "time zone", got.Terms().Window.TimeZone, "America/Denver")
	eq(t, "previous time zone", got.Revisions()[0].PreviousTerms.Window.TimeZone, "America/Chicago")
	eq(t, "revision reason", got.Revisions()[0].Reason, "DEPARTURE_CANCELLED")
	j := got.Judgement()
	if j == nil {
		t.Fatal("the judgement was lost")
	}
	eq(t, "outcome", j.Outcome, promise.OutcomeKept)
	eqTime(t, "deliveredAt", j.DeliveredAt, T0.Add(55*time.Hour))
	eqTime(t, "holdExpiresAt", got.HoldExpiresAt(), T0.Add(15*time.Minute))
	eq(t, "lines", len(got.Lines()), 1)
}

func nominalRevision() promise.Revising {
	return promise.Revising{
		Terms: promise.Terms{
			Tier: shared.TierStandard, ShipBy: T0.Add(30 * time.Hour), Basis: shared.BasisNominal, PolicyVersion: "v1",
			Window: shared.Window{Earliest: T0.Add(50 * time.Hour), Latest: T0.Add(60 * time.Hour), TimeZone: "America/Denver"},
		},
		Witness: shared.Witness{NodeID: "N-DFW7"}, BookID: "N-DFW7:nominal-x", Reason: "DEPARTURE_CANCELLED",
	}
}

func promiseMiss(t *testing.T, f Fixture) {
	_, err := f.Promises.Get(ctx, "prm-nope")
	wantErr(t, err, ports.ErrNotFound)
	_, err = f.Promises.GetByOffer(ctx, "off-nope")
	wantErr(t, err, ports.ErrNotFound)
	_, err = f.Promises.FindByOrderGroup(ctx, "ord-nope", 1)
	wantErr(t, err, ports.ErrNotFound)
	list, err := f.Promises.ListByOrder(ctx, "ord-nope")
	noErr(t, err)
	eq(t, "empty list", len(list), 0)
}

func promiseConcurrency(t *testing.T, f Fixture) {
	noErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-1", "off-1", "N-AUS3", "dep-1800")))
	wantErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-1", "off-9", "N-AUS3", "dep-1800")), ports.ErrConcurrentUpdate)
	wantErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-2", "off-1", "N-AUS3", "dep-1800")), ports.ErrConcurrentUpdate)
	a, _ := f.Promises.Get(ctx, "prm-1")
	b, _ := f.Promises.Get(ctx, "prm-1")
	_, err := a.Confirm("ord-1", 1, T0.Add(time.Minute))
	noErr(t, err)
	noErr(t, f.Promises.Save(ctx, a))
	_, err = b.ReleaseExpiredHold(T0.Add(time.Hour))
	noErr(t, err)
	wantErr(t, f.Promises.Save(ctx, b), ports.ErrConcurrentUpdate)
}

func promiseFinders(t *testing.T, f Fixture) {
	noErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-1", "off-1", "N-AUS3", "dep-1800")))
	confirmedPromise(t, f, "prm-2", "off-2", "ord-7", 2)
	confirmedPromise(t, f, "prm-3", "off-3", "ord-7", 1)
	got, err := f.Promises.GetByOffer(ctx, "off-2")
	noErr(t, err)
	eq(t, "by offer", got.ID(), promise.ID("prm-2"))
	got, err = f.Promises.FindByOrderGroup(ctx, "ord-7", 1)
	noErr(t, err)
	eq(t, "by order group", got.ID(), promise.ID("prm-3"))
	list, err := f.Promises.ListByOrder(ctx, "ord-7")
	noErr(t, err)
	eq(t, "order list", len(list), 2)
	eq(t, "ordered by groupNo", list[0].GroupNo(), 1)
	eq(t, "second group", list[1].GroupNo(), 2)
}

func promiseSweeps(t *testing.T, f Fixture) {
	noErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-1", "off-1", "N-AUS3", "dep-1800")))
	noErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-2", "off-2", "N-AUS3", "dep-1800")))
	risky := confirmedPromise(t, f, "prm-3", "off-3", "ord-1", 1)
	_, err := risky.FlagAtRisk("WAVE_CANCELLED", T0.Add(time.Hour))
	noErr(t, err)
	noErr(t, f.Promises.Save(ctx, risky))
	expired, err := f.Promises.ListHoldsExpired(ctx, T0.Add(15*time.Minute), 10)
	noErr(t, err)
	eq(t, "holds expired", len(expired), 2)
	expired, _ = f.Promises.ListHoldsExpired(ctx, T0.Add(15*time.Minute-time.Nanosecond), 10)
	eq(t, "not before expiry", len(expired), 0)
	expired, _ = f.Promises.ListHoldsExpired(ctx, T0.Add(time.Hour), 1)
	eq(t, "limit", len(expired), 1)
	atRisk, err := f.Promises.ListAtRisk(ctx, 10)
	noErr(t, err)
	eq(t, "at risk", len(atRisk), 1)
	eq(t, "at risk id", atRisk[0], promise.ID("prm-3"))
}

func promiseByPath(t *testing.T, f Fixture) {
	confirmedPromise(t, f, "prm-1", "off-1", "ord-1", 1)
	noErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-2", "off-2", "N-AUS3", "dep-1800"))) // PROVISIONAL: not active
	confirmedPromiseAt(t, f, "prm-3", "off-3", "dep-other", "ord-3", 1)
	for name, flt := range map[string]repository.PromiseRiskFilter{
		"pickup": {DepartureID: "dep-1800"}, "leg": {DepartureID: "dep-2200"},
	} {
		ids, err := f.Promises.ListActiveByPath(ctx, flt)
		noErr(t, err)
		want := 1
		if name == "leg" {
			want = 2
		}
		eq(t, name, len(ids), want)
	}
	ids, _ := f.Promises.ListActiveByPath(ctx, repository.PromiseRiskFilter{WaveID: "w-1"})
	eq(t, "wave", len(ids), 2)
	ids, _ = f.Promises.ListActiveByPath(ctx, repository.PromiseRiskFilter{DepartureID: "dep-none", WaveID: "w-none"})
	eq(t, "no match", len(ids), 0)
	ids, _ = f.Promises.ListActiveByPath(ctx, repository.PromiseRiskFilter{})
	eq(t, "an empty filter matches nothing", len(ids), 0)
}

func promiseHeld(t *testing.T, f Fixture) {
	noErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-1", "off-1", "N-AUS3", "dep-1800")))
	noErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-2", "off-2", "N-AUS3", "dep-1800")))
	noErr(t, f.Promises.Save(ctx, NewPromise(t, "prm-3", "off-3", "N-DFW7", "dep-9")))
	confirmedPromise(t, f, "prm-4", "off-4", "ord-1", 1) // confirmed: not held any more
	held, err := f.Promises.HeldQuantities(ctx, []string{"N-AUS3"})
	noErr(t, err)
	eq(t, "AUS3 ci-1001", held[repository.HoldKey{NodeID: "N-AUS3", ItemID: "ci-1001"}], 4)
	eq(t, "other nodes are not listed", len(held), 1)
	held, _ = f.Promises.HeldQuantities(ctx, []string{"N-AUS3", "N-DFW7"})
	eq(t, "DFW7 ci-1001", held[repository.HoldKey{NodeID: "N-DFW7", ItemID: "ci-1001"}], 2)
	held, _ = f.Promises.HeldQuantities(ctx, nil)
	eq(t, "no nodes", len(held), 0)
}
