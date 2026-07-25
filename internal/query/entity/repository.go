// Package entity reads the entities_read_model table written by internal/projection/entity.
package entity

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("entity: not found")

type Entity struct {
	ID         uuid.UUID
	Name       string
	UniverseID uuid.UUID
	IsArchived bool
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Entity, error) {
	var e Entity
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, universe_id, is_archived FROM entities_read_model WHERE id = $1`, id,
	).Scan(&e.ID, &e.Name, &e.UniverseID, &e.IsArchived)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entity{}, ErrNotFound
	}
	if err != nil {
		return Entity{}, fmt.Errorf("query/entity: get %s: %w", id, err)
	}
	return e, nil
}

// ListByUniverse returns non-archived Entities under universeID, ordered by name.
func (r *Repository) ListByUniverse(ctx context.Context, universeID uuid.UUID) ([]Entity, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, universe_id, is_archived FROM entities_read_model
		 WHERE universe_id = $1 AND is_archived = false
		 ORDER BY name`,
		universeID,
	)
	if err != nil {
		return nil, fmt.Errorf("query/entity: list by universe %s: %w", universeID, err)
	}
	defer rows.Close()

	var entities []Entity
	for rows.Next() {
		var e Entity
		if err := rows.Scan(&e.ID, &e.Name, &e.UniverseID, &e.IsArchived); err != nil {
			return nil, fmt.Errorf("query/entity: scan row: %w", err)
		}
		entities = append(entities, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/entity: iterate rows: %w", err)
	}
	return entities, nil
}
