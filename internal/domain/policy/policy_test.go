package policy

import (
	"errors"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

var t0 = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func valid() Input {
	return Input{OfferTTLMinutes: 10, HoldTTLMinutes: 15, PackagesPerOrder: 2, SharePercent: 80, Deadlines: []Deadline{
		{Tier: shared.TierStandard, Minute: 23*60 + 59}, {Tier: shared.TierNextDay, RegionID: "R-SC", Minute: 20 * 60}, {Tier: shared.TierNextDay, Minute: 22 * 60},
	}}
}

func TestDefault(t *testing.T) {
	p := Default()
	if p.Version() != 0 || p.VersionName() != "v0" || p.OfferTTL() != 10*time.Minute || p.HoldTTL() != 15*time.Minute ||
		p.PackagesPerOrder() != 1 || p.SharePercent() != 100 || len(p.Deadlines()) != 0 || !p.PublishedAt().IsZero() {
		t.Fatalf("default policy wrong: %+v", p.Snapshot())
	}
	if p.PastDeadline(shared.TierNextDay, "R", 24*60-1) {
		t.Fatal("no deadline: never past")
	}
}

func TestPublishValidation(t *testing.T) {
	cases := map[string]func(*Input){
		"offer ttl low":      func(i *Input) { i.OfferTTLMinutes = 0 },
		"offer ttl high":     func(i *Input) { i.OfferTTLMinutes = MaxTTLMinutes + 1 },
		"hold ttl low":       func(i *Input) { i.HoldTTLMinutes = 0 },
		"hold ttl high":      func(i *Input) { i.HoldTTLMinutes = MaxTTLMinutes + 1 },
		"packages low":       func(i *Input) { i.PackagesPerOrder = 0 },
		"packages high":      func(i *Input) { i.PackagesPerOrder = MaxPackagesPerOrder + 1 },
		"share low":          func(i *Input) { i.SharePercent = 0 },
		"share high":         func(i *Input) { i.SharePercent = MaxSharePercent + 1 },
		"bad tier":           func(i *Input) { i.Deadlines = []Deadline{{Tier: "X"}} },
		"bad region":         func(i *Input) { i.Deadlines = []Deadline{{Tier: shared.TierNextDay, RegionID: "-"}} },
		"minute negative":    func(i *Input) { i.Deadlines = []Deadline{{Tier: shared.TierNextDay, Minute: -1}} },
		"minute past day":    func(i *Input) { i.Deadlines = []Deadline{{Tier: shared.TierNextDay, Minute: 1440}} },
		"duplicate deadline": func(i *Input) { i.Deadlines = []Deadline{{Tier: shared.TierNextDay}, {Tier: shared.TierNextDay}} },
		"too many deadlines": func(i *Input) { i.Deadlines = make([]Deadline, MaxDeadlines+1) },
	}
	for name, mut := range cases {
		in := valid()
		mut(&in)
		if _, _, err := Publish(0, in, t0); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
	// the bounds themselves are legal
	in := Input{OfferTTLMinutes: MaxTTLMinutes, HoldTTLMinutes: 1, PackagesPerOrder: MaxPackagesPerOrder, SharePercent: MaxSharePercent}
	if _, _, err := Publish(0, in, t0); err != nil {
		t.Fatalf("bounds refused: %v", err)
	}
	in = Input{OfferTTLMinutes: 1, HoldTTLMinutes: MaxTTLMinutes, PackagesPerOrder: 1, SharePercent: 1}
	if _, _, err := Publish(0, in, t0); err != nil {
		t.Fatalf("lower bounds refused: %v", err)
	}
	for _, cur := range []int{-1, shared.MaxInt32} {
		if _, _, err := Publish(cur, valid(), t0); !errors.Is(err, ErrVersionNotNext) {
			t.Errorf("current %d: err = %v", cur, err)
		}
	}
	max := make([]Deadline, MaxDeadlines)
	for i := range max {
		max[i] = Deadline{Tier: shared.TierStandard, RegionID: "r" + string(rune('a'+i/26)) + string(rune('a'+i%26))}
	}
	in = valid()
	in.Deadlines = max
	if _, _, err := Publish(0, in, t0); err != nil {
		t.Fatalf("MaxDeadlines refused: %v", err)
	}
}

func TestRestoreRoundTripAndBoundaries(t *testing.T) {
	p, _, _ := Publish(0, valid(), t0)
	got, err := Restore(p.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if s1, s2 := p.Snapshot(), got.Snapshot(); s1.Version != s2.Version || s1.OfferTTLMinutes != s2.OfferTTLMinutes || len(s1.Deadlines) != len(s2.Deadlines) || !s1.PublishedAt.Equal(s2.PublishedAt) {
		t.Fatalf("round trip: %+v vs %+v", s1, s2)
	}
	base := p.Snapshot()
	bad := map[string]func(*Snapshot){
		"version zero":      func(s *Snapshot) { s.Version = 0 },
		"version too big":   func(s *Snapshot) { s.Version = shared.MaxInt32 + 1 },
		"no publishedAt":    func(s *Snapshot) { s.PublishedAt = time.Time{} },
		"offer ttl invalid": func(s *Snapshot) { s.OfferTTLMinutes = 0 },
	}
	for name, mut := range bad {
		s := base
		mut(&s)
		if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: err = %v, want ErrInvalidSnapshot", name, err)
		}
	}
	s := base
	s.Version = shared.MaxInt32
	if _, err := Restore(s); err != nil {
		t.Errorf("the ceiling version is legal: %v", err)
	}
	s = base
	s.Version = 1
	if _, err := Restore(s); err != nil {
		t.Errorf("version 1 is legal: %v", err)
	}
}

func TestPastDeadline(t *testing.T) {
	p, _, _ := Publish(0, valid(), t0)
	cases := []struct {
		name   string
		tier   shared.Tier
		region string
		minute int
		want   bool
	}{
		{"region override before", shared.TierNextDay, "R-SC", 20 * 60, false},
		{"region override after", shared.TierNextDay, "R-SC", 20*60 + 1, true},
		{"other region uses the all-region deadline", shared.TierNextDay, "R-NE", 21 * 60, false},
		{"other region after all-region deadline", shared.TierNextDay, "R-NE", 22*60 + 1, true},
		{"all-region exactly at the deadline", shared.TierNextDay, "R-NE", 22 * 60, false},
		{"tier without a deadline", shared.TierSameDay, "R-SC", 23*60 + 59, false},
		{"standard late", shared.TierStandard, "R-SC", 23*60 + 59, false},
	}
	for _, c := range cases {
		if got := p.PastDeadline(c.tier, c.region, c.minute); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestPublish(t *testing.T) {
	p, evs, err := Publish(2, valid(), t0.In(time.FixedZone("x", 3600)))
	if err != nil {
		t.Fatal(err)
	}
	assertPublished(t, p)
	ds := p.Deadlines()
	if len(ds) != 3 || ds[0].Tier != shared.TierNextDay || ds[0].RegionID != "" || ds[1].RegionID != "R-SC" || ds[2].Tier != shared.TierStandard {
		t.Fatalf("deadlines must be sorted by tier then region: %+v", ds)
	}
	ds[0].Minute = 1
	if p.Deadlines()[0].Minute == 1 {
		t.Fatal("Deadlines must return a copy")
	}
	if len(evs) != 1 {
		t.Fatalf("events = %d", len(evs))
	}
	assertPublishedEvent(t, evs[0])
}

func assertPublished(t *testing.T, p *Policy) {
	t.Helper()
	eq(t, "version", p.Version(), 3)
	eq(t, "name", p.VersionName(), "v3")
	eq(t, "offer ttl", p.OfferTTL(), 10*time.Minute)
	eq(t, "hold ttl", p.HoldTTL(), 15*time.Minute)
	eq(t, "packages", p.PackagesPerOrder(), 2)
	eq(t, "share", p.SharePercent(), 80)
	eqTime(t, "publishedAt", p.PublishedAt(), t0)
	eq(t, "publishedAt zone", p.PublishedAt().Location(), time.UTC)
}

func assertPublishedEvent(t *testing.T, ev shared.Event) {
	t.Helper()
	e, ok := ev.(PromisePolicyPublished)
	if !ok {
		t.Fatalf("event = %T", ev)
	}
	eq(t, "name", e.EventName(), "PromisePolicyPublished")
	eq(t, "entity", e.Entity(), "promisepolicy")
	eq(t, "subject", e.SubjectID(), "v3")
	eq(t, "aggregate version", e.AggregateVersion(), int64(3))
	eqTime(t, "at", e.OccurredAt(), t0)
	eq(t, "number", e.Number, 3)
	eq(t, "offer ttl", e.OfferTTLMinutes, 10)
	eq(t, "hold ttl", e.HoldTTLMinutes, 15)
	eq(t, "packages", e.PackagesPerOrder, 2)
	eq(t, "share", e.SharePercent, 80)
	eq(t, "deadlines", len(e.Deadlines), 3)
	eqTime(t, "publishedAt", e.PublishedAt, t0)
	eq(t, "clock", e.Deadlines[2].ClockOf(), "23:59")
}
