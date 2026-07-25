// Package character is the Character read-model projector. Imports only
// domain/character/events — see projection/universe's doc comment for why. This is an
// independent projection/consumer from the Entity projector (internal/projection/entity),
// even though both are populated from the same CreateCharacter command — see plan §7's
// cross-projector ordering caveat for what that implies for callers joining across them.
package character

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/character/events"
)

const projectorName = "character-read-model"

type Projector struct{}

func NewProjector() *Projector { return &Projector{} }

func (p *Projector) Name() string { return projectorName }

func (p *Projector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *Projector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	switch env.EventType {
	case events.TypeCharacterCreated:
		var e events.CharacterCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return fmt.Errorf("character projector: unmarshal %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO characters_read_model (id, name, campaign_id, entity_id, player_user_id, is_archived, updated_at)
			 VALUES ($1, $2, $3, $4, $5, false, $6)
			 ON CONFLICT (id) DO NOTHING`,
			env.AggregateID, e.Name, e.CampaignID, e.EntityID, e.PlayerUserID, e.OccurredAt,
		)
		return err
	case events.TypeCharacterRenamed:
		var e events.CharacterRenamed
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return fmt.Errorf("character projector: unmarshal %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx, `UPDATE characters_read_model SET name = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, e.Name, e.OccurredAt)
		return err
	case events.TypePlayerChanged:
		var e events.PlayerChanged
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return fmt.Errorf("character projector: unmarshal %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx, `UPDATE characters_read_model SET player_user_id = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, e.NewPlayerUserID, e.OccurredAt)
		return err
	case events.TypeCharacterArchived:
		var e events.CharacterArchived
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return fmt.Errorf("character projector: unmarshal %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx, `UPDATE characters_read_model SET is_archived = true, updated_at = $2 WHERE id = $1`, env.AggregateID, e.OccurredAt)
		return err
	default:
		return nil
	}
}
