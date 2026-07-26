// Package ruleset is the Ruleset read-model projector. Imports only domain/ruleset/events —
// see projection/universe's doc comment for why.
package ruleset

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/ruleset/events"
)

const projectorName = "ruleset-read-model"

type Projector struct{}

func NewProjector() *Projector { return &Projector{} }

func (p *Projector) Name() string { return projectorName }

func (p *Projector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *Projector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	switch env.EventType {
	case events.TypeRulesetCreated:
		return p.handleCreated(ctx, tx, env)
	case events.TypeRulesetRenamed:
		return p.handleRenamed(ctx, tx, env)
	case events.TypeRulesetDescriptionChanged:
		return p.handleDescriptionChanged(ctx, tx, env)
	case events.TypeRulesetReferencesChanged:
		return p.handleReferencesChanged(ctx, tx, env)
	case events.TypeRulesetArchived:
		return p.handleArchived(ctx, tx, env)
	default:
		return nil
	}
}

func (p *Projector) handleCreated(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.RulesetCreated
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("ruleset projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO rulesets_read_model (id, name, description, reference_urls, is_archived, updated_at)
		 VALUES ($1, $2, $3, $4, false, $5)
		 ON CONFLICT (id) DO NOTHING`,
		env.AggregateID, e.Name, e.Description, e.References, e.OccurredAt,
	)
	return err
}

func (p *Projector) handleRenamed(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.RulesetRenamed
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("ruleset projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx, `UPDATE rulesets_read_model SET name = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, e.Name, e.OccurredAt)
	return err
}

func (p *Projector) handleDescriptionChanged(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.RulesetDescriptionChanged
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("ruleset projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx, `UPDATE rulesets_read_model SET description = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, e.Description, e.OccurredAt)
	return err
}

func (p *Projector) handleReferencesChanged(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.RulesetReferencesChanged
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("ruleset projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx, `UPDATE rulesets_read_model SET reference_urls = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, e.References, e.OccurredAt)
	return err
}

func (p *Projector) handleArchived(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.RulesetArchived
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("ruleset projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx, `UPDATE rulesets_read_model SET is_archived = true, updated_at = $2 WHERE id = $1`, env.AggregateID, e.OccurredAt)
	return err
}
