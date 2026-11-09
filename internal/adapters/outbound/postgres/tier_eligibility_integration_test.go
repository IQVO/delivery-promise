//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// tierEligibilityMigration is the migration this file proves (ADR 0022). The
// step down goes to listingOwnerMigration, whatever sits between the two on a
// branch that has not merged its siblings yet.
const tierEligibilityMigration = 7

// TestTierEligibilityMigrationGoesUpDownUp proves migration 0007 on a FRESH
// database: the set climbs to the ceiling with the table and its partial index
// in place; the adapter writes and reads it; stepping down to the
// listing-owner migration drops both; stepping up creates them empty.
func TestTierEligibilityMigrationGoesUpDownUp(t *testing.T) {
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
	const tables = `SELECT count(*) FROM information_schema.tables WHERE table_name = 'rm_node_tier_eligibility'`
	const indexes = `SELECT count(*) FROM pg_indexes WHERE indexname = 'rm_node_tier_eligibility_ineligible'`
	if count(`SELECT version FROM schema_migrations`) < tierEligibilityMigration || count(tables) != 1 || count(indexes) != 1 {
		t.Fatal("migration 0007 did not create the read model")
	}

	r := postgres.NewNodeTierEligibility(pool)
	at := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	applied, err := r.Apply(ctx, readmodel.NodeTierEligibility{NodeID: "N-A", Tier: "SAME_DAY", Eligible: false, ChangedAt: at})
	mustDo(t, "apply", err)
	if !applied {
		t.Fatal("the first fact applies")
	}
	rows, err := r.Ineligible(ctx)
	mustDo(t, "ineligible", err)
	if len(rows) != 1 || rows[0].NodeID != "N-A" || !rows[0].ChangedAt.Equal(at) {
		t.Fatalf("rows = %+v", rows)
	}

	mustDo(t, "down to the listing-owner migration", postgres.MigrateTo(url, listingOwnerMigration))
	if count(tables) != 0 || count(indexes) != 0 {
		t.Fatal("0007 down left the read model behind")
	}
	mustDo(t, "up again", postgres.RunMigrations(url))
	if count(tables) != 1 || count(`SELECT count(*) FROM rm_node_tier_eligibility`) != 0 {
		t.Fatal("0007 up again must create an empty table")
	}
}
