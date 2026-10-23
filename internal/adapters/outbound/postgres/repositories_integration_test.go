//go:build integration

package postgres_test

import (
	"context"
	"testing"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/kafka"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/repocontract"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// fixture wires one fresh database (migrations applied) into the contract suite.
func fixture(t *testing.T) repocontract.Fixture {
	t.Helper()
	pool := pgtest.NewPool(t)
	ob := postgres.NewOutbox(pool, kafka.NewEncoder())
	return repocontract.Fixture{
		UoW: postgres.NewUnitOfWork(pool), Offers: postgres.NewOfferRepository(pool), Promises: postgres.NewPromiseRepository(pool),
		Books: postgres.NewBookRepository(pool), Policies: postgres.NewPolicyRepository(pool), Network: postgres.NewNetworkViews(pool),
		Shipments: postgres.NewShipmentViews(pool), Outbox: ob, Inbox: postgres.NewInbox(pool),
		OutboxCount: func() int {
			n, err := ob.Count(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			return n
		},
	}
}

func TestPostgresSatisfiesTheRepositoryContract(t *testing.T) {
	repocontract.Run(t, fixture)
}

func TestMigrationsGoUpDownUp(t *testing.T) {
	url := pgtest.URL(t) // already migrated once (twice in the template)
	if err := postgres.RevertMigrations(url); err != nil {
		t.Fatalf("down: %v", err)
	}
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("up again: %v", err)
	}
	if err := postgres.RunMigrations(url); err != nil {
		t.Fatalf("up is idempotent: %v", err)
	}
}
