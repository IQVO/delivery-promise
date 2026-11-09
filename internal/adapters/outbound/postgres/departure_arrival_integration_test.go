//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// beforeDepartureArrival is the migration 0008 (ADR 0021) sits on top of at
// this change: 0006 and 0007 belong to sibling changes, and stepping down to
// 0005 crosses them when they are present.
const beforeDepartureArrival = 5

// TestDepartureArrivalMigrationGoesUpAndDown proves migration 0008 on a FRESH
// database: the table exists with its primary key, the adapter writes and
// prunes it, the step back drops it (the rows go with it) and the step up
// again restores it empty.
func TestDepartureArrivalMigrationGoesUpAndDown(t *testing.T) {
	ctx := context.Background()
	url := pgtest.EmptyURL(t)
	mustDo(t, "up", postgres.RunMigrations(url))
	pool := pgtest.OpenPool(t, url)
	count := func(sql string) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return n
	}
	var version int
	if err := pool.QueryRow(ctx, `SELECT version FROM schema_migrations`).Scan(&version); err != nil || version != latestMigration {
		t.Fatalf("schema version %d (%v), want %d", version, err, latestMigration)
	}
	arrivals := postgres.NewDepartureArrivals(pool)
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	mustDo(t, "record", arrivals.RecordDepartureArrived(ctx, "dep-1", at))
	mustDo(t, "record again", arrivals.RecordDepartureArrived(ctx, "dep-1", at.Add(time.Hour)))
	if n := count(`SELECT count(*) FROM rm_departure_arrival WHERE departure_id = 'dep-1' AND arrived_at = '2026-11-09T12:00:00Z'`); n != 1 {
		t.Fatalf("the first arrival must be kept once, got %d rows", n)
	}
	if n := count(`SELECT count(*) FROM pg_indexes WHERE tablename = 'rm_departure_arrival' AND indexname = 'rm_departure_arrival_arrived_at'`); n != 1 {
		t.Fatalf("the pruning index is missing")
	}

	mustDo(t, "down", postgres.MigrateTo(url, beforeDepartureArrival))
	if n := count(`SELECT count(*) FROM information_schema.tables WHERE table_name = 'rm_departure_arrival'`); n != 0 {
		t.Fatal("0008 down left the table behind")
	}
	mustDo(t, "up again", postgres.RunMigrations(url))
	if n := count(`SELECT count(*) FROM rm_departure_arrival`); n != 0 {
		t.Fatalf("the table must come back empty, got %d rows", n)
	}
}
