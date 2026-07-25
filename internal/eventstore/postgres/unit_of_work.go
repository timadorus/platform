package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UnitOfWork lets an application-layer command service make more than one aggregate's
// Repository.Save calls commit atomically (e.g. creating a Character and its auto-created
// Entity together, internal/command/character/service.go). It is deliberately postgres-
// specific and opt-in: single-aggregate command services never construct one, and
// Store.Append behaves identically whether or not an ambient transaction is present.
type UnitOfWork struct {
	tx pgx.Tx
}

// NewUnitOfWork begins a transaction and returns a context carrying it, so that any
// Repository.Save call made with the returned context joins this transaction instead of
// opening its own (see tx.go).
func NewUnitOfWork(ctx context.Context, pool *pgxpool.Pool) (*UnitOfWork, context.Context, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return nil, ctx, fmt.Errorf("postgres: begin unit of work: %w", err)
	}
	return &UnitOfWork{tx: tx}, WithTx(ctx, tx), nil
}

func (u *UnitOfWork) Commit(ctx context.Context) error {
	if err := u.tx.Commit(ctx); err != nil {
		return fmt.Errorf("postgres: commit unit of work: %w", err)
	}
	return nil
}

func (u *UnitOfWork) Rollback(ctx context.Context) error {
	if err := u.tx.Rollback(ctx); err != nil {
		return fmt.Errorf("postgres: rollback unit of work: %w", err)
	}
	return nil
}
