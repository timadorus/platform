package universechanges

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/entity/events"
)

const EntityProjectorName = "universe-changes-entity"

type EntityProjector struct{}

func NewEntityProjector() *EntityProjector { return &EntityProjector{} }

func (p *EntityProjector) Name() string { return EntityProjectorName }

func (p *EntityProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *EntityProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := p.resolveUniverseID(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}

// resolveUniverseID mirrors CampaignProjector.resolveUniverseID's exact shape — see that
// method's doc comment for the full reasoning.
func (p *EntityProjector) resolveUniverseID(ctx context.Context, tx pgx.Tx, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == events.TypeEntityCreated {
		var e events.EntityCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("universechanges: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}

	var universeID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT universe_id FROM entities_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("universechanges: resolve universe for entity %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}
