// Package checkpoint tracks, per projection, the last event global_seq successfully
// applied — read and written in the same Postgres transaction as the projection's own
// read-model writes, so a crash between commit and Ack (see internal/projection.Router) can
// never leave a projection either skipping or double-applying an event.
package checkpoint

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// Get returns the last processed global_seq for projectionName, initializing it to 0 (and
// locking the row) if this is the first time this projection has run. Must be called inside
// the same transaction that Set and the projection's read-model writes use.
func Get(ctx context.Context, tx pgx.Tx, projectionName string) (int64, error) {
	var lastSeq int64
	err := tx.QueryRow(ctx,
		`SELECT last_global_seq FROM projection_checkpoints WHERE projection_name = $1 FOR UPDATE`,
		projectionName,
	).Scan(&lastSeq)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, err := tx.Exec(ctx,
			`INSERT INTO projection_checkpoints (projection_name, last_global_seq) VALUES ($1, 0)
			 ON CONFLICT (projection_name) DO NOTHING`,
			projectionName,
		); err != nil {
			return 0, fmt.Errorf("checkpoint: initialize %q: %w", projectionName, err)
		}
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("checkpoint: get %q: %w", projectionName, err)
	}
	return lastSeq, nil
}

// Set records globalSeq as the last event successfully applied by projectionName.
func Set(ctx context.Context, tx pgx.Tx, projectionName string, globalSeq int64) error {
	if _, err := tx.Exec(ctx,
		`UPDATE projection_checkpoints SET last_global_seq = $2, updated_at = now() WHERE projection_name = $1`,
		projectionName, globalSeq,
	); err != nil {
		return fmt.Errorf("checkpoint: set %q: %w", projectionName, err)
	}
	return nil
}
