// Package universe reads the universes_read_model / universe_creators tables written by
// internal/projection/universe. No domain logic, no event replay — parameterized SQL only
// (plan §9).
package universe

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("universe: not found")

type Universe struct {
	ID         uuid.UUID
	Name       string
	IsArchived bool
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Universe, error) {
	var u Universe
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, is_archived FROM universes_read_model WHERE id = $1`, id,
	).Scan(&u.ID, &u.Name, &u.IsArchived)
	if errors.Is(err, pgx.ErrNoRows) {
		return Universe{}, ErrNotFound
	}
	if err != nil {
		return Universe{}, fmt.Errorf("query/universe: get %s: %w", id, err)
	}
	return u, nil
}

// ListCreators returns the Creator user ids for universe id. Returns ErrNotFound if the
// Universe itself doesn't exist (as opposed to existing with zero creators, which the
// write-side invariant makes impossible anyway).
func (r *Repository) ListCreators(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	if _, err := r.Get(ctx, id); err != nil {
		return nil, err
	}

	rows, err := r.pool.Query(ctx,
		`SELECT user_id FROM universe_creators WHERE universe_id = $1 ORDER BY user_id`, id,
	)
	if err != nil {
		return nil, fmt.Errorf("query/universe: list creators for %s: %w", id, err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var userID uuid.UUID
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("query/universe: scan creator row: %w", err)
		}
		ids = append(ids, userID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/universe: iterate creator rows: %w", err)
	}
	return ids, nil
}

// ListAll returns every non-archived Universe, ordered by name.
func (r *Repository) ListAll(ctx context.Context) ([]Universe, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, is_archived FROM universes_read_model WHERE is_archived = false ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("query/universe: list all: %w", err)
	}
	defer rows.Close()

	var universes []Universe
	for rows.Next() {
		var u Universe
		if err := rows.Scan(&u.ID, &u.Name, &u.IsArchived); err != nil {
			return nil, fmt.Errorf("query/universe: scan row: %w", err)
		}
		universes = append(universes, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/universe: iterate rows: %w", err)
	}
	return universes, nil
}
