//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/application/readmodel"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// latestMigration is the ceiling of the embedded set: bump it with every new
// migration so the step count below stays honest. ownerMigration is the owner
// key migration (0005) this file proves: the steps down go to the one before it.
const (
	latestMigration = 6
	ownerMigration  = 5
)

// TestListingOwnerMigrationBackfillsOperatorOwned proves migration 0005 (ADR
// 0017) on a FRESH database: the set climbs to latestMigration; a listing row
// written under 0004 gains owner_ref "" (operator-owned, never NULL) in the
// column and in its data; the key index replaces the (node, item) one; the
// step back to 0004 drops the column and the step up again restores it.
func TestListingOwnerMigrationBackfillsOperatorOwned(t *testing.T) {
	ctx := context.Background()
	url := pgtest.EmptyURL(t)
	mustDo(t, "up", postgres.RunMigrations(url))
	pool := pgtest.OpenPool(t, url)
	scalar := func(sql string, dst any) {
		t.Helper()
		if err := pool.QueryRow(ctx, sql).Scan(dst); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	var version int
	scalar(`SELECT version FROM schema_migrations`, &version)
	if version != latestMigration {
		t.Fatalf("schema version %d, want %d", version, latestMigration)
	}

	mustDo(t, "one step down to 0004", postgres.MigrateTo(url, ownerMigration-1))
	if _, err := pool.Exec(ctx, `INSERT INTO rm_listings (id, item_id, node_id, data)
		VALUES ('lst-legacy', 'ci-1001', 'N-AUS3', '{"ID":"lst-legacy","ItemID":"ci-1001","NodeID":"N-AUS3","AvailableQty":7}')`); err != nil {
		t.Fatalf("seed a 0004 row: %v", err)
	}
	mustDo(t, "up to 0005", postgres.RunMigrations(url))

	var owner, dataOwner string
	scalar(`SELECT owner_ref FROM rm_listings WHERE id = 'lst-legacy'`, &owner)
	scalar(`SELECT data->>'OwnerRef' FROM rm_listings WHERE id = 'lst-legacy'`, &dataOwner)
	if owner != "" || dataOwner != "" {
		t.Fatalf("backfill: column %q, data %q, want ''", owner, dataOwner)
	}
	var keyIndex, oldIndex int
	scalar(`SELECT count(*) FROM pg_indexes WHERE indexname = 'rm_listings_node_item_owner'`, &keyIndex)
	scalar(`SELECT count(*) FROM pg_indexes WHERE indexname = 'rm_listings_node_item'`, &oldIndex)
	if keyIndex != 1 || oldIndex != 0 {
		t.Fatalf("indexes: key %d, old %d", keyIndex, oldIndex)
	}

	views := postgres.NewNetworkViews(pool)
	l, err := views.GetListing(ctx, "lst-legacy")
	mustDo(t, "get legacy", err)
	if l.OwnerRef != "" || l.AvailableQty != 7 {
		t.Fatalf("legacy row reads %+v", l)
	}
	mustDo(t, "put seller", views.PutListing(ctx, readmodel.Listing{ID: "lst-s", ItemID: "ci-1001", NodeID: "N-AUS3", OwnerRef: "S-0042", AvailableQty: 3}))
	scalar(`SELECT owner_ref FROM rm_listings WHERE id = 'lst-s'`, &owner)
	if owner != "S-0042" {
		t.Fatalf("seller column %q", owner)
	}

	mustDo(t, "down to 0004", postgres.MigrateTo(url, ownerMigration-1))
	var columns int
	scalar(`SELECT count(*) FROM information_schema.columns WHERE table_name = 'rm_listings' AND column_name = 'owner_ref'`, &columns)
	if columns != 0 {
		t.Fatal("0005 down left owner_ref behind")
	}
	mustDo(t, "up again", postgres.RunMigrations(url))
	scalar(`SELECT owner_ref FROM rm_listings WHERE id = 'lst-s'`, &owner)
	if owner != "S-0042" {
		t.Fatalf("after down/up the column is backfilled from the row's data, got %q", owner)
	}
	scalar(`SELECT owner_ref FROM rm_listings WHERE id = 'lst-legacy'`, &owner)
	if owner != "" {
		t.Fatalf("after down/up the legacy row stays operator-owned, got %q", owner)
	}
}
