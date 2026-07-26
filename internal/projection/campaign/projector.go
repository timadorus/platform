// Package campaign is the Campaign read-model projector. Imports only domain/campaign/events
// — see projection/universe's doc comment for why.
package campaign

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign/events"
)

const projectorName = "campaign-read-model"

type Projector struct{}

func NewProjector() *Projector { return &Projector{} }

func (p *Projector) Name() string { return projectorName }

func (p *Projector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *Projector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	switch env.EventType {
	case events.TypeCampaignCreated:
		return p.handleCreated(ctx, tx, env)
	case events.TypeCampaignRenamed:
		return p.handleRenamed(ctx, tx, env)
	case events.TypeGamemasterAdded:
		return p.handleGamemasterAdded(ctx, tx, env)
	case events.TypeGamemasterRemove:
		return p.handleGamemasterRemoved(ctx, tx, env)
	case events.TypeCampaignArchived:
		return p.handleArchived(ctx, tx, env)
	default:
		return nil
	}
}

func (p *Projector) handleCreated(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.CampaignCreated
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("campaign projector: unmarshal %s: %w", env.EventType, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, is_archived, updated_at)
		 VALUES ($1, $2, $3, $4, false, $5)
		 ON CONFLICT (id) DO NOTHING`,
		env.AggregateID, e.Name, e.UniverseID, e.RulesetID, e.OccurredAt,
	); err != nil {
		return err
	}
	for _, userID := range e.GamemasterUserIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO campaign_gamemasters (campaign_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			env.AggregateID, userID,
		); err != nil {
			return err
		}
	}
	return nil
}

func (p *Projector) handleRenamed(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.CampaignRenamed
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("campaign projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx, `UPDATE campaigns_read_model SET name = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, e.Name, e.OccurredAt)
	return err
}

func (p *Projector) handleGamemasterAdded(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.GamemasterAdded
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("campaign projector: unmarshal %s: %w", env.EventType, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO campaign_gamemasters (campaign_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		env.AggregateID, e.UserID,
	); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE campaigns_read_model SET updated_at = $2 WHERE id = $1`, env.AggregateID, e.OccurredAt)
	return err
}

func (p *Projector) handleGamemasterRemoved(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.GamemasterRemoved
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("campaign projector: unmarshal %s: %w", env.EventType, err)
	}
	if _, err := tx.Exec(ctx,
		`DELETE FROM campaign_gamemasters WHERE campaign_id = $1 AND user_id = $2`,
		env.AggregateID, e.UserID,
	); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `UPDATE campaigns_read_model SET updated_at = $2 WHERE id = $1`, env.AggregateID, e.OccurredAt)
	return err
}

func (p *Projector) handleArchived(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.CampaignArchived
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("campaign projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx,
		`UPDATE campaigns_read_model SET is_archived = true, updated_at = $2 WHERE id = $1`,
		env.AggregateID, e.OccurredAt,
	)
	return err
}
