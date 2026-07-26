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
	// e.References is nil when a Ruleset is created without references (the common case: the
	// command API's CreateRulesetRequest only requires a name). pgx encodes a nil []string as SQL
	// NULL, which violates reference_urls' NOT NULL constraint and bypasses its DEFAULT '{}' —
	// defaults only apply when a column is omitted from the INSERT, not when NULL is supplied
	// explicitly. Normalize to a non-nil empty slice so it round-trips as '{}' instead.
	references := e.References
	if references == nil {
		references = []string{}
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO rulesets_read_model (id, name, description, reference_urls, is_archived, updated_at)
		 VALUES ($1, $2, $3, $4, false, $5)
		 ON CONFLICT (id) DO NOTHING`,
		env.AggregateID, e.Name, e.Description, references, e.OccurredAt,
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
	// Same nil-vs-NULL normalization as handleCreated: a request that sets references to an
	// empty list arrives here as e.References == nil, which must not hit reference_urls (NOT
	// NULL) as SQL NULL.
	references := e.References
	if references == nil {
		references = []string{}
	}
	_, err := tx.Exec(ctx, `UPDATE rulesets_read_model SET reference_urls = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, references, e.OccurredAt)
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
