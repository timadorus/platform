package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type txKey struct{}

// WithTx stashes tx in ctx so that Store.Append, when called with the returned context,
// joins the caller's transaction instead of opening its own. Used by UnitOfWork to make a
// single Postgres transaction span multiple aggregates' Append calls (see unit_of_work.go).
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

func txFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}
