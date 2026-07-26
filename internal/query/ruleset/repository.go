// Package ruleset reads the rulesets_read_model table written by internal/projection/ruleset.
package ruleset

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("ruleset: not found")

type Ruleset struct {
	ID          uuid.UUID
	Name        string
	Description string
	References  []string
	IsArchived  bool
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Ruleset, error) {
	var out Ruleset
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, description, reference_urls, is_archived FROM rulesets_read_model WHERE id = $1`, id,
	).Scan(&out.ID, &out.Name, &out.Description, &out.References, &out.IsArchived)
	if errors.Is(err, pgx.ErrNoRows) {
		return Ruleset{}, ErrNotFound
	}
	if err != nil {
		return Ruleset{}, fmt.Errorf("query/ruleset: get %s: %w", id, err)
	}
	return out, nil
}

// ListAll returns every non-archived Ruleset, ordered by name. Ruleset has no parent to scope
// by (plan §2), matching User/Universe's own list-all shape.
func (r *Repository) ListAll(ctx context.Context) ([]Ruleset, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, description, reference_urls, is_archived FROM rulesets_read_model
		 WHERE is_archived = false
		 ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("query/ruleset: list all: %w", err)
	}
	defer rows.Close()

	var rulesets []Ruleset
	for rows.Next() {
		var out Ruleset
		if err := rows.Scan(&out.ID, &out.Name, &out.Description, &out.References, &out.IsArchived); err != nil {
			return nil, fmt.Errorf("query/ruleset: scan row: %w", err)
		}
		rulesets = append(rulesets, out)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/ruleset: iterate rows: %w", err)
	}
	return rulesets, nil
}
