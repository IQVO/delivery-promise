package memory

import (
	"context"
	"testing"
	"time"
)

var tt0 = time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)

// load appends one observed transit of minutes to the (origin, dest) pair
// through the two consumer events.
func load(t *testing.T, ta *TransitActuals, loadID, origin, dest string, minutes int, at time.Time) {
	t.Helper()
	if err := ta.RecordLoadDeparted(context.Background(), loadID, origin, at); err != nil {
		t.Fatal(err)
	}
	if err := ta.RecordLoadArrived(context.Background(), loadID, dest, at.Add(time.Duration(minutes)*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

func TestTransitActualsPairsLoadsIntoRings(t *testing.T) {
	ta := NewStore().TransitActuals()
	ctx := context.Background()
	for i := range 20 {
		load(t, ta, "ld-"+string(rune('a'+i)), "N-A", "N-B", 60+i, tt0.Add(time.Duration(i)*time.Hour))
	}
	ok, minutes := ta.MeasuredTransit(ctx, "N-A", "N-B", 90, 20, 28, tt0.Add(24*time.Hour))
	if !ok || minutes != 77 { // nearest rank ceil(0.9*20) = 18th of 60..79
		t.Fatalf("P90 = %d ok=%v, want 77", minutes, ok)
	}
	// below minObservations the timetable stands
	ok, _ = ta.MeasuredTransit(ctx, "N-A", "N-B", 90, 21, 28, tt0.Add(24*time.Hour))
	if ok {
		t.Fatal("fewer observations than minObservations must not be measured")
	}
	// an unknown pair is never measured
	if ok, _ := ta.MeasuredTransit(ctx, "N-A", "N-Z", 90, 1, 28, tt0); ok {
		t.Fatal("an unknown pair must not be measured")
	}
}

func TestTransitActualsArrivedWithoutDepartedIsANoOp(t *testing.T) {
	ta := NewStore().TransitActuals()
	ctx := context.Background()
	if err := ta.RecordLoadArrived(ctx, "ld-x", "N-B", tt0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ta.MeasuredTransit(ctx, "N-A", "N-B", 90, 1, 28, tt0); ok {
		t.Fatal("an arrival without a departure must record nothing")
	}
	// a second arrival of the same load: the open row is gone, nothing doubles
	load(t, ta, "ld-1", "N-A", "N-B", 90, tt0)
	if err := ta.RecordLoadArrived(ctx, "ld-1", "N-B", tt0.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	ok, minutes := ta.MeasuredTransit(ctx, "N-A", "N-B", 100, 1, 28, tt0.Add(time.Hour))
	if !ok || minutes != 90 {
		t.Fatalf("minutes = %d ok=%v, want one 90-minute observation", minutes, ok)
	}
}

func TestTransitActualsRingKeepsTheLatestTwoHundred(t *testing.T) {
	ta := NewStore().TransitActuals()
	ctx := context.Background()
	for i := 1; i <= 205; i++ {
		load(t, ta, "ld-"+string(rune('a'+i%26))+string(rune('a'+i/26)), "N-A", "N-B", i, tt0.Add(time.Duration(i)*time.Minute))
	}
	ok, minutes := ta.MeasuredTransit(ctx, "N-A", "N-B", 1, 1, 28, tt0.Add(10*time.Hour))
	if !ok || minutes != 7 { // the ring holds 6..205; rank ceil(0.01*200) = 2nd smallest
		t.Fatalf("ring cap: P1 = %d ok=%v, want 7", minutes, ok)
	}
	ok, minutes = ta.MeasuredTransit(ctx, "N-A", "N-B", 100, 1, 28, tt0.Add(10*time.Hour))
	if !ok || minutes != 205 {
		t.Fatalf("ring cap: P100 = %d ok=%v, want 205", minutes, ok)
	}
}

func TestTransitActualsWindowPrunesOnRead(t *testing.T) {
	ta := NewStore().TransitActuals()
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 6, 0, 0, 0, time.UTC)
	load(t, ta, "ld-old", "N-A", "N-B", 300, now.AddDate(0, 0, -40))
	load(t, ta, "ld-new-1", "N-A", "N-B", 100, now.AddDate(0, 0, -2))
	load(t, ta, "ld-new-2", "N-A", "N-B", 120, now.AddDate(0, 0, -1))
	ok, minutes := ta.MeasuredTransit(ctx, "N-A", "N-B", 100, 2, 28, now)
	if !ok || minutes != 120 { // the 40-day-old observation left the window
		t.Fatalf("window: P100 = %d ok=%v, want 120", minutes, ok)
	}
	if ok, _ := ta.MeasuredTransit(ctx, "N-A", "N-B", 100, 3, 28, now); ok {
		t.Fatal("three observations are only two inside the window")
	}
}

func TestTransitActualsPrunesStaleOpenRows(t *testing.T) {
	ta := NewStore().TransitActuals()
	ctx := context.Background()
	if err := ta.RecordLoadDeparted(ctx, "ld-stale", "N-A", tt0.AddDate(0, 0, -400)); err != nil {
		t.Fatal(err)
	}
	if err := ta.RecordLoadDeparted(ctx, "ld-live", "N-A", tt0); err != nil {
		t.Fatal(err)
	}
	// the stale open row was pruned by the later departure; the live one pairs
	if err := ta.RecordLoadArrived(ctx, "ld-stale", "N-B", tt0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ta.MeasuredTransit(ctx, "N-A", "N-B", 100, 1, 365, tt0); ok {
		t.Fatal("the stale open row must not pair")
	}
}
