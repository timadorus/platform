// Package object is the Object read-model projector. Imports only domain/object/events — see
// projection/universe's doc comment for why.
package object

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/object/events"
)

const projectorName = "object-read-model"

type Projector struct{}

func NewProjector() *Projector { return &Projector{} }

func (p *Projector) Name() string { return projectorName }

func (p *Projector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *Projector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	switch env.EventType {
	case events.TypeObjectCreated:
		var e events.ObjectCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return fmt.Errorf("object projector: unmarshal %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO objects_read_model (id, name, universe_id, is_archived, updated_at)
			 VALUES ($1, $2, $3, false, $4)
			 ON CONFLICT (id) DO NOTHING`,
			env.AggregateID, e.Name, e.UniverseID, e.OccurredAt,
		)
		return err
	case events.TypeObjectRenamed:
		var e events.ObjectRenamed
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return fmt.Errorf("object projector: unmarshal %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx, `UPDATE objects_read_model SET name = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, e.Name, e.OccurredAt)
		return err
	case events.TypeObjectArchived:
		var e events.ObjectArchived
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return fmt.Errorf("object projector: unmarshal %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx, `UPDATE objects_read_model SET is_archived = true, updated_at = $2 WHERE id = $1`, env.AggregateID, e.OccurredAt)
		return err
	default:
		return nil
	}
}
