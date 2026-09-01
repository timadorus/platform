// Package rulesettables reads the ruleset_tables_read_model table written by
// internal/engine/timadorus/tables at cmd/timadorus-engine startup.
package rulesettables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("rulesettables: not found")

type Row struct {
	Key  string
	Data json.RawMessage
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// List returns every row of rulesetID's tableName table, ordered by row key.
func (r *Repository) List(ctx context.Context, rulesetID uuid.UUID, tableName string) ([]Row, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT row_key, data FROM ruleset_tables_read_model
		 WHERE ruleset_id = $1 AND table_name = $2
		 ORDER BY row_key`,
		rulesetID, tableName,
	)
	if err != nil {
		return nil, fmt.Errorf("query/rulesettables: list %s for ruleset %s: %w", tableName, rulesetID, err)
	}
	defer rows.Close()

	var out []Row
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.Key, &row.Data); err != nil {
			return nil, fmt.Errorf("query/rulesettables: scan row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/rulesettables: iterate rows: %w", err)
	}
	return out, nil
}

// Get returns one row's data, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, rulesetID uuid.UUID, tableName, rowKey string) (json.RawMessage, error) {
	var data json.RawMessage
	err := r.pool.QueryRow(ctx,
		`SELECT data FROM ruleset_tables_read_model
		 WHERE ruleset_id = $1 AND table_name = $2 AND row_key = $3`,
		rulesetID, tableName, rowKey,
	).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query/rulesettables: get %s/%s for ruleset %s: %w", tableName, rowKey, rulesetID, err)
	}
	return data, nil
}
