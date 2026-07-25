// Package user reads the users_read_model table written by internal/projection/user.
package user

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("user: not found")

type User struct {
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

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (User, error) {
	var u User
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, is_archived FROM users_read_model WHERE id = $1`, id,
	).Scan(&u.ID, &u.Name, &u.IsArchived)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, fmt.Errorf("query/user: get %s: %w", id, err)
	}
	return u, nil
}
