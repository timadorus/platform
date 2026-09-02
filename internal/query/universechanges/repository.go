// Package universechanges reads the universe_changes_read_model table written by
// internal/projection/universechanges.
package universechanges

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Change struct {
	GlobalSeq     int64
	AggregateType string
	AggregateID   uuid.UUID
	EventType     string
	OccurredAt    time.Time
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Cursor returns the current max global_seq recorded for universeID, or 0 if there are none
// yet — a client calls this once, on first load, so its first real poll (since=<this value>)
// never returns the Universe's entire history.
func (r *Repository) Cursor(ctx context.Context, universeID uuid.UUID) (int64, error) {
	var cursor int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(global_seq), 0) FROM universe_changes_read_model WHERE universe_id = $1`,
		universeID,
	).Scan(&cursor); err != nil {
		return 0, fmt.Errorf("query/universechanges: cursor for universe %s: %w", universeID, err)
	}
	return cursor, nil
}

// List returns changes to universeID strictly after since, ordered by global_seq ascending,
// capped at 20 rows per call (matching the existing Entity-search convention) so a client that
// falls behind for a while doesn't get flooded in one response.
func (r *Repository) List(ctx context.Context, universeID uuid.UUID, since int64) ([]Change, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT global_seq, aggregate_type, aggregate_id, event_type, occurred_at
		 FROM universe_changes_read_model
		 WHERE universe_id = $1 AND global_seq > $2
		 ORDER BY global_seq ASC
		 LIMIT 20`,
		universeID, since,
	)
	if err != nil {
		return nil, fmt.Errorf("query/universechanges: list for universe %s since %d: %w", universeID, since, err)
	}
	defer rows.Close()

	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.GlobalSeq, &c.AggregateType, &c.AggregateID, &c.EventType, &c.OccurredAt); err != nil {
			return nil, fmt.Errorf("query/universechanges: scan row: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/universechanges: iterate rows: %w", err)
	}
	return out, nil
}
