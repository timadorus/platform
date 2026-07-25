// Package universe is the Universe read-model projector. It imports ONLY
// domain/universe/events — never internal/domain/universe or internal/eventsourcing — which
// makes the CQRS read/write separation a compile-time fact for this projection (plan §3).
package universe

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/universe/events"
)

const projectorName = "universe-read-model"

type Projector struct{}

func NewProjector() *Projector { return &Projector{} }

func (p *Projector) Name() string { return projectorName }

func (p *Projector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *Projector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	switch env.EventType {
	case events.TypeUniverseCreated:
		return p.handleCreated(ctx, tx, env)
	case events.TypeUniverseRenamed:
		return p.handleRenamed(ctx, tx, env)
	case events.TypeCreatorAdded:
		return p.handleCreatorAdded(ctx, tx, env)
	case events.TypeCreatorRemoved:
		return p.handleCreatorRemoved(ctx, tx, env)
	case events.TypeUniverseArchived:
		return p.handleArchived(ctx, tx, env)
	default:
		// Forward-compatible: a future event type this build doesn't know about yet is
		// skipped rather than treated as an error, so an old projector binary doesn't
		// wedge on new events it doesn't need to react to.
		return nil
	}
}

func (p *Projector) handleCreated(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.UniverseCreated
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("universe projector: unmarshal %s: %w", env.EventType, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO universes_read_model (id, name, is_archived, updated_at)
		 VALUES ($1, $2, false, $3)
		 ON CONFLICT (id) DO NOTHING`,
		env.AggregateID, e.Name, e.OccurredAt,
	); err != nil {
		return err
	}
	for _, userID := range e.CreatorUserIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO universe_creators (universe_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			env.AggregateID, userID,
		); err != nil {
			return err
		}
	}
	return nil
}

func (p *Projector) handleRenamed(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.UniverseRenamed
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("universe projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx,
		`UPDATE universes_read_model SET name = $2, updated_at = $3 WHERE id = $1`,
		env.AggregateID, e.Name, e.OccurredAt,
	)
	return err
}

func (p *Projector) handleCreatorAdded(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.CreatorAdded
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("universe projector: unmarshal %s: %w", env.EventType, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO universe_creators (universe_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		env.AggregateID, e.UserID,
	); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE universes_read_model SET updated_at = $2 WHERE id = $1`, env.AggregateID, e.OccurredAt)
	return err
}

func (p *Projector) handleCreatorRemoved(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.CreatorRemoved
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("universe projector: unmarshal %s: %w", env.EventType, err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM universe_creators WHERE universe_id = $1 AND user_id = $2`,
		env.AggregateID, e.UserID,
	); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE universes_read_model SET updated_at = $2 WHERE id = $1`, env.AggregateID, e.OccurredAt)
	return err
}

func (p *Projector) handleArchived(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.UniverseArchived
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("universe projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx,
		`UPDATE universes_read_model SET is_archived = true, updated_at = $2 WHERE id = $1`,
		env.AggregateID, e.OccurredAt,
	)
	return err
}
