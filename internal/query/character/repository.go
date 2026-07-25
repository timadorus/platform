// Package character reads the characters_read_model table written by
// internal/projection/character.
package character

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("character: not found")

type Character struct {
	ID           uuid.UUID
	Name         string
	CampaignID   uuid.UUID
	EntityID     uuid.UUID
	PlayerUserID uuid.UUID
	IsArchived   bool
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Character, error) {
	var c Character
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, campaign_id, entity_id, player_user_id, is_archived FROM characters_read_model WHERE id = $1`, id,
	).Scan(&c.ID, &c.Name, &c.CampaignID, &c.EntityID, &c.PlayerUserID, &c.IsArchived)
	if errors.Is(err, pgx.ErrNoRows) {
		return Character{}, ErrNotFound
	}
	if err != nil {
		return Character{}, fmt.Errorf("query/character: get %s: %w", id, err)
	}
	return c, nil
}

// ListByCampaign returns non-archived Characters under campaignID, ordered by name.
func (r *Repository) ListByCampaign(ctx context.Context, campaignID uuid.UUID) ([]Character, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, campaign_id, entity_id, player_user_id, is_archived FROM characters_read_model
		 WHERE campaign_id = $1 AND is_archived = false
		 ORDER BY name`,
		campaignID,
	)
	if err != nil {
		return nil, fmt.Errorf("query/character: list by campaign %s: %w", campaignID, err)
	}
	defer rows.Close()

	var characters []Character
	for rows.Next() {
		var c Character
		if err := rows.Scan(&c.ID, &c.Name, &c.CampaignID, &c.EntityID, &c.PlayerUserID, &c.IsArchived); err != nil {
			return nil, fmt.Errorf("query/character: scan row: %w", err)
		}
		characters = append(characters, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/character: iterate rows: %w", err)
	}
	return characters, nil
}
