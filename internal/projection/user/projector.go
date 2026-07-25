// Package user is the User read-model projector. Imports only domain/user/events — see
// projection/universe's doc comment for why.
package user

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/user/events"
)

const projectorName = "user-read-model"

type Projector struct{}

func NewProjector() *Projector { return &Projector{} }

func (p *Projector) Name() string { return projectorName }

func (p *Projector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *Projector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	switch env.EventType {
	case events.TypeUserCreated:
		var e events.UserCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return fmt.Errorf("user projector: unmarshal %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx,
			`INSERT INTO users_read_model (id, name, is_archived, updated_at) VALUES ($1, $2, false, $3) ON CONFLICT (id) DO NOTHING`,
			env.AggregateID, e.Name, e.OccurredAt,
		)
		return err
	case events.TypeUserRenamed:
		var e events.UserRenamed
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return fmt.Errorf("user projector: unmarshal %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx, `UPDATE users_read_model SET name = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, e.Name, e.OccurredAt)
		return err
	case events.TypeUserArchived:
		var e events.UserArchived
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return fmt.Errorf("user projector: unmarshal %s: %w", env.EventType, err)
		}
		_, err := tx.Exec(ctx, `UPDATE users_read_model SET is_archived = true, updated_at = $2 WHERE id = $1`, env.AggregateID, e.OccurredAt)
		return err
	default:
		return nil
	}
}
