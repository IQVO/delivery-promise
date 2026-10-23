package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/claudioed/delivery-promise/internal/adapters/outbound/postgres/pgtx"
)

// querier is the subset of pgx shared by *pgxpool.Pool and pgx.Tx that the
// repositories need, so the same SQL runs against either.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// UnitOfWork is the pgx-backed ports.UnitOfWork: Do begins one transaction,
// carries it in the ctx handed to fn (see package pgtx), commits on a nil
// return and rolls back otherwise. If ctx already carries a transaction Do
// joins it instead of beginning a nested one.
type UnitOfWork struct {
	pool *pgxpool.Pool
}

// NewUnitOfWork constructs a UnitOfWork over pool.
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork { return &UnitOfWork{pool: pool} }

// Do implements ports.UnitOfWork.
func (u *UnitOfWork) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := pgtx.From(ctx); ok {
		return fn(ctx)
	}
	tx, err := u.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin unit of work: %w", err)
	}
	// Rollback after a successful Commit is a harmless no-op (pgx.ErrTxClosed);
	// it also covers a panic in fn. A context that survives cancellation of ctx
	// makes a cancelled message still roll back.
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(pgtx.With(ctx, tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit unit of work: %w", err)
	}
	return nil
}

// db is the pool every repository shares; queryFor picks the open transaction.
type db struct{ pool *pgxpool.Pool }

// queryFor returns the transaction carried by ctx, or the pool when there is
// none.
func (d db) q(ctx context.Context) querier {
	if tx, ok := pgtx.From(ctx); ok {
		return tx
	}
	return d.pool
}

// BindTx returns a child context carrying tx, so the unit of work of a use case
// JOINS a transaction another component opened (the Idempotency-Key middleware)
// instead of beginning a second one. cmd/ hands it to the HTTP adapter: adapters
// never import each other.
func BindTx(ctx context.Context, tx pgx.Tx) context.Context { return pgtx.With(ctx, tx) }
