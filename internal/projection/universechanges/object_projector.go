package universechanges

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/object/events"
)

const objectProjectorName = "universe-changes-object"

type ObjectProjector struct{}

func NewObjectProjector() *ObjectProjector { return &ObjectProjector{} }

func (p *ObjectProjector) Name() string { return objectProjectorName }

func (p *ObjectProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *ObjectProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := p.resolveUniverseID(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}

// resolveUniverseID mirrors CampaignProjector.resolveUniverseID's exact shape — see that
// method's doc comment for the full reasoning.
func (p *ObjectProjector) resolveUniverseID(ctx context.Context, tx pgx.Tx, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == events.TypeObjectCreated {
		var e events.ObjectCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("universechanges: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}

	var universeID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT universe_id FROM objects_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("universechanges: resolve universe for object %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}
