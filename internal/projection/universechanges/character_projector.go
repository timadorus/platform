package universechanges

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/character/events"
)

const CharacterProjectorName = "universe-changes-character"

type CharacterProjector struct{}

func NewCharacterProjector() *CharacterProjector { return &CharacterProjector{} }

func (p *CharacterProjector) Name() string { return CharacterProjectorName }

func (p *CharacterProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *CharacterProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := p.resolveUniverseID(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}

// resolveUniverseID needs one more hop than its siblings: CharacterCreated carries CampaignID,
// not UniverseID directly, so even the create-time path needs a read-model query (against
// campaigns_read_model, keyed by the payload's own CampaignID) — Character is the only one of
// the five aggregate types where even Created isn't fully free. Every other Character event
// resolves via a single joined query against both characters_read_model and
// campaigns_read_model, rather than two separate round trips.
func (p *CharacterProjector) resolveUniverseID(ctx context.Context, tx pgx.Tx, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == events.TypeCharacterCreated {
		var e events.CharacterCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("universechanges: unmarshal %s: %w", env.EventType, err)
		}
		var universeID uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT universe_id FROM campaigns_read_model WHERE id = $1`, e.CampaignID,
		).Scan(&universeID); err != nil {
			return uuid.Nil, fmt.Errorf("universechanges: resolve universe for campaign %s (character %s create): %w", e.CampaignID, env.AggregateID, err)
		}
		return universeID, nil
	}

	var universeID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT c.universe_id FROM characters_read_model ch
		 JOIN campaigns_read_model c ON c.id = ch.campaign_id
		 WHERE ch.id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("universechanges: resolve universe for character %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}
