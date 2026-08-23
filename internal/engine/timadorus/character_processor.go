package timadorus

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/timadorus/platform/internal/observability"
)

// CharacterProcessorName is both the durable JetStream consumer name and the checkpoint table
// key (see projection.Projector.Name's doc comment). Reuses the shared projection_checkpoints
// table (internal/projection/checkpoint) — no new migration needed.
const CharacterProcessorName = "timadorus-engine"

// targetRulesetName is matched case-insensitively against each triggering aggregate's Campaign's
// Ruleset name (design spec §2) — the one hardcoded piece of business logic both processors in
// this package share.
const targetRulesetName = "timadorus"

// CharacterProcessor implements projection.Projector unmodified, but — unlike every read-model
// projector — legitimately re-enters the write side (loads and saves a Character aggregate).
// This is why it lives in its own binary (cmd/timadorus-engine) rather than alongside the
// read-only projectors in cmd/projector: importing domain/character/eventsourcing/eventstore
// here would break that binary's documented "never imports domain invariant code" guarantee.
//
// Each action rewrites the entire "actions" array into a new event payload (see
// appendActionTimestamp), so the cost of N actions on one Character is O(N^2) bytes across
// the event log — a known, accepted consequence of reusing SetInfo rather than a new
// mutation path, not a bug, but worth flagging for whoever later sizes this feature for
// heavy use.
//
// Unlike every existing (idempotent, upsert-based) projector, this Processor's effect is not
// naturally idempotent on replay: a checkpoint reset would re-append every historical
// timestamp rather than converge to the same state. Relatedly, a dead-lettered
// ActionRequested that's later replayed after the checkpoint has already advanced past it is
// silently skipped, not reprocessed.
type CharacterProcessor struct {
	characters *eventsourcing.Repository[*character.Character]
	cache      *RulesetCache
}

// NewCharacterProcessor builds its own Registry scoped to just Character — the only aggregate
// type this processor ever loads/saves — mirroring cmd/command-api/main.go's construction
// pattern (registry -> postgres.NewStore(pool, registry) -> eventsourcing.NewRepository(store,
// ...)). cache is shared with CampaignProcessor — see RulesetCache's doc comment.
func NewCharacterProcessor(pool *pgxpool.Pool, cache *RulesetCache) *CharacterProcessor {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	return &CharacterProcessor{
		characters: eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
			return &character.Character{}
		}),
		cache: cache,
	}
}

func (p *CharacterProcessor) Name() string { return CharacterProcessorName }

func (p *CharacterProcessor) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *CharacterProcessor) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	if env.EventType != events.TypeActionRequested {
		return nil
	}
	var e events.ActionRequested
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf(errPrefix+"unmarshal %s: %w", env.EventType, err)
	}

	var campaignID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT campaign_id FROM characters_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&campaignID); err != nil {
		return fmt.Errorf(errPrefix+"look up campaign for character %s: %w", env.AggregateID, err)
	}

	rulesetName, err := p.cache.resolve(ctx, tx, campaignID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	return p.appendActionTimestamp(ctx, tx, env, e.OccurredAt)
}

// appendActionTimestamp loads the Character — a plain read against committed state via the
// pool, outside tx (postgres.Store.Load never consults the ambient transaction; only the
// later Append/Save does), so a save further down can still race a concurrent writer. That's
// fine: the save goes through postgres.WithTx and is guarded by the aggregate's optimistic
// concurrency check, so a stale load just fails Handle, which the Router already retries via
// Nack + redelivery, re-Loading the current version on the next attempt. It appends
// occurredAt to info's "actions" list and saves via the already-existing SetInfo — reusing
// the whole InfoChanged/projector/read-model pipeline built for that feature, not a new
// mutation path.
func (p *CharacterProcessor) appendActionTimestamp(ctx context.Context, tx pgx.Tx, env bus.Envelope, occurredAt time.Time) error {
	characterID := env.AggregateID
	// Thread the originating request's correlation id through to the InfoChanged event this
	// appends, so its metadata isn't stamped empty (the Router's own ctx carries none) and the
	// event log keeps its causal link back to the PUT .../action request that triggered it.
	txCtx := postgres.WithTx(observability.WithCorrelationID(ctx, env.CorrelationID()), tx)

	c, err := p.characters.Load(txCtx, characterID)
	if err != nil {
		return fmt.Errorf(errPrefix+"load character %s: %w", characterID, err)
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
	info["actions"] = append(actions, occurredAt.UTC().Format(time.RFC3339Nano))

	newInfo, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf(errPrefix+"marshal updated info for character %s: %w", characterID, err)
	}

	if err := c.SetInfo(string(newInfo)); err != nil {
		// A Character archived between the PUT .../action request and this engine processing
		// the resulting ActionRequested is a legitimate, expected race (archive is immediate;
		// this engine only catches up later) — not a failure to retry. Retrying can't
		// un-archive the aggregate, so treating it as an error would get this event
		// permanently dead-lettered instead of cleanly no-op'd, same rationale as the
		// malformed-info case above.
		if errors.Is(err, character.ErrArchived) {
			return nil
		}
		return fmt.Errorf(errPrefix+"set info for character %s: %w", characterID, err)
	}
	if err := p.characters.Save(txCtx, c); err != nil {
		return fmt.Errorf(errPrefix+"save character %s: %w", characterID, err)
	}
	return nil
}
