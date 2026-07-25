// Package object reads the objects_read_model table written by internal/projection/object.
package object

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("object: not found")

type Object struct {
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

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Object, error) {
	var o Object
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, universe_id, is_archived FROM objects_read_model WHERE id = $1`, id,
	).Scan(&o.ID, &o.Name, &o.UniverseID, &o.IsArchived)
	if errors.Is(err, pgx.ErrNoRows) {
		return Object{}, ErrNotFound
	}
	if err != nil {
		return Object{}, fmt.Errorf("query/object: get %s: %w", id, err)
	}
	return o, nil
}

// ListByUniverse returns non-archived Objects under universeID, ordered by name. Does not
// itself verify universeID exists — plan §9's query-api never touches domain/eventsourcing,
// so an unknown universeID simply yields an empty list rather than a 404 (see plan §7's
// cross-projection lag caveat for why this projection can't assert Universe existence
// authoritatively anyway).
func (r *Repository) ListByUniverse(ctx context.Context, universeID uuid.UUID) ([]Object, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, universe_id, is_archived FROM objects_read_model
		 WHERE universe_id = $1 AND is_archived = false
		 ORDER BY name`,
		universeID,
	)
	if err != nil {
		return nil, fmt.Errorf("query/object: list by universe %s: %w", universeID, err)
	}
	defer rows.Close()

	var objects []Object
	for rows.Next() {
		var o Object
		if err := rows.Scan(&o.ID, &o.Name, &o.UniverseID, &o.IsArchived); err != nil {
			return nil, fmt.Errorf("query/object: scan row: %w", err)
		}
		objects = append(objects, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/object: iterate rows: %w", err)
	}
	return objects, nil
}
