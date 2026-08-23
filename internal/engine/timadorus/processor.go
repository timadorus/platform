package timadorus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/character"
	"github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
)

// ProcessorName is both the durable JetStream consumer name and the checkpoint table key (see
// projection.Projector.Name's doc comment). It reuses the shared projection_checkpoints table
// (internal/projection/checkpoint) — no new migration needed.
const ProcessorName = "timadorus-engine"

// targetRulesetName is matched case-insensitively against each Character's Campaign's Ruleset
// name (design spec §2) — the one hardcoded piece of business logic this initial version has.
const targetRulesetName = "timadorus"

// Processor implements projection.Projector unmodified, but — unlike every read-model
// projector — legitimately re-enters the write side (loads and saves a Character aggregate).
// This is why it lives in its own binary (cmd/timadorus-engine) rather than alongside the
// read-only projectors in cmd/projector: importing domain/character/eventsourcing/eventstore
// here would break that binary's documented "never imports domain invariant code" guarantee.
type Processor struct {
	characters *eventsourcing.Repository[*character.Character]
	cache      *rulesetCache
}

// NewProcessor builds its own Registry scoped to just Character — the only aggregate type this
// processor ever loads/saves — mirroring cmd/command-api/main.go's construction pattern
// (registry -> postgres.NewStore(pool, registry) -> eventsourcing.NewRepository(store, ...)).
func NewProcessor(pool *pgxpool.Pool) *Processor {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	return &Processor{
		characters: eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
			return &character.Character{}
		}),
		cache: newRulesetCache(),
	}
}

func (p *Processor) Name() string { return ProcessorName }

func (p *Processor) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *Processor) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	if env.EventType != events.TypeActionRequested {
		return nil
	}
	var e events.ActionRequested
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("timadorus-engine: unmarshal %s: %w", env.EventType, err)
	}

	var campaignID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT campaign_id FROM characters_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&campaignID); err != nil {
		return fmt.Errorf("timadorus-engine: look up campaign for character %s: %w", env.AggregateID, err)
	}

	rulesetName, err := p.rulesetName(ctx, tx, campaignID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	return p.appendActionTimestamp(ctx, tx, env.AggregateID, e.OccurredAt)
}

func (p *Processor) rulesetName(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID) (string, error) {
	if name, ok := p.cache.get(campaignID); ok {
		return name, nil
	}

	var name string
	err := tx.QueryRow(ctx,
		`SELECT r.name FROM campaigns_read_model c
		 JOIN rulesets_read_model r ON r.id = c.ruleset_id
		 WHERE c.id = $1`, campaignID,
	).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("timadorus-engine: look up ruleset for campaign %s: %w", campaignID, err)
	}

	p.cache.set(campaignID, name)
	return name, nil
}

// appendActionTimestamp loads the Character within tx (via postgres.WithTx, so the write
// commits atomically with the Router's own checkpoint advance — a concurrency conflict here
// just fails Handle, which the Router already retries via Nack + redelivery, re-Loading the
// current version), appends occurredAt to info's "actions" list, and saves via the
// already-existing SetInfo — reusing the whole InfoChanged/projector/read-model pipeline
// built for that feature, not a new mutation path.
func (p *Processor) appendActionTimestamp(ctx context.Context, tx pgx.Tx, characterID uuid.UUID, occurredAt time.Time) error {
	txCtx := postgres.WithTx(ctx, tx)

	c, err := p.characters.Load(txCtx, characterID)
	if err != nil {
		return fmt.Errorf("timadorus-engine: load character %s: %w", characterID, err)
	}

	// Parse into a generic map, touching only "actions" — never discard other top-level keys a
	// human might have set via the already-existing PUT .../info endpoint. If the existing
	// info isn't valid JSON, or isn't a JSON object at all (allowed today: SetInfo accepts any
	// string), start fresh rather than erroring — retrying won't fix malformed content, so
	// erroring here would get this Character permanently stuck instead of self-healing.
	info := map[string]any{}
	if raw := c.Info(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &info) // best-effort; info stays {} on failure
	}
	actions, _ := info["actions"].([]any)
	info["actions"] = append(actions, occurredAt.UTC().Format(time.RFC3339))

	newInfo, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("timadorus-engine: marshal updated info for character %s: %w", characterID, err)
	}

	if err := c.SetInfo(string(newInfo)); err != nil {
		return fmt.Errorf("timadorus-engine: set info for character %s: %w", characterID, err)
	}
	if err := p.characters.Save(txCtx, c); err != nil {
		return fmt.Errorf("timadorus-engine: save character %s: %w", characterID, err)
	}
	return nil
}
