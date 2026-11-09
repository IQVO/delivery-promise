//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

var it0 = time.Date(2026, 10, 1, 6, 0, 0, 0, time.UTC)

// transitLoad appends one observed transit of minutes to the (origin, dest)
// pair through the two consumer events.
func transitLoad(t *testing.T, ta *postgres.TransitActuals, loadID, origin, dest string, minutes int, at time.Time) {
	t.Helper()
	if err := ta.RecordLoadDeparted(context.Background(), loadID, origin, at); err != nil {
		t.Fatal(err)
	}
	if err := ta.RecordLoadArrived(context.Background(), loadID, dest, at.Add(time.Duration(minutes)*time.Minute)); err != nil {
		t.Fatal(err)
	}
}

// TestTransitActualsMigrationAndPairing runs the measured-transit read model
// over the real Postgres: the 0004 migration is applied by pgtest's template.
func TestTransitActualsMigrationAndPairing(t *testing.T) {
	pool := pgtest.NewPool(t)
	ta := postgres.NewTransitActuals(pool)
	ctx := context.Background()

	// twenty observed transits of 60..79 minutes pair into the ring
	for i := range 20 {
		transitLoad(t, ta, "ld-it-"+string(rune('a'+i)), "N-A", "N-B", 60+i, it0.Add(time.Duration(i)*time.Hour))
	}
	ok, minutes := ta.MeasuredTransit(ctx, "N-A", "N-B", 90, 20, 28, it0.Add(24*time.Hour))
	if !ok || minutes != 77 { // nearest rank ceil(0.9*20) = 18th of 60..79
		t.Fatalf("P90 = %d ok=%v, want 77", minutes, ok)
	}
	if ok, _ := ta.MeasuredTransit(ctx, "N-A", "N-B", 90, 21, 28, it0.Add(24*time.Hour)); ok {
		t.Fatal("below minObservations the pair is not measured")
	}

	// an arrival whose departure was never seen changes nothing
	if err := ta.RecordLoadArrived(ctx, "ld-never-departed", "N-B", it0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if ok, _ := ta.MeasuredTransit(ctx, "N-A", "N-B", 100, 21, 28, it0.Add(24*time.Hour)); ok {
		t.Fatal("an unpaired arrival must not add an observation")
	}

	// the ring caps at the latest 200 observations
	for i := 100; i < 320; i++ {
		transitLoad(t, ta, "ld-cap-"+string(rune('a'+i%26))+string(rune('a'+i/26)), "N-C", "N-D", i, it0.Add(time.Duration(i)*time.Minute))
	}
	ok, minutes = ta.MeasuredTransit(ctx, "N-C", "N-D", 1, 1, 28, it0.Add(10*time.Hour))
	if !ok || minutes != 121 { // the ring holds 120..319; rank ceil(0.01*200) = 2nd smallest
		t.Fatalf("ring cap: P1 = %d ok=%v, want 121", minutes, ok)
	}

	// observations older than the window stop counting
	now := time.Date(2026, 11, 5, 6, 0, 0, 0, time.UTC)
	transitLoad(t, ta, "ld-old", "N-E", "N-F", 500, now.AddDate(0, 0, -40))
	transitLoad(t, ta, "ld-new-1", "N-E", "N-F", 100, now.AddDate(0, 0, -2))
	transitLoad(t, ta, "ld-new-2", "N-E", "N-F", 300, now.AddDate(0, 0, -1))
	ok, minutes = ta.MeasuredTransit(ctx, "N-E", "N-F", 100, 2, 28, now)
	if !ok || minutes != 300 { // the two in-window observations, not the 500 of 40 days ago
		t.Fatalf("window: P100 = %d ok=%v, want 300", minutes, ok)
	}
	if ok, _ := ta.MeasuredTransit(ctx, "N-E", "N-F", 100, 3, 28, now); ok {
		t.Fatal("the 40-day-old observation left the window")
	}
}
