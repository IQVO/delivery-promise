//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// beforeTrackingMany is the migration 0009 (ADR 0025) sits on top of.
const beforeTrackingMany = 8

// TestTrackingManyAssignmentsMigrationGoesUpAndDown proves migration 0009 on a
// FRESH database: a tracking number is shared by several assignments, the step
// back keeps the lowest assignment id per number and restores the one-owner
// key, and the step up again accepts sharing.
func TestTrackingManyAssignmentsMigrationGoesUpAndDown(t *testing.T) {
	ctx := context.Background()
	url := pgtest.EmptyURL(t)
	mustDo(t, "up", postgres.RunMigrations(url))
	pool := pgtest.OpenPool(t, url)
	shipments := postgres.NewShipmentViews(pool)
	mustDo(t, "asg-b", shipments.PutShipment(ctx, "asg-b", []string{"TRK-S", "TRK-B"}))
	mustDo(t, "asg-a", shipments.PutShipment(ctx, "asg-a", []string{"TRK-S"}))
	mustDo(t, "asg-a again", shipments.PutShipment(ctx, "asg-a", []string{"TRK-S"}))
	owners, err := shipments.AssignmentsOf(ctx, "TRK-S")
	if err != nil || len(owners) != 2 || owners[0] != "asg-a" || owners[1] != "asg-b" {
		t.Fatalf("owners = %v (%v), want [asg-a asg-b]", owners, err)
	}

	mustDo(t, "down", postgres.MigrateTo(url, beforeTrackingMany))
	owners, err = shipments.AssignmentsOf(ctx, "TRK-S")
	if err != nil || len(owners) != 1 || owners[0] != "asg-a" {
		t.Fatalf("after down owners = %v (%v), want [asg-a]", owners, err)
	}
	if owners, err = shipments.AssignmentsOf(ctx, "TRK-B"); err != nil || len(owners) != 1 || owners[0] != "asg-b" {
		t.Fatalf("an unshared number keeps its owner, got %v (%v)", owners, err)
	}
	mustDo(t, "up again", postgres.RunMigrations(url))
	mustDo(t, "share again", shipments.PutShipment(ctx, "asg-c", []string{"TRK-S"}))
	if owners, err = shipments.AssignmentsOf(ctx, "TRK-S"); err != nil || len(owners) != 2 {
		t.Fatalf("after up owners = %v (%v), want two", owners, err)
	}
}
