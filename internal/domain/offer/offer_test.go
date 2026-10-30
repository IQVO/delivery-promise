package offer

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

var now = time.Date(2026, 10, 22, 19, 5, 0, 0, time.UTC)

func quote() Quote {
	return Quote{
		ID: "off-1", GroupNo: 1, DestinationPrefix: "787", Tier: shared.TierNextDay,
		Lines:   []shared.Line{{CatalogItemID: "ci-1001", Quantity: 2}},
		Window:  shared.Window{Earliest: now.Add(18 * time.Hour), Latest: now.Add(30 * time.Hour), TimeZone: "America/Chicago"},
		Witness: shared.Witness{NodeID: "n1", PickupDepartureID: "d1", Legs: []string{"d2"}, DeliveryWaveID: "w1"},
		Basis:   shared.BasisScheduled, PolicyVersion: "v3", TTL: 10 * time.Minute,
		ExpiresAt: now.Add(10 * time.Minute), LatestMeetableAt: now.Add(3 * time.Hour), PickupCutoffAt: now.Add(4 * time.Hour),
	}
}

func TestNewCopiesItsInputs(t *testing.T) {
	q := quote()
	o, _, _ := New(q, now)
	q.Lines[0].Quantity = 99
	q.Witness.Legs[0] = "zz"
	if o.Lines()[0].Quantity != 2 || o.Witness().Legs[0] != "d2" {
		t.Fatal("the offer must own its lines and legs")
	}
	o.Lines()[0].Quantity = 7
	if o.Lines()[0].Quantity != 2 {
		t.Fatal("Lines returns a copy")
	}
}

func TestNewValidation(t *testing.T) {
	cases := map[string]func(*Quote){
		"bad id":      func(q *Quote) { q.ID = "-" },
		"group low":   func(q *Quote) { q.GroupNo = 0 },
		"group high":  func(q *Quote) { q.GroupNo = shared.MaxLines + 1 },
		"bad prefix":  func(q *Quote) { q.DestinationPrefix = "7 8" },
		"bad tier":    func(q *Quote) { q.Tier = "X" },
		"bad basis":   func(q *Quote) { q.Basis = "X" },
		"no policy":   func(q *Quote) { q.PolicyVersion = "" },
		"long policy": func(q *Quote) { q.PolicyVersion = strings.Repeat("v", shared.MaxID+1) },
		"zero ttl":    func(q *Quote) { q.TTL = 0 },
		"no lines":    func(q *Quote) { q.Lines = nil },
		"bad window":  func(q *Quote) { q.Window = shared.Window{Earliest: now.Add(time.Hour), Latest: now} },
		"no zone":     func(q *Quote) { q.Window.TimeZone = "" },
		"bad zone":    func(q *Quote) { q.Window.TimeZone = "Mars/Olympus Mons" },
		"bad witness": func(q *Quote) { q.Witness.DeliveryWaveID = "" },
	}
	for name, mut := range cases {
		q := quote()
		mut(&q)
		if _, _, err := New(q, now); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
	q := quote()
	q.Basis, q.Witness = shared.BasisNominal, shared.Witness{NodeID: "n1"}
	if _, _, err := New(q, now); !errors.Is(err, ErrNominalBasisNotAllowedForTier) {
		t.Errorf("a NOMINAL fast tier: err = %v", err)
	}
	// STANDARD may be NOMINAL.
	q.Tier = shared.TierStandard
	if _, _, err := New(q, now); err != nil {
		t.Fatalf("a nominal STANDARD offer is legal: %v", err)
	}
}

func TestExpiryBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		expires time.Time
		meet    time.Time
		ok      bool
	}{
		{"exactly the TTL", now.Add(10 * time.Minute), now.Add(time.Hour), true},
		{"exactly the meetable cutoff", now.Add(5 * time.Minute), now.Add(5 * time.Minute), true},
		{"one nanosecond past the TTL", now.Add(10*time.Minute + 1), now.Add(time.Hour), false},
		{"one nanosecond past the cutoff", now.Add(5*time.Minute + 1), now.Add(5 * time.Minute), false},
		{"not after quotedAt", now, now.Add(time.Hour), false},
		{"before quotedAt", now.Add(-time.Minute), now.Add(time.Hour), false},
		{"one nanosecond after quotedAt", now.Add(1), now.Add(time.Hour), true},
	}
	for _, c := range cases {
		q := quote()
		q.ExpiresAt, q.LatestMeetableAt = c.expires, c.meet
		_, _, err := New(q, now)
		if c.ok && err != nil || !c.ok && !errors.Is(err, ErrOfferExpiryTooLate) {
			t.Errorf("%s: err = %v, want ok=%v", c.name, err, c.ok)
		}
	}
}

func TestConsume(t *testing.T) {
	cases := []struct {
		name string
		at   time.Duration
		ok   bool
	}{{"inside the TTL", time.Minute, true}, {"one nanosecond before expiry", 10*time.Minute - 1, true}, {"exactly at expiry", 10 * time.Minute, false}, {"after expiry", time.Hour, false}}
	for _, c := range cases {
		o, _, _ := New(quote(), now)
		err := o.Consume(now.Add(c.at))
		if c.ok {
			if err != nil || o.State() != StateConsumed || o.Version() != 2 || !o.ClosedAt().Equal(now.Add(c.at)) {
				t.Errorf("%s: err=%v state=%s", c.name, err, o.State())
			}
		} else if !errors.Is(err, ErrOfferNotQuotable) || o.State() != StateQuoted || o.Version() != 1 {
			t.Errorf("%s: err=%v state=%s", c.name, err, o.State())
		}
	}
	// an offer cannot back two promises
	o, _, _ := New(quote(), now)
	_ = o.Consume(now)
	if err := o.Consume(now); !errors.Is(err, ErrOfferNotQuotable) {
		t.Fatalf("second consume: %v", err)
	}
	e, _, _ := New(quote(), now)
	_ = e.Expire(now.Add(time.Hour))
	if err := e.Consume(now); !errors.Is(err, ErrOfferNotQuotable) {
		t.Fatalf("consume of an expired offer: %v", err)
	}
	if o.Quotable(now) || e.Quotable(now) {
		t.Fatal("closed offers are not quotable")
	}
}

func TestExpire(t *testing.T) {
	o, _, _ := New(quote(), now)
	if err := o.Expire(now.Add(10*time.Minute - 1)); !errors.Is(err, shared.ErrIllegalTransition) || o.State() != StateQuoted {
		t.Fatalf("not yet past expiry: %v", err)
	}
	if err := o.Expire(now.Add(10 * time.Minute)); err != nil || o.State() != StateExpired || o.Version() != 2 || !o.ClosedAt().Equal(now.Add(10*time.Minute)) {
		t.Fatalf("expire at expiresAt: %v %s", err, o.State())
	}
	if err := o.Expire(now.Add(time.Hour)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("expire twice: %v", err)
	}
	c, _, _ := New(quote(), now)
	_ = c.Consume(now)
	if err := c.Expire(now.Add(time.Hour)); !errors.Is(err, shared.ErrIllegalTransition) {
		t.Fatalf("expire a consumed offer: %v", err)
	}
}

func TestQuotable(t *testing.T) {
	o, _, _ := New(quote(), now)
	if !o.Quotable(now) || !o.Quotable(now.Add(10*time.Minute-1)) || o.Quotable(now.Add(10*time.Minute)) {
		t.Fatal("quotable until expiresAt, exclusive")
	}
}

func TestRestoreRoundTrip(t *testing.T) {
	for _, step := range []string{"quoted", "consumed", "expired"} {
		o, _, _ := New(quote(), now)
		switch step {
		case "consumed":
			_ = o.Consume(now.Add(time.Minute))
		case "expired":
			_ = o.Expire(now.Add(time.Hour))
		}
		got, err := Restore(o.Snapshot())
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		a, b := o.Snapshot(), got.Snapshot()
		if a.State != b.State || a.Version != b.Version || !a.ClosedAt.Equal(b.ClosedAt) || !a.ExpiresAt.Equal(b.ExpiresAt) || a.Witness.NodeID != b.Witness.NodeID {
			t.Fatalf("%s: %+v vs %+v", step, a, b)
		}
	}
}

func TestRestoreRejectsCorruptRows(t *testing.T) {
	o, _, _ := New(quote(), now)
	base := o.Snapshot()
	cases := map[string]func(*Snapshot){
		"version zero":              func(s *Snapshot) { s.Version = 0 },
		"unknown state":             func(s *Snapshot) { s.State = "X" },
		"no quotedAt":               func(s *Snapshot) { s.QuotedAt = time.Time{} },
		"expiresAt equals quotedAt": func(s *Snapshot) { s.ExpiresAt = s.QuotedAt },
		"bad tier":                  func(s *Snapshot) { s.Tier = "X" },
		"QUOTED with closedAt":      func(s *Snapshot) { s.ClosedAt = now },
		"CONSUMED without closedAt": func(s *Snapshot) { s.State = StateConsumed },
	}
	for name, mut := range cases {
		s := base
		mut(&s)
		if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: err = %v, want ErrInvalidSnapshot", name, err)
		}
	}
	// the boundary: expiresAt one nanosecond after quotedAt is legal
	s := base
	s.ExpiresAt = s.QuotedAt.Add(1)
	if _, err := Restore(s); err != nil {
		t.Errorf("expiresAt just after quotedAt: %v", err)
	}
	// a row persisted before the window named its zone stays readable (ADR 0011)
	legacy := base
	legacy.Window.TimeZone = ""
	if got, err := Restore(legacy); err != nil || got.Window().TimeZone != "" {
		t.Errorf("a row without a zone: %v", err)
	}
	closed := base
	closed.State, closed.ClosedAt = StateConsumed, now
	if _, err := Restore(closed); err != nil {
		t.Errorf("a consumed row with closedAt: %v", err)
	}
	if !StateQuoted.Valid() || !StateExpired.Valid() || !StateConsumed.Valid() || State("").Valid() {
		t.Error("State.Valid")
	}
}

func TestNewQuotesAnOffer(t *testing.T) {
	o, evs, err := New(quote(), now.In(time.FixedZone("z", 7200)))
	if err != nil {
		t.Fatal(err)
	}
	assertQuotedOffer(t, o)
	if len(evs) != 1 {
		t.Fatalf("events = %d", len(evs))
	}
	assertQuotedEvent(t, evs[0], o)
}

func assertQuotedOffer(t *testing.T, o *Offer) {
	t.Helper()
	eq(t, "id", o.ID(), ID("off-1"))
	eq(t, "groupNo", o.GroupNo(), 1)
	eq(t, "destination", o.DestinationPrefix(), "787")
	eq(t, "tier", o.Tier(), shared.TierNextDay)
	eq(t, "state", o.State(), StateQuoted)
	eq(t, "basis", o.Basis(), shared.BasisScheduled)
	eq(t, "policy", o.PolicyVersion(), "v3")
	eq(t, "version", o.Version(), int64(1))
	eq(t, "window", o.Window(), quote().Window)
	eq(t, "witness node", o.Witness().NodeID, "n1")
	eq(t, "lines", len(o.Lines()), 1)
	eqTime(t, "closedAt", o.ClosedAt(), time.Time{})
	eqTime(t, "quotedAt", o.QuotedAt(), now)
	eq(t, "quotedAt zone", o.QuotedAt().Location(), time.UTC)
	eqTime(t, "expiresAt", o.ExpiresAt(), now.Add(10*time.Minute))
	eqTime(t, "pickup cutoff", o.PickupCutoffAt(), now.Add(4*time.Hour))
}

func assertQuotedEvent(t *testing.T, ev shared.Event, o *Offer) {
	t.Helper()
	e, ok := ev.(DeliveryOfferQuoted)
	if !ok {
		t.Fatalf("event = %T", ev)
	}
	eq(t, "name", e.EventName(), "DeliveryOfferQuoted")
	eq(t, "entity", e.Entity(), "deliveryoffer")
	eq(t, "subject", e.SubjectID(), "off-1")
	eq(t, "version", e.AggregateVersion(), int64(1))
	eqTime(t, "at", e.OccurredAt(), now)
	eq(t, "tier", e.Tier, shared.TierNextDay)
	eq(t, "node", e.NodeID, "n1")
	eq(t, "destination", e.DestinationPrefix, "787")
	eq(t, "basis", e.Basis, shared.BasisScheduled)
	eq(t, "policy", e.PolicyVersion, "v3")
	eqTime(t, "expiresAt", e.ExpiresAt, o.ExpiresAt())
}
