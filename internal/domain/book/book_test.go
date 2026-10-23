package book

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/domain/shared"
)

var (
	cutoff   = time.Date(2026, 10, 22, 23, 0, 0, 0, time.UTC)
	deadline = cutoff.Add(-95 * time.Minute)
	now      = deadline.Add(-time.Hour)
)

func opening(share int) Opening {
	return Opening{NodeID: "N-AUS3", PickupDepartureID: "dep-1800", CutoffAt: cutoff, HoldDeadlineAt: deadline, Share: share}
}

func openBook(t *testing.T, share int) *Book {
	t.Helper()
	b, err := Open(opening(share))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOpen(t *testing.T) {
	b := openBook(t, 3)
	if b.ID() != "N-AUS3:dep-1800" || b.NodeID() != "N-AUS3" || b.PickupDepartureID() != "dep-1800" || b.State() != StateOpen || b.Share() != 3 ||
		b.Provisional() != 0 || b.Confirmed() != 0 || b.Room() != 3 || b.Version() != 1 || !b.ClosedAt().IsZero() ||
		!b.CutoffAt().Equal(cutoff) || !b.HoldDeadlineAt().Equal(deadline) {
		t.Fatalf("book = %+v", b.Snapshot())
	}
	if MakeID("a", "b") != "a:b" {
		t.Fatal("MakeID")
	}
	// the deadline may equal the cutoff (zero handling) and the share may be the ceiling
	o := opening(MaxShare)
	o.HoldDeadlineAt = cutoff
	if _, err := Open(o); err != nil {
		t.Fatalf("boundary opening refused: %v", err)
	}
}

func TestOpenValidation(t *testing.T) {
	cases := map[string]func(*Opening){
		"bad node":              func(o *Opening) { o.NodeID = "-" },
		"bad pickup":            func(o *Opening) { o.PickupDepartureID = "" },
		"no cutoff":             func(o *Opening) { o.CutoffAt = time.Time{} },
		"no deadline":           func(o *Opening) { o.HoldDeadlineAt = time.Time{} },
		"deadline after cutoff": func(o *Opening) { o.HoldDeadlineAt = cutoff.Add(time.Nanosecond) },
		"share zero":            func(o *Opening) { o.Share = 0 },
		"share too big":         func(o *Opening) { o.Share = MaxShare + 1 },
	}
	for name, mut := range cases {
		o := opening(5)
		mut(&o)
		if _, err := Open(o); !errors.Is(err, shared.ErrValidation) {
			t.Errorf("%s: err = %v, want ErrValidation", name, err)
		}
	}
	// 64 + ':' + 64 characters is the longest legal book id
	long := opening(1)
	long.NodeID, long.PickupDepartureID = "n"+strings.Repeat("x", shared.MaxID-1), "p"+strings.Repeat("x", shared.MaxID-1)
	if _, err := Open(long); err != nil {
		t.Fatalf("the longest id is legal: %v", err)
	}
}

func TestHoldCountsConfirmedToo(t *testing.T) {
	b := openBook(t, 2)
	_, _ = b.Hold(now)
	_ = b.ConfirmHold()
	evs, err := b.Hold(now)
	if err != nil || len(evs) != 1 {
		t.Fatalf("provisional 1 + confirmed 1 = share: %v %v", evs, err)
	}
	if _, err := b.Hold(now); !errors.Is(err, ErrPromiseCapacityExhausted) {
		t.Fatalf("err = %v", err)
	}
}

func TestHoldDeadlineAndClosedBook(t *testing.T) {
	b := openBook(t, 5)
	if _, err := b.Hold(deadline.Add(-time.Nanosecond)); err != nil {
		t.Fatalf("a hold just before the deadline: %v", err)
	}
	if _, err := b.Hold(deadline); !errors.Is(err, ErrBookClosed) {
		t.Fatalf("a hold at the deadline: %v", err)
	}
	if _, err := b.Hold(cutoff.Add(time.Hour)); !errors.Is(err, ErrBookClosed) {
		t.Fatalf("a hold after the deadline: %v", err)
	}
	c := openBook(t, 5)
	if err := c.Close(cutoff); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Hold(now); !errors.Is(err, ErrBookClosed) {
		t.Fatalf("a hold on a CLOSED book: %v", err)
	}
	// closed has precedence over full
	f := openBook(t, 1)
	_, _ = f.Hold(now)
	_ = f.Close(cutoff)
	if _, err := f.Hold(now); !errors.Is(err, ErrBookClosed) {
		t.Fatalf("closed before exhausted: %v", err)
	}
}

func TestConfirmAndReleaseKeepCountersConsistent(t *testing.T) {
	b := openBook(t, 3)
	_, _ = b.Hold(now)
	_, _ = b.Hold(now)
	if err := b.ConfirmHold(); err != nil || b.Provisional() != 1 || b.Confirmed() != 1 {
		t.Fatalf("confirm: %v %+v", err, b.Snapshot())
	}
	if err := b.ReleaseProvisional(); err != nil || b.Provisional() != 0 || b.Confirmed() != 1 {
		t.Fatalf("release provisional: %v %+v", err, b.Snapshot())
	}
	if err := b.ReleaseConfirmed(); err != nil || b.Confirmed() != 0 || b.Room() != 3 {
		t.Fatalf("release confirmed: %v %+v", err, b.Snapshot())
	}
	if b.Version() != 6 {
		t.Fatalf("every change bumps the version: %d", b.Version())
	}
}

func TestUnderflowIsRefusedAndChangesNothing(t *testing.T) {
	for name, op := range map[string]func(*Book) error{
		"confirm":             (*Book).ConfirmHold,
		"release provisional": (*Book).ReleaseProvisional,
		"release confirmed":   (*Book).ReleaseConfirmed,
	} {
		b := openBook(t, 3)
		if err := op(b); !errors.Is(err, ErrCapacityUnderflow) || b.Version() != 1 || b.Provisional() != 0 || b.Confirmed() != 0 {
			t.Errorf("%s: err=%v %+v", name, err, b.Snapshot())
		}
	}
	// a provisional hold does not satisfy a confirmed release, and the reverse
	b := openBook(t, 3)
	_, _ = b.Hold(now)
	if err := b.ReleaseConfirmed(); !errors.Is(err, ErrCapacityUnderflow) {
		t.Errorf("release confirmed with only a provisional hold: %v", err)
	}
	_ = b.ConfirmHold()
	if err := b.ReleaseProvisional(); !errors.Is(err, ErrCapacityUnderflow) {
		t.Errorf("release provisional with only a confirmed hold: %v", err)
	}
}

func TestConfirmAndReleaseStayLegalOnAClosedBook(t *testing.T) {
	b := openBook(t, 3)
	_, _ = b.Hold(now)
	_, _ = b.Hold(now)
	if err := b.Close(cutoff); err != nil {
		t.Fatal(err)
	}
	if err := b.ConfirmHold(); err != nil {
		t.Fatalf("confirm on a closed book: %v", err)
	}
	if err := b.ReleaseProvisional(); err != nil {
		t.Fatalf("release on a closed book: %v", err)
	}
	if err := b.ReleaseConfirmed(); err != nil {
		t.Fatalf("release confirmed on a closed book: %v", err)
	}
}

func TestClose(t *testing.T) {
	b := openBook(t, 3)
	if err := b.Close(cutoff.Add(-time.Nanosecond)); !errors.Is(err, shared.ErrIllegalTransition) || b.State() != StateOpen {
		t.Fatalf("close before the cutoff: %v", err)
	}
	if err := b.Close(cutoff); err != nil || b.State() != StateClosed || !b.ClosedAt().Equal(cutoff) || b.Version() != 2 {
		t.Fatalf("close at the cutoff: %v %+v", err, b.Snapshot())
	}
	if err := b.Close(cutoff.Add(time.Hour)); !errors.Is(err, shared.ErrIllegalTransition) || b.Version() != 2 {
		t.Fatalf("close twice: %v", err)
	}
}

func TestRestore(t *testing.T) {
	b := openBook(t, 4)
	_, _ = b.Hold(now)
	_ = b.ConfirmHold()
	_, _ = b.Hold(now)
	_ = b.Close(cutoff)
	got, err := Restore(b.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if got.Snapshot() != b.Snapshot() {
		t.Fatalf("round trip: %+v vs %+v", got.Snapshot(), b.Snapshot())
	}
	base := openBook(t, 4).Snapshot()
	cases := map[string]func(*Snapshot){
		"wrong id":              func(s *Snapshot) { s.ID = "x:y" },
		"bad node":              func(s *Snapshot) { s.NodeID = "-" },
		"version zero":          func(s *Snapshot) { s.Version = 0 },
		"negative provisional":  func(s *Snapshot) { s.Provisional = -1 },
		"negative confirmed":    func(s *Snapshot) { s.Confirmed = -1 },
		"over the share":        func(s *Snapshot) { s.Provisional, s.Confirmed = 3, 2 },
		"unknown state":         func(s *Snapshot) { s.State = "X" },
		"OPEN with closedAt":    func(s *Snapshot) { s.ClosedAt = cutoff },
		"CLOSED without closed": func(s *Snapshot) { s.State = StateClosed },
		"share zero":            func(s *Snapshot) { s.Share = 0 },
	}
	for name, mut := range cases {
		s := base
		mut(&s)
		if _, err := Restore(s); !errors.Is(err, shared.ErrInvalidSnapshot) {
			t.Errorf("%s: err = %v, want ErrInvalidSnapshot", name, err)
		}
	}
	full := base
	full.Provisional, full.Confirmed = 2, 2
	if _, err := Restore(full); err != nil {
		t.Errorf("provisional + confirmed == share is legal: %v", err)
	}
	one := base
	one.Version = 1
	if _, err := Restore(one); err != nil {
		t.Errorf("version 1 is legal: %v", err)
	}
}

func TestThroughputOrders(t *testing.T) {
	cases := []struct {
		name                string
		cap                 Throughput
		window, day, expect int
	}{
		{"hourly, whole hours", Throughput{400, true}, 120, 0, 800},
		{"hourly rounds up", Throughput{100, true}, 90, 0, 150},
		{"hourly partial", Throughput{10, true}, 7, 0, 2},
		{"daily share of the day", Throughput{1000, false}, 480, 960, 500},
		{"daily rounds up", Throughput{10, false}, 1, 960, 1},
		{"daily window longer than the day is capped by the cap", Throughput{1000, false}, 1920, 960, 1000},
		{"daily with unknown day minutes is the whole cap", Throughput{1000, false}, 100, 0, 1000},
		{"no cap", Throughput{0, true}, 60, 0, 0},
		{"no window", Throughput{10, true}, 0, 0, 0},
	}
	for _, c := range cases {
		if got := ThroughputOrders(c.cap, c.window, c.day); got != c.expect {
			t.Errorf("%s: got %d, want %d", c.name, got, c.expect)
		}
	}
}

func TestShare(t *testing.T) {
	cases := []struct {
		name                       string
		maxOrders, throughput, pct int
		want                       int
	}{
		{"min of slot and throughput: slot", 2500, 3000, 100, 2500},
		{"min of slot and throughput: throughput", 2500, 800, 100, 800},
		{"slot only", 2500, 0, 100, 2500},
		{"throughput only", 0, 800, 100, 800},
		{"percent share", 2500, 3000, 80, 2000},
		{"percent rounds up", 10, 0, 25, 3},
		{"nothing bounds it: at least one", 0, 0, 100, 1},
		{"tiny share is at least one", 1, 0, 1, 1},
		{"ceiling", MaxShare, 0, 100, MaxShare},
	}
	for _, c := range cases {
		if got := Share(c.maxOrders, c.throughput, c.pct); got != c.want {
			t.Errorf("%s: got %d, want %d", c.name, got, c.want)
		}
	}
}

func TestHoldUntilFullRaisesExhaustedOnce(t *testing.T) {
	b := openBook(t, 2)
	evs, err := b.Hold(now)
	if err != nil || len(evs) != 0 || b.Provisional() != 1 || b.Version() != 2 || b.Room() != 1 {
		t.Fatalf("first hold: %v %v %+v", evs, err, b.Snapshot())
	}
	evs, err = b.Hold(now.Add(time.Minute))
	if err != nil || len(evs) != 1 || b.Room() != 0 {
		t.Fatalf("second hold must fill the book: %v %v", evs, err)
	}
	assertExhausted(t, evs[0])
	if evs, err = b.Hold(now); !errors.Is(err, ErrPromiseCapacityExhausted) || evs != nil || b.Provisional() != 2 || b.Version() != 3 {
		t.Fatalf("a full book refuses: %v %v", evs, err)
	}
}

func assertExhausted(t *testing.T, ev shared.Event) {
	t.Helper()
	e, ok := ev.(PromiseBookExhausted)
	if !ok {
		t.Fatalf("event = %T", ev)
	}
	eq(t, "name", e.EventName(), "PromiseBookExhausted")
	eq(t, "entity", e.Entity(), "promisebook")
	eq(t, "subject", e.SubjectID(), "N-AUS3:dep-1800")
	eq(t, "version", e.AggregateVersion(), int64(3))
	eqTime(t, "at", e.OccurredAt(), now.Add(time.Minute))
	eq(t, "node", e.NodeID, "N-AUS3")
	eq(t, "pickup", e.PickupDepartureID, "dep-1800")
	eq(t, "share", e.Share, 2)
}
