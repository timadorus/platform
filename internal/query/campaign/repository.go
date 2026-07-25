// Package campaign reads the campaigns_read_model / campaign_gamemasters tables written by
// internal/projection/campaign.
package campaign

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("campaign: not found")

type Campaign struct {
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

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Campaign, error) {
	var c Campaign
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, universe_id, is_archived FROM campaigns_read_model WHERE id = $1`, id,
	).Scan(&c.ID, &c.Name, &c.UniverseID, &c.IsArchived)
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	if err != nil {
		return Campaign{}, fmt.Errorf("query/campaign: get %s: %w", id, err)
	}
	return c, nil
}

func (r *Repository) ListGamemasters(ctx context.Context, id uuid.UUID) ([]uuid.UUID, error) {
	if _, err := r.Get(ctx, id); err != nil {
		return nil, err
	}

	rows, err := r.pool.Query(ctx,
		`SELECT user_id FROM campaign_gamemasters WHERE campaign_id = $1 ORDER BY user_id`, id,
	)
	if err != nil {
		return nil, fmt.Errorf("query/campaign: list gamemasters for %s: %w", id, err)
	}
	defer rows.Close()

	var ids []uuid.UUID
	for rows.Next() {
		var userID uuid.UUID
		if err := rows.Scan(&userID); err != nil {
			return nil, fmt.Errorf("query/campaign: scan gamemaster row: %w", err)
		}
		ids = append(ids, userID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/campaign: iterate gamemaster rows: %w", err)
	}
	return ids, nil
}

// ListByUniverse returns non-archived Campaigns under universeID, ordered by name.
func (r *Repository) ListByUniverse(ctx context.Context, universeID uuid.UUID) ([]Campaign, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, universe_id, is_archived FROM campaigns_read_model
		 WHERE universe_id = $1 AND is_archived = false
		 ORDER BY name`,
		universeID,
	)
	if err != nil {
		return nil, fmt.Errorf("query/campaign: list by universe %s: %w", universeID, err)
	}
	defer rows.Close()

	var campaigns []Campaign
	for rows.Next() {
		var c Campaign
		if err := rows.Scan(&c.ID, &c.Name, &c.UniverseID, &c.IsArchived); err != nil {
			return nil, fmt.Errorf("query/campaign: scan row: %w", err)
		}
		campaigns = append(campaigns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/campaign: iterate rows: %w", err)
	}
	return campaigns, nil
}
