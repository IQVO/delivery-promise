package promise

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

var (
	t0      = time.Date(2026, 10, 22, 19, 9, 0, 0, time.UTC)
	shipBy  = time.Date(2026, 10, 22, 23, 0, 0, 0, time.UTC)
	earlier = time.Date(2026, 10, 23, 14, 0, 0, 0, time.UTC)
	latest  = time.Date(2026, 10, 24, 2, 0, 0, 0, time.UTC)
)

func terms() Terms {
	return Terms{Tier: shared.TierNextDay, ShipBy: shipBy, Window: shared.Window{Earliest: earlier, Latest: latest, TimeZone: "America/Chicago"}, Basis: shared.BasisScheduled, PolicyVersion: "v3"}
}

func making() Making {
	return Making{
		ID: "prm-1", OfferID: "off-1", DestinationPrefix: "787", Lines: []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
		Terms: terms(), Witness: shared.Witness{NodeID: "n1", PickupDepartureID: "d1", Legs: []string{"d2", "d3"}, DeliveryWaveID: "w1"},
		BookID: "n1:d1", HoldTTL: 15 * time.Minute, Available: map[string]int{"ci-1001": 2},
	}
}

func provisional(t *testing.T) *Promise {
	t.Helper()
	p, _, err := Make(making(), t0)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func confirmed(t *testing.T) *Promise {
	t.Helper()
	p := provisional(t)
	if _, err := p.Confirm("ord-1", 1, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	return p
}

func atRisk(t *testing.T) *Promise {
	t.Helper()
	p := confirmed(t)
	if _, err := p.FlagAtRisk("DEPARTURE_CANCELLED", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return p
}

func inState(t *testing.T, s State) *Promise {
	t.Helper()
	switch s {
	case StateProvisional:
		return provisional(t)
	case StateConfirmed:
		return confirmed(t)
	case StateAtRisk:
		return atRisk(t)
	case StateKept:
		p := confirmed(t)
		mustEvents(t, 1)(p.Judge(Verdict{DeliveredAt: latest}, latest))
		return p
	case StateBroken:
		p := confirmed(t)
		mustEvents(t, 1)(p.Judge(Verdict{DeliveredAt: latest.Add(time.Hour)}, latest))
		return p
	case StateReleased:
		p := provisional(t)
		mustEvents(t, 1)(p.ReleaseExpiredHold(t0.Add(time.Hour)))
		return p
	default:
		p := confirmed(t)
		mustEvents(t, 1)(p.Cancel("CUSTOMER_REQUEST", t0.Add(time.Hour)))
		return p
	}
}

func mustEvents(t *testing.T, n int) func([]shared.Event, error) []shared.Event {
	return func(evs []shared.Event, err error) []shared.Event {
		t.Helper()
		if err != nil || len(evs) != n {
			t.Fatalf("want %d events and no error, got %d, %v", n, len(evs), err)
		}
		return evs
	}
}

func TestMakeCreatesAProvisionalPromiseAndRaisesPromiseMade(t *testing.T) {
	p, evs, err := Make(making(), t0.In(time.FixedZone("z", 3600)))
	if err != nil {
		t.Fatal(err)
	}
	assertMade(t, p)
	e, ok := mustEvents(t, 1)(evs, nil)[0].(PromiseMade)
	if !ok || e.EventName() != "PromiseMade" || e.Entity() != "promise" || e.SubjectID() != "prm-1" || e.AggregateVersion() != 1 || !e.OccurredAt().Equal(t0) {
		t.Fatalf("event = %+v", evs[0])
	}
	if e.OfferID != "off-1" || e.Terms != terms() || e.Witness.PickupDepartureID != "d1" || !e.HoldExpiresAt.Equal(p.HoldExpiresAt()) || len(e.Lines) != 1 {
		t.Fatalf("payload = %+v", e)
	}
}

func TestMakeOwnsItsInputs(t *testing.T) {
	in := making()
	p, _, _ := Make(in, t0)
	in.Lines[0].Quantity = 50
	in.Witness.Legs[0] = "zz"
	if p.Lines()[0].Quantity != 2 || p.Witness().Legs[0] != "d2" {
		t.Fatal("the promise must copy lines and legs")
	}
	p.Lines()[0].Quantity = 9
	p.Witness().Legs[0] = "x"
	if p.Lines()[0].Quantity != 2 || p.Witness().Legs[0] != "d2" {
		t.Fatal("accessors return copies")
	}
}

func TestMakeValidation(t *testing.T) {
	cases := map[string]struct {
		mut  func(*Making)
		want error
	}{
		"bad promise id":        {func(m *Making) { m.ID = "-" }, shared.ErrValidation},
		"bad offer id":          {func(m *Making) { m.OfferID = "" }, shared.ErrValidation},
		"bad prefix":            {func(m *Making) { m.DestinationPrefix = "" }, shared.ErrValidation},
		"no book":               {func(m *Making) { m.BookID = "" }, shared.ErrValidation},
		"zero ttl":              {func(m *Making) { m.HoldTTL = 0 }, shared.ErrValidation},
		"no lines":              {func(m *Making) { m.Lines = nil }, shared.ErrValidation},
		"bad witness":           {func(m *Making) { m.Witness.DeliveryWaveID = "" }, shared.ErrValidation},
		"unknown tier":          {func(m *Making) { m.Terms.Tier = "X" }, ErrInvalidPromiseTerms},
		"unknown basis":         {func(m *Making) { m.Terms.Basis = "X" }, ErrInvalidPromiseTerms},
		"no policy version":     {func(m *Making) { m.Terms.PolicyVersion = "" }, ErrInvalidPromiseTerms},
		"inverted window":       {func(m *Making) { m.Terms.Window = shared.Window{Earliest: latest, Latest: earlier} }, ErrInvalidPromiseTerms},
		"malformed zone":        {func(m *Making) { m.Terms.Window.TimeZone = "America/Chi cago" }, ErrInvalidPromiseTerms},
		"no shipBy":             {func(m *Making) { m.Terms.ShipBy = time.Time{} }, ErrInvalidPromiseTerms},
		"shipBy == earliest":    {func(m *Making) { m.Terms.ShipBy = earlier }, ErrInvalidPromiseTerms},
		"shipBy after earliest": {func(m *Making) { m.Terms.ShipBy = earlier.Add(time.Second) }, ErrInvalidPromiseTerms},
		"nominal fast tier": {func(m *Making) {
			m.Terms.Basis, m.Witness = shared.BasisNominal, shared.Witness{NodeID: "n1"}
		}, ErrNominalBasisNotAllowedForTier},
		"line not at the node":   {func(m *Making) { m.Available = map[string]int{} }, ErrGroupSpansNodes},
		"quantity over the node": {func(m *Making) { m.Available = map[string]int{"ci-1001": 1} }, ErrGroupSpansNodes},
	}
	for name, c := range cases {
		in := making()
		c.mut(&in)
		if _, _, err := Make(in, t0); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
}

func TestMakeBoundaries(t *testing.T) {
	in := making()
	in.Terms.ShipBy = earlier.Add(-time.Nanosecond)
	in.Terms.Window.Latest = in.Terms.Window.Earliest
	in.Available = map[string]int{"ci-1001": 3}
	if _, _, err := Make(in, t0); err != nil {
		t.Fatalf("shipBy just before earliest, earliest == latest, spare stock: %v", err)
	}
	nominal := making()
	nominal.Terms.Tier, nominal.Terms.Basis, nominal.Witness = shared.TierStandard, shared.BasisNominal, shared.Witness{NodeID: "n1"}
	if _, _, err := Make(nominal, t0); err != nil {
		t.Fatalf("a NOMINAL STANDARD promise: %v", err)
	}
	// an offer persisted before windows named their zone still makes a promise (ADR 0011)
	legacy := making()
	legacy.Terms.Window.TimeZone = ""
	if p, _, err := Make(legacy, t0); err != nil || p.Terms().Window.TimeZone != "" {
		t.Fatalf("a window without a zone: %v", err)
	}
}

func TestConfirm(t *testing.T) {
	p := provisional(t)
	evs := mustEvents(t, 1)(p.Confirm("ord-1", 2, t0.Add(15*time.Minute-time.Nanosecond)))
	if p.State() != StateConfirmed || p.OrderID() != "ord-1" || p.GroupNo() != 2 || p.Version() != 2 {
		t.Fatalf("promise = %+v", p.Snapshot())
	}
	e, ok := evs[0].(PromiseConfirmed)
	if !ok || e.EventName() != "PromiseConfirmed" || e.OrderID != "ord-1" || e.GroupNo != 2 || e.AggregateVersion() != 2 || e.SubjectID() != "prm-1" || e.Entity() != "promise" {
		t.Fatalf("event = %+v", evs[0])
	}
}

func TestConfirmIsIdempotentForTheSameOrderGroup(t *testing.T) {
	for _, p := range []*Promise{confirmed(t), atRisk(t)} {
		v := p.Version()
		mustEvents(t, 0)(p.Confirm("ord-1", 1, t0.Add(time.Hour)))
		if p.Version() != v {
			t.Fatalf("a repeated confirm must change nothing (%s)", p.State())
		}
		if _, err := p.Confirm("ord-2", 1, t0); !errors.Is(err, shared.ErrIllegalTransition) {
			t.Fatalf("another order: %v", err)
		}
		if _, err := p.Confirm("ord-1", 2, t0); !errors.Is(err, shared.ErrIllegalTransition) {
			t.Fatalf("another group: %v", err)
		}
	}
}

func TestConfirmRefusals(t *testing.T) {
	p := provisional(t)
	if _, err := p.Confirm("ord-1", 1, t0.Add(15*time.Minute)); !errors.Is(err, ErrPromiseHoldExpired) || p.State() != StateProvisional || p.Version() != 1 {
		t.Fatalf("confirm exactly at holdExpiresAt: %v", err)
	}
	for _, bad := range []struct {
		order string
		group int
	}{{"", 1}, {"-", 1}, {"ord-1", 0}, {"ord-1", shared.MaxLines + 1}} {
		if _, err := provisional(t).Confirm(bad.order, bad.group, t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	for _, s := range []State{StateKept, StateBroken, StateReleased, StateCancelled} {
		if _, err := inState(t, s).Confirm("ord-1", 1, t0); !errors.Is(err, ErrPromiseClosed) {
			t.Errorf("confirm in %s: %v", s, err)
		}
	}
}

// transitions is the model's full table: which command is legal from which state.
var transitions = map[string]map[State]bool{
	"FlagAtRisk":  {StateConfirmed: true},
	"Revise":      {StateConfirmed: true, StateAtRisk: true},
	"Judge":       {StateConfirmed: true, StateAtRisk: true},
	"Cancel":      {StateConfirmed: true, StateAtRisk: true},
	"ReleaseHold": {StateProvisional: true},
}

func apply(t *testing.T, name string, p *Promise) error {
	t.Helper()
	switch name {
	case "FlagAtRisk":
		_, err := p.FlagAtRisk("PACKAGE_EXCEPTION", t0.Add(time.Hour))
		return err
	case "Revise":
		_, err := p.Revise(revising(), t0.Add(time.Hour))
		return err
	case "Judge":
		_, err := p.Judge(Verdict{DeliveredAt: latest}, t0.Add(time.Hour))
		return err
	case "Cancel":
		_, err := p.Cancel("CUSTOMER_REQUEST", t0.Add(time.Hour))
		return err
	default:
		_, err := p.ReleaseExpiredHold(t0.Add(time.Hour))
		return err
	}
}

func revising() Revising {
	tm := terms()
	tm.Window = shared.Window{Earliest: earlier.Add(24 * time.Hour), Latest: latest.Add(24 * time.Hour)}
	tm.ShipBy = shipBy.Add(24 * time.Hour)
	return Revising{Terms: tm, Witness: shared.Witness{NodeID: "n2", PickupDepartureID: "d9", DeliveryWaveID: "w2"}, BookID: "n2:d9", Reason: "DEPARTURE_CANCELLED"}
}

func TestEveryLegalAndIllegalTransition(t *testing.T) {
	for command, legal := range transitions {
		for _, s := range states {
			err := apply(t, command, inState(t, s))
			switch {
			case legal[s] && err != nil:
				t.Errorf("%s from %s must be legal: %v", command, s, err)
			case !legal[s] && s.Terminal() && !errors.Is(err, ErrPromiseClosed):
				t.Errorf("%s from terminal %s must be ErrPromiseClosed: %v", command, s, err)
			case !legal[s] && !s.Terminal() && !errors.Is(err, shared.ErrIllegalTransition):
				t.Errorf("%s from %s must be an illegal transition: %v", command, s, err)
			}
		}
	}
}

func TestStatePredicates(t *testing.T) {
	for _, s := range states {
		if !s.Valid() {
			t.Errorf("%s must be valid", s)
		}
		wantTerminal := s == StateKept || s == StateBroken || s == StateReleased || s == StateCancelled
		if s.Terminal() != wantTerminal || s.Active() != (s == StateConfirmed || s == StateAtRisk) {
			t.Errorf("%s: terminal=%v active=%v", s, s.Terminal(), s.Active())
		}
	}
	if State("X").Valid() || State("").Valid() {
		t.Error("unknown states are invalid")
	}
}

func TestFlagAtRisk(t *testing.T) {
	p := confirmed(t)
	evs := mustEvents(t, 1)(p.FlagAtRisk("DEPARTURE_CANCELLED", t0.Add(time.Hour)))
	e, ok := evs[0].(PromiseAtRisk)
	if p.State() != StateAtRisk || p.AtRiskReason() != "DEPARTURE_CANCELLED" || p.Version() != 3 || !ok || e.EventName() != "PromiseAtRisk" || e.OrderID != "ord-1" ||
		e.Reason != "DEPARTURE_CANCELLED" || !e.DetectedAt.Equal(t0.Add(time.Hour)) || e.SubjectID() != "prm-1" || e.Entity() != "promise" {
		t.Fatalf("promise %+v event %+v", p.Snapshot(), evs[0])
	}
	if _, err := p.FlagAtRisk("AGAIN", t0); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("a second flag: %v", err)
	}
	for _, reason := range []string{"", " x", "x\n"} {
		if _, err := confirmed(t).FlagAtRisk(reason, t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("reason %q: %v", reason, err)
		}
	}
}

func TestReviseNeverMovesEarlier(t *testing.T) {
	same := revising()
	same.Terms.Window = terms().Window
	same.Terms.ShipBy = shipBy
	if _, err := confirmed(t).Revise(same, t0); err != nil {
		t.Fatalf("an equal deliverByLatest is not earlier: %v", err)
	}
	earlierRev := revising()
	earlierRev.Terms.Window.Latest = latest.Add(-time.Nanosecond)
	earlierRev.Terms.Window.Earliest = earlier
	earlierRev.Terms.ShipBy = shipBy
	p := confirmed(t)
	if _, err := p.Revise(earlierRev, t0); !errors.Is(err, ErrRevisionMovesEarlier) || len(p.Revisions()) != 0 || p.Version() != 2 {
		t.Fatalf("an earlier latest: %v", err)
	}
}

func TestReviseValidation(t *testing.T) {
	cases := map[string]struct {
		mut  func(*Revising)
		want error
	}{
		"no reason":        {func(r *Revising) { r.Reason = "" }, shared.ErrValidation},
		"no book":          {func(r *Revising) { r.BookID = "" }, shared.ErrValidation},
		"invalid terms":    {func(r *Revising) { r.Terms.ShipBy = r.Terms.Window.Earliest }, ErrInvalidPromiseTerms},
		"nominal fast":     {func(r *Revising) { r.Terms.Basis = shared.BasisNominal; r.Witness = shared.Witness{NodeID: "n2"} }, ErrNominalBasisNotAllowedForTier},
		"bad witness":      {func(r *Revising) { r.Witness.PickupDepartureID = "" }, shared.ErrValidation},
		"tier gets faster": {func(r *Revising) { r.Terms.Tier = shared.TierSameDay }, ErrInvalidPromiseTerms},
	}
	for name, c := range cases {
		in := revising()
		c.mut(&in)
		if _, err := confirmed(t).Revise(in, t0); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", name, err, c.want)
		}
	}
	slower := revising()
	slower.Terms.Tier, slower.Terms.Basis, slower.Witness = shared.TierStandard, shared.BasisNominal, shared.Witness{NodeID: "n2"}
	if _, err := confirmed(t).Revise(slower, t0); err != nil {
		t.Fatalf("falling back to a NOMINAL STANDARD: %v", err)
	}
}

func TestMarkUnkeepableIsRaisedOnce(t *testing.T) {
	p := atRisk(t)
	evs := mustEvents(t, 1)(p.MarkUnkeepable(t0.Add(2 * time.Hour)))
	e, ok := evs[0].(PromiseUnkeepable)
	if !ok || e.EventName() != "PromiseUnkeepable" || e.OrderID != "ord-1" || e.GroupNo != 1 || e.Reason != "DEPARTURE_CANCELLED" || e.SubjectID() != "prm-1" || e.Entity() != "promise" || !p.Unkeepable() || p.State() != StateAtRisk {
		t.Fatalf("promise %+v event %+v", p.Snapshot(), evs[0])
	}
	v := p.Version()
	mustEvents(t, 0)(p.MarkUnkeepable(t0.Add(3 * time.Hour)))
	if p.Version() != v {
		t.Fatal("the second call changes nothing")
	}
	// a revision resets the flag, so a later failure raises again
	mustEvents(t, 1)(p.Revise(revising(), t0.Add(4*time.Hour)))
	if p.Unkeepable() {
		t.Fatal("a revision clears the flag")
	}
	for _, s := range []State{StateProvisional, StateConfirmed} {
		if _, err := inState(t, s).MarkUnkeepable(t0); !errors.Is(err, shared.ErrIllegalTransition) {
			t.Errorf("MarkUnkeepable from %s: %v", s, err)
		}
	}
	if _, err := inState(t, StateKept).MarkUnkeepable(t0); !errors.Is(err, ErrPromiseClosed) {
		t.Errorf("MarkUnkeepable from KEPT: %v", err)
	}
}

func TestJudgeDelivered(t *testing.T) {
	cases := []struct {
		name      string
		delivered time.Time
		outcome   Outcome
		late      int
		cause     Cause
		state     State
	}{
		{"well before", latest.Add(-5 * time.Hour), OutcomeKept, 0, "", StateKept},
		{"exactly on time", latest, OutcomeKept, 0, "", StateKept},
		{"one nanosecond late rounds up to a minute", latest.Add(time.Nanosecond), OutcomeBroken, 1, CauseLate, StateBroken},
		{"one second late", latest.Add(time.Second), OutcomeBroken, 1, CauseLate, StateBroken},
		{"exactly one minute late", latest.Add(time.Minute), OutcomeBroken, 1, CauseLate, StateBroken},
		{"61 seconds late", latest.Add(61 * time.Second), OutcomeBroken, 2, CauseLate, StateBroken},
		{"an hour late", latest.Add(time.Hour), OutcomeBroken, 60, CauseLate, StateBroken},
	}
	for _, c := range cases {
		for _, from := range []*Promise{confirmed(t), atRisk(t)} {
			evs := mustEvents(t, 1)(from.Judge(Verdict{DeliveredAt: c.delivered.In(time.FixedZone("z", 3600))}, latest.Add(2*time.Hour)))
			j := from.Judgement()
			if from.State() != c.state || j == nil || j.Outcome != c.outcome || j.LateByMinutes != c.late || j.Cause != c.cause || !j.DeliveredAt.Equal(c.delivered) ||
				!j.JudgedAt.Equal(latest.Add(2*time.Hour)) || j.DeliveredAt.Location() != time.UTC {
				t.Errorf("%s: promise %+v", c.name, from.Snapshot())
			}
			assertJudgeEvent(t, c.name, evs[0], c.outcome, c.late, c.cause, c.delivered)
		}
	}
}

func TestJudgeLostAndReturned(t *testing.T) {
	cases := []struct {
		name  string
		cause Cause
		at    time.Time
		late  int
	}{
		{"lost before the deadline", CauseLost, latest.Add(-time.Hour), 0},
		{"lost exactly at the deadline", CauseLost, latest, 0},
		{"lost 90 seconds after", CauseLost, latest.Add(90 * time.Second), 2},
		{"returned a day after", CauseReturned, latest.Add(24 * time.Hour), 1440},
	}
	for _, c := range cases {
		p := confirmed(t)
		evs := mustEvents(t, 1)(p.Judge(Verdict{Cause: c.cause, At: c.at}, c.at))
		j := p.Judgement()
		if p.State() != StateBroken || j.Outcome != OutcomeBroken || j.Cause != c.cause || j.LateByMinutes != c.late || !j.DeliveredAt.IsZero() {
			t.Errorf("%s: %+v", c.name, p.Snapshot())
		}
		assertJudgeEvent(t, c.name, evs[0], OutcomeBroken, c.late, c.cause, time.Time{})
	}
}

func TestJudgeValidation(t *testing.T) {
	for name, v := range map[string]Verdict{
		"no delivery":      {},
		"lost no time":     {Cause: CauseLost},
		"late is no input": {Cause: CauseLate, At: latest},
		"unknown cause":    {Cause: "X", At: latest},
	} {
		p := confirmed(t)
		if _, err := p.Judge(v, latest); !errors.Is(err, shared.ErrValidation) || p.State() != StateConfirmed || p.Version() != 2 {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCancel(t *testing.T) {
	p := atRisk(t)
	evs := mustEvents(t, 1)(p.Cancel("CUSTOMER_REQUEST", t0.Add(time.Hour)))
	e, ok := evs[0].(PromiseCancelled)
	if p.State() != StateCancelled || p.CancelReason() != "CUSTOMER_REQUEST" || !ok || e.EventName() != "PromiseCancelled" || e.OrderID != "ord-1" || e.Reason != "CUSTOMER_REQUEST" || e.SubjectID() != "prm-1" || e.Entity() != "promise" {
		t.Fatalf("promise %+v event %+v", p.Snapshot(), evs[0])
	}
	if _, err := confirmed(t).Cancel("", t0); !errors.Is(err, shared.ErrValidation) {
		t.Fatalf("reason required: %v", err)
	}
}

func TestReleaseExpiredHold(t *testing.T) {
	p := provisional(t)
	if _, err := p.ReleaseExpiredHold(t0.Add(15*time.Minute - time.Nanosecond)); !errors.Is(err, shared.ErrIllegalTransition) || p.State() != StateProvisional {
		t.Fatalf("release before expiry: %v", err)
	}
	evs := mustEvents(t, 1)(p.ReleaseExpiredHold(t0.Add(15 * time.Minute)))
	e, ok := evs[0].(PromiseReleased)
	if p.State() != StateReleased || p.ReleaseReason() != ReleaseHoldExpired || !ok || e.EventName() != "PromiseReleased" || e.Reason != "HOLD_EXPIRED" || e.SubjectID() != "prm-1" || e.Entity() != "promise" {
		t.Fatalf("promise %+v event %+v", p.Snapshot(), evs[0])
	}
}

func TestUsesDepartureAndWave(t *testing.T) {
	p := provisional(t)
	for id, want := range map[string]bool{"d1": true, "d2": true, "d3": true, "d4": false, "": false} {
		if p.UsesDeparture(id) != want {
			t.Errorf("UsesDeparture(%q) = %v", id, !want)
		}
	}
	if !p.UsesWave("w1") || p.UsesWave("w2") || p.UsesWave("") {
		t.Error("UsesWave")
	}
}

func TestRestoreRoundTripOfEveryState(t *testing.T) {
	for _, s := range states {
		p := inState(t, s)
		got, err := Restore(p.Snapshot())
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		a, b := p.Snapshot(), got.Snapshot()
		if a.State != b.State || a.OrderID != b.OrderID || a.Version != b.Version || a.BookID != b.BookID || a.Terms != b.Terms || len(a.Revisions) != len(b.Revisions) || (a.Judgement == nil) != (b.Judgement == nil) {
			t.Errorf("%s: %+v vs %+v", s, a, b)
		}
	}
	rev := atRisk(t)
	mustEvents(t, 1)(rev.Revise(revising(), t0.Add(2*time.Hour)))
	mustEvents(t, 1)(rev.FlagAtRisk("X", t0.Add(3*time.Hour)))
	mustEvents(t, 1)(rev.MarkUnkeepable(t0.Add(4 * time.Hour)))
	got, err := Restore(rev.Snapshot())
	if err != nil || !got.Unkeepable() || len(got.Revisions()) != 1 || got.AtRiskReason() != "X" {
		t.Fatalf("revised, at risk, unkeepable: %v %+v", err, got)
	}
}

func TestRestoreRejectsCorruptRows(t *testing.T) {
	cases := map[string]struct {
		base State
		mut  func(*Snapshot)
	}{
		"bad id":                        {StateProvisional, func(s *Snapshot) { s.ID = "-" }},
		"version zero":                  {StateProvisional, func(s *Snapshot) { s.Version = 0 }},
		"no madeAt":                     {StateProvisional, func(s *Snapshot) { s.MadeAt = time.Time{} }},
		"no book":                       {StateProvisional, func(s *Snapshot) { s.BookID = "" }},
		"unknown state":                 {StateProvisional, func(s *Snapshot) { s.State = "X" }},
		"bad prefix":                    {StateProvisional, func(s *Snapshot) { s.DestinationPrefix = "" }},
		"no lines":                      {StateProvisional, func(s *Snapshot) { s.Lines = nil }},
		"invalid terms":                 {StateProvisional, func(s *Snapshot) { s.Terms.ShipBy = s.Terms.Window.Earliest }},
		"bad witness":                   {StateProvisional, func(s *Snapshot) { s.Witness.PickupDepartureID = "" }},
		"provisional with an order":     {StateProvisional, func(s *Snapshot) { s.OrderID, s.GroupNo = "o", 1 }},
		"provisional with a revision":   {StateProvisional, func(s *Snapshot) { s.Revisions = []Revision{{No: 1}} }},
		"confirmed without order":       {StateConfirmed, func(s *Snapshot) { s.OrderID = "" }},
		"confirmed without group":       {StateConfirmed, func(s *Snapshot) { s.GroupNo = 0 }},
		"confirmed with a judgement":    {StateConfirmed, func(s *Snapshot) { s.Judgement = &Judgement{} }},
		"confirmed with a risk reason":  {StateConfirmed, func(s *Snapshot) { s.AtRiskReason = "x" }},
		"at risk without a reason":      {StateAtRisk, func(s *Snapshot) { s.AtRiskReason = "" }},
		"revision misnumbered":          {StateConfirmed, func(s *Snapshot) { s.Revisions = []Revision{{No: 2, PreviousTerms: terms(), RevisedAt: t0}} }},
		"revision without instant":      {StateConfirmed, func(s *Snapshot) { s.Revisions = []Revision{{No: 1, PreviousTerms: terms()}} }},
		"revision with invalid terms":   {StateConfirmed, func(s *Snapshot) { s.Revisions = []Revision{{No: 1, RevisedAt: t0}} }},
		"kept without judgement":        {StateKept, func(s *Snapshot) { s.Judgement = nil }},
		"kept with a broken outcome":    {StateKept, func(s *Snapshot) { s.Judgement.Outcome = OutcomeBroken }},
		"kept with a cause":             {StateKept, func(s *Snapshot) { s.Judgement.Cause = CauseLate }},
		"kept with late minutes":        {StateKept, func(s *Snapshot) { s.Judgement.LateByMinutes = 1 }},
		"kept without delivery":         {StateKept, func(s *Snapshot) { s.Judgement.DeliveredAt = time.Time{} }},
		"kept without judgedAt":         {StateKept, func(s *Snapshot) { s.Judgement.JudgedAt = time.Time{} }},
		"broken without a cause":        {StateBroken, func(s *Snapshot) { s.Judgement.Cause = "" }},
		"broken with negative lateness": {StateBroken, func(s *Snapshot) { s.Judgement.LateByMinutes = -1 }},
		"broken late without delivery":  {StateBroken, func(s *Snapshot) { s.Judgement.DeliveredAt = time.Time{} }},
		"broken lost with a delivery":   {StateBroken, func(s *Snapshot) { s.Judgement.Cause = CauseLost }},
		"released without a reason":     {StateReleased, func(s *Snapshot) { s.ReleaseReason = "" }},
		"released with an order":        {StateReleased, func(s *Snapshot) { s.OrderID, s.GroupNo = "o", 1 }},
		"cancelled without a reason":    {StateCancelled, func(s *Snapshot) { s.CancelReason = "" }},
		"cancelled without an order":    {StateCancelled, func(s *Snapshot) { s.OrderID = "" }},
	}
	for name, c := range cases {
		s := inState(t, c.base).Snapshot()
		c.mut(&s)
		if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: err = %v, want ErrInvalidSnapshot", name, err)
		}
	}
	// a LOST judgement without a delivery is a legal BROKEN row
	p := confirmed(t)
	mustEvents(t, 1)(p.Judge(Verdict{Cause: CauseLost, At: latest}, latest))
	if _, err := Restore(p.Snapshot()); err != nil {
		t.Fatalf("a LOST row: %v", err)
	}
}

func assertMade(t *testing.T, p *Promise) {
	t.Helper()
	eq(t, "id", p.ID(), ID("prm-1"))
	eq(t, "offer", p.OfferID(), "off-1")
	eq(t, "state", p.State(), StateProvisional)
	eq(t, "destination", p.DestinationPrefix(), "787")
	eq(t, "order", p.OrderID(), "")
	eq(t, "group", p.GroupNo(), 0)
	eq(t, "book", p.BookID(), "n1:d1")
	eq(t, "version", p.Version(), int64(1))
	eq(t, "revisions", len(p.Revisions()), 0)
	eq(t, "unkeepable", p.Unkeepable(), false)
	eq(t, "judgement", p.Judgement() == nil, true)
	eqTime(t, "holdExpiresAt", p.HoldExpiresAt(), t0.Add(15*time.Minute))
	eqTime(t, "madeAt", p.MadeAt(), t0)
	eq(t, "terms", p.Terms(), terms())
	eq(t, "witness node", p.Witness().NodeID, "n1")
	eq(t, "lines", len(p.Lines()), 1)
}

func TestRevise(t *testing.T) {
	p := atRisk(t)
	evs := mustEvents(t, 1)(p.Revise(revising(), t0.Add(2*time.Hour)))
	eq(t, "state", p.State(), StateConfirmed)
	eq(t, "risk reason", p.AtRiskReason(), "")
	eq(t, "book", p.BookID(), "n2:d9")
	eq(t, "node", p.Witness().NodeID, "n2")
	eq(t, "version", p.Version(), int64(4))
	rs := p.Revisions()
	if len(rs) != 1 {
		t.Fatalf("revisions = %+v", rs)
	}
	eq(t, "revision no", rs[0].No, 1)
	eq(t, "previous terms", rs[0].PreviousTerms, terms())
	eq(t, "revision reason", rs[0].Reason, "DEPARTURE_CANCELLED")
	eqTime(t, "revisedAt", rs[0].RevisedAt, t0.Add(2*time.Hour))
	assertRevisedEvent(t, evs[0])
	// a second revision numbers 2 and remembers the first terms
	r2 := revising()
	r2.Terms.Window.Latest = r2.Terms.Window.Latest.Add(time.Hour)
	mustEvents(t, 1)(p.Revise(r2, t0.Add(3*time.Hour)))
	rs = p.Revisions()
	if len(rs) != 2 {
		t.Fatalf("revisions = %+v", rs)
	}
	eq(t, "second revision no", rs[1].No, 2)
	eqTime(t, "second previous latest", rs[1].PreviousTerms.Window.Latest, latest.Add(24*time.Hour))
	// a CONFIRMED promise revises too
	mustEvents(t, 1)(confirmed(t).Revise(revising(), t0))
}

func assertRevisedEvent(t *testing.T, ev shared.Event) {
	t.Helper()
	e, ok := ev.(PromiseRevised)
	if !ok {
		t.Fatalf("event = %T", ev)
	}
	eq(t, "name", e.EventName(), "PromiseRevised")
	eq(t, "revision no", e.RevisionNo, 1)
	eqTime(t, "previous latest", e.PreviousDeliverByLatest, latest)
	eqTime(t, "new latest", e.Terms.Window.Latest, latest.Add(24*time.Hour))
	eq(t, "node", e.NodeID, "n2")
	eq(t, "order", e.OrderID, "ord-1")
	eq(t, "reason", e.Reason, "DEPARTURE_CANCELLED")
	eq(t, "subject", e.SubjectID(), "prm-1")
	eq(t, "entity", e.Entity(), "promise")
}

func assertJudgeEvent(t *testing.T, name string, ev shared.Event, outcome Outcome, late int, cause Cause, delivered time.Time) {
	t.Helper()
	if outcome == OutcomeKept {
		assertKeptEvent(t, name, ev, delivered)
		return
	}
	e, ok := ev.(PromiseBroken)
	if !ok {
		t.Errorf("%s: event = %T", name, ev)
		return
	}
	eq(t, name+" name", e.EventName(), "PromiseBroken")
	eq(t, name+" node", e.NodeID, "n1")
	eq(t, name+" order", e.OrderID, "ord-1")
	eq(t, name+" late", e.LateByMinutes, late)
	eq(t, name+" cause", e.Cause, cause)
	eqTime(t, name+" deliveredAt", e.DeliveredAt, delivered)
	eqTime(t, name+" latest", e.DeliverByLatest, latest)
	eq(t, name+" subject", e.SubjectID(), "prm-1")
	eq(t, name+" entity", e.Entity(), "promise")
}

func assertKeptEvent(t *testing.T, name string, ev shared.Event, delivered time.Time) {
	t.Helper()
	e, ok := ev.(PromiseKept)
	if !ok {
		t.Errorf("%s: event = %T", name, ev)
		return
	}
	eq(t, name+" name", e.EventName(), "PromiseKept")
	eq(t, name+" node", e.NodeID, "n1")
	eq(t, name+" order", e.OrderID, "ord-1")
	eqTime(t, name+" deliveredAt", e.DeliveredAt, delivered)
	eqTime(t, name+" latest", e.DeliverByLatest, latest)
	eq(t, name+" subject", e.SubjectID(), "prm-1")
	eq(t, name+" entity", e.Entity(), "promise")
}
