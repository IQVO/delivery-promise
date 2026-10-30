//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/repocontract"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/domain/promise"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// TestWindowTimeZoneMigrationBackfillsLegacyRows proves migration 0002 on a
// FRESH database: up/down/up of the whole set, then rows written under 0001
// (windows without a zone) gain the delivery station's zone when 0002 runs,
// keep it when 0002 is rolled back, and are left alone when re-run (ADR 0011).
func TestWindowTimeZoneMigrationBackfillsLegacyRows(t *testing.T) {
	ctx := context.Background()
	url := pgtest.EmptyURL(t)
	mustDo(t, "up", postgres.RunMigrations(url))
	mustDo(t, "down", postgres.RevertMigrations(url))
	mustDo(t, "up again", postgres.RunMigrations(url))
	mustDo(t, "back to 0001", postgres.MigrateTo(url, 1))

	pool := pgtest.OpenPool(t, url)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	// The read models: 787 is served by a station with a real zone, 999 by one
	// whose zone no database knows, 555 has no service area at all.
	exec(`INSERT INTO rm_nodes (id, data) VALUES ('N-DAU1', '{"ID":"N-DAU1","Type":"DELIVERY_STATION","Timezone":"America/Chicago"}'),
		('N-BAD', '{"ID":"N-BAD","Type":"DELIVERY_STATION","Timezone":"Mars/Olympus"}')`)
	exec(`INSERT INTO rm_areas (postal_prefix, data) VALUES ('787', '{"PostalPrefix":"787","StationID":"N-DAU1"}'),
		('999', '{"PostalPrefix":"999","StationID":"N-BAD"}')`)

	offers, promises := postgres.NewOfferRepository(pool), postgres.NewPromiseRepository(pool)
	for _, row := range []struct{ id, prefix, zone string }{
		{"off-legacy", "787", ""}, {"off-unknown-zone", "999", ""}, {"off-no-area", "555", ""}, {"off-zoned", "787", "America/Denver"},
	} {
		s := repocontract.NewOffer(t, row.id).Snapshot()
		s.DestinationPrefix, s.Window.TimeZone = row.prefix, row.zone
		o, err := offer.Restore(s)
		mustDo(t, "restore "+row.id, err)
		mustDo(t, "save "+row.id, offers.Save(ctx, o))
	}
	legacy := repocontract.NewPromise(t, "prm-legacy", "off-legacy", "N-AUS3", "dep-1800").Snapshot()
	legacy.Terms.Window.TimeZone = ""
	p, err := promise.Restore(legacy)
	mustDo(t, "restore promise", err)
	mustDo(t, "save promise", promises.Save(ctx, p))

	want := map[offer.ID]string{"off-legacy": "America/Chicago", "off-unknown-zone": "", "off-no-area": "", "off-zoned": "America/Denver"}
	check := func(stage string) {
		t.Helper()
		for id, zone := range want {
			o, err := offers.Get(ctx, id)
			mustDo(t, stage+" get "+string(id), err)
			if got := o.Window().TimeZone; got != zone {
				t.Errorf("%s: offer %s zone %q, want %q", stage, id, got, zone)
			}
		}
		got, err := promises.Get(ctx, "prm-legacy")
		mustDo(t, stage+" get promise", err)
		if z := got.Terms().Window.TimeZone; z != "America/Chicago" {
			t.Errorf("%s: promise zone %q, want America/Chicago", stage, z)
		}
	}
	mustDo(t, "up to 0002", postgres.RunMigrations(url))
	check("after up")
	mustDo(t, "down to 0001", postgres.MigrateTo(url, 1))
	check("after down (the zones are kept)")
	mustDo(t, "up again", postgres.RunMigrations(url))
	check("after up again")
}

func mustDo(t *testing.T, what string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}
