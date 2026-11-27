//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres"
	"github.com/claudioed/delivery-promise/internal/adapters/outbound/repocontract"
	"github.com/claudioed/delivery-promise/internal/application/ports"
	"github.com/claudioed/delivery-promise/internal/domain/offer"
	"github.com/claudioed/delivery-promise/internal/testing/pgtest"
)

// ADR 0031 left one known limit to this follow-up: a unique violation on a
// version-1 INSERT (save in repositories.go, Policies.Save) aborts the
// savepoint the Idempotency-Key middleware bound into the request context
// (runFreshRequest in idempotency.go). The use case's own retry of
// ports.ErrConcurrentUpdate (Service.tx in usecases/service.go, MaxAttempts)
// then re-executed on the aborted transaction, every statement failed with
// 25P02 and the request answered 500, an answer the middleware never stores.
// The retry must re-execute on a rolled-back savepoint: the racing create
// loses the first attempt, the second attempt reads what the winner
// committed and goes on.
func TestAJoinedRetryReExecutesAfterAVersion1InsertConflict(t *testing.T) {
	pool := pgtest.NewPool(t)
	ctx := context.Background()
	offers := postgres.NewOfferRepository(pool)
	uow := postgres.NewUnitOfWork(pool)

	// The committed winner of the race: the version-1 INSERT below collides
	// with this row.
	if err := offers.Save(ctx, repocontract.NewOffer(t, "off-race")); err != nil {
		t.Fatalf("save the racing winner: %v", err)
	}

	// What RequireIdempotencyKey does (runFreshRequest): the request
	// transaction, its savepoint bound into the handler's context.
	reqTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the request transaction: %v", err)
	}
	defer func() { _ = reqTx.Rollback(ctx) }()
	sp, err := reqTx.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the request savepoint: %v", err)
	}
	bound := postgres.BindTx(ctx, sp)

	// What Service.tx does: retry the unit of work while it loses the
	// optimistic race.
	var attempts int
	for attempt := 0; attempt < 3; attempt++ {
		err = uow.Do(bound, func(ctx context.Context) error {
			attempts++
			if attempts == 1 {
				// The losing create: a version-1 INSERT whose key the
				// winner already committed.
				return offers.Save(ctx, repocontract.NewOffer(t, "off-race"))
			}
			// The retry re-executes: it reads what the winner committed and
			// passes. On an aborted transaction this read fails with 25P02.
			_, err := offers.Get(ctx, offer.ID("off-race"))
			return err
		})
		if !errors.Is(err, ports.ErrConcurrentUpdate) {
			break
		}
	}
	if err != nil {
		t.Fatalf("the retry did not re-execute cleanly: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("the unit ran %d times, want 2 (the losing insert, then the re-execution)", attempts)
	}
}
