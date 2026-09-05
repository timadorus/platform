package timadorus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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

// defaultTraitPoints is the number of trait points a new "timadorus"-ruleset Character starts
// with — CharacterCreated's own default-seeding sibling to CampaignProcessor's
// defaultTraits/defaultMaxStatBudget. Not a `const` for the same reason those aren't: matches
// this package's established shape for a default seed value, even though a plain int could be a
// const on its own.
var defaultTraitPoints = 2

// CharacterProcessor implements projection.Projector unmodified, but — unlike every read-model
// projector — legitimately re-enters the write side (loads and saves a Character aggregate).
// This is why it lives in its own binary (cmd/timadorus-engine) rather than alongside the
// read-only projectors in cmd/projector: importing domain/character/eventsourcing/eventstore
// here would break that binary's documented "never imports domain invariant code" guarantee.
//
// Reacts to two event types on a "timadorus"-ruleset Character's Campaign: CharacterCreated seeds
// stats.traitPoints/stats.traits with their starting defaults, and ActionRequested recognizes a
// {"action":"addTrait","trait":"<name>"} payload — validating it against the Campaign's own
// configured trait list, the Character's current traitPoints, and its existing traits before
// applying it, logging (and no-op'ing) any rejection instead of erroring the event. Any other
// ActionRequested payload falls back to appending occurredAt to info's "actions" array, exactly
// as before.
//
// Each action rewrites the entire "actions" array into a new event payload (see
// appendActionTimestamp), so the cost of N actions on one Character is O(N^2) bytes across
// the event log — a known, accepted consequence of reusing SetInfo rather than a new
// mutation path, not a bug, but worth flagging for whoever later sizes this feature for
// heavy use.
//
// Unlike every existing (idempotent, upsert-based) projector, this Processor's effect is not
// naturally idempotent on replay: a checkpoint reset would re-append every historical
// timestamp rather than converge to the same state (CharacterCreated's own seeding is a plain
// idempotent overwrite; only the ActionRequested fallback isn't). Relatedly, a dead-lettered
// ActionRequested that's later replayed after the checkpoint has already advanced past it is
// silently skipped, not reprocessed.
type CharacterProcessor struct {
	characters *eventsourcing.Repository[*character.Character]
	cache      *RulesetCache
	logger     *slog.Logger
}

// NewCharacterProcessor builds its own Registry scoped to just Character — the only aggregate
// type this processor ever loads/saves — mirroring cmd/command-api/main.go's construction
// pattern (registry -> postgres.NewStore(pool, registry) -> eventsourcing.NewRepository(store,
// ...)). cache is shared with CampaignProcessor — see RulesetCache's doc comment.
func NewCharacterProcessor(pool *pgxpool.Pool, cache *RulesetCache, logger *slog.Logger) *CharacterProcessor {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	return &CharacterProcessor{
		characters: eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
			return &character.Character{}
		}),
		cache:  cache,
		logger: logger,
	}
}

func (p *CharacterProcessor) Name() string { return CharacterProcessorName }

func (p *CharacterProcessor) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *CharacterProcessor) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	switch env.EventType {
	case events.TypeCharacterCreated:
		return p.handleCharacterCreated(ctx, tx, env)
	case events.TypeActionRequested:
		return p.handleActionRequested(ctx, tx, env)
	default:
		return nil
	}
}

// handleCharacterCreated merges the default stats object into a newly created Character's info,
// but only if that Character's Campaign uses the "timadorus" Ruleset. Resolves the ruleset via
// the shared cache's own campaigns_read_model join (RulesetCache.resolve) — CharacterCreated
// carries CampaignID directly, unlike ActionRequested's envelope (which only carries the
// Character's own id and needs an extra characters_read_model hop to find it), but it does NOT
// carry RulesetID the way CampaignCreated does, so — unlike CampaignProcessor.handleCampaignCreated's
// event-store-based resolution — this cannot skip the read-model join on a cache miss.
func (p *CharacterProcessor) handleCharacterCreated(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.CharacterCreated
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf(errPrefix+"unmarshal %s: %w", env.EventType, err)
	}

	rulesetName, err := p.cache.resolve(ctx, tx, e.CampaignID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	return p.mutateInfo(ctx, tx, env, env.AggregateID, func(info map[string]any) {
		info["stats"] = map[string]any{"traitPoints": defaultTraitPoints, "traits": []string{}}
	})
}

// characterAction is the one recognized shape of a PUT .../action payload today — everything
// else (including the empty {} the CLI's generic `action` verb and this package's own tests
// send) falls through to the pre-existing timestamp-append behavior below. Extend this dispatch,
// not the fallback, when the next real action is added.
type characterAction struct {
	Action string `json:"action"`
	Trait  string `json:"trait"`
}

// handleActionRequested is Handle's original logic, renamed to make room for
// handleCharacterCreated as its sibling. A recognized {"action":"addTrait","trait":"<name>"}
// payload is validated against the Character's own Campaign's configured trait list before being
// applied (see tryAddTrait) — once recognized, it commits to that outcome (a mutation, or a
// logged rejection) and does NOT fall through to the timestamp-append fallback, since that
// fallback is for a genuinely different, unrecognized action, not a rejected one. Any other
// payload appends occurredAt to the "actions" array instead, exactly as before.
func (p *CharacterProcessor) handleActionRequested(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
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

	var action characterAction
	if err := json.Unmarshal([]byte(e.Payload), &action); err == nil && action.Action == "addTrait" {
		return p.tryAddTrait(ctx, tx, env, campaignID, action.Trait)
	}

	return p.appendActionTimestamp(ctx, tx, env, e.OccurredAt)
}

// tryAddTrait validates a requested trait against the Character's own Campaign's configured
// trait list, current traitPoints, and the Character's own existing traits, before applying it —
// the engine, never the SPA, is the source of truth for eligibility. Deliberately NOT built on
// mutateInfo: unlike every mutateInfo caller, this mutation is conditional, and forcing a
// conditional skip through mutateInfo's unconditional-mutate contract would still raise a
// spurious InfoChanged event with no real change. Every rejection is logged, not silent — a
// legitimate, expected outcome the SPA can't always prevent (e.g. a stale picker, or a race with
// someone else's concurrent Add Trait), not a bug.
func (p *CharacterProcessor) tryAddTrait(ctx context.Context, tx pgx.Tx, env bus.Envelope, campaignID uuid.UUID, trait string) error {
	characterID := env.AggregateID

	campaignTraits, err := loadCampaignTraits(ctx, tx, campaignID)
	if err != nil {
		return err
	}
	if !containsString(campaignTraits, trait) {
		p.logger.Warn("addTrait rejected: trait is not in the Campaign's own trait list",
			"characterID", characterID, "campaignID", campaignID, "trait", trait)
		return nil
	}

	txCtx := postgres.WithTx(observability.WithCorrelationID(ctx, env.CorrelationID()), tx)
	c, err := p.characters.Load(txCtx, characterID)
	if err != nil {
		return fmt.Errorf(errPrefix+"load character %s: %w", characterID, err)
	}

	info := map[string]any{}
	if raw := c.Info(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &info) // best-effort; info stays {} on failure
	}
	stats, _ := info["stats"].(map[string]any)
	if stats == nil {
		stats = map[string]any{}
	}
	traitPoints, _ := stats["traitPoints"].(float64) // JSON numbers decode as float64 into map[string]any
	if traitPoints <= 0 {
		p.logger.Warn("addTrait rejected: no traitPoints remaining",
			"characterID", characterID, "campaignID", campaignID, "trait", trait)
		return nil
	}
	existingTraits, _ := stats["traits"].([]any)
	for _, t := range existingTraits {
		if s, ok := t.(string); ok && s == trait {
			p.logger.Warn("addTrait rejected: Character already has this trait",
				"characterID", characterID, "campaignID", campaignID, "trait", trait)
			return nil
		}
	}

	stats["traitPoints"] = traitPoints - 1
	stats["traits"] = append(existingTraits, trait)
	info["stats"] = stats

	newInfo, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf(errPrefix+"marshal updated info for character %s: %w", characterID, err)
	}
	if err := c.SetInfo(string(newInfo)); err != nil {
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

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// loadCampaignTraits reads the Campaign's own configured trait list directly from
// campaigns_read_model — a plain cross-projection read-model query, matching
// RulesetCache.resolve's own already-established pattern. Best-effort on parse failure (an
// unparseable or absent configuration yields an empty trait list, rejecting any addTrait request
// rather than erroring the whole event) — same "start fresh rather than error" philosophy
// mutateInfo/mutateConfiguration already use for a malformed opaque JSON field.
func loadCampaignTraits(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID) ([]string, error) {
	var raw string
	if err := tx.QueryRow(ctx,
		`SELECT configuration FROM campaigns_read_model WHERE id = $1`, campaignID,
	).Scan(&raw); err != nil {
		return nil, fmt.Errorf(errPrefix+"look up configuration for campaign %s: %w", campaignID, err)
	}
	var config struct {
		Traits []string `json:"traits"`
	}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &config) // best-effort; empty list on failure
	}
	return config.Traits, nil
}

// appendActionTimestamp is the fallback for any ActionRequested payload that isn't a recognized
// addTrait action (including the historical {} the CLI's generic action verb sends) — appends
// occurredAt to info's "actions" list via the shared mutateInfo helper.
func (p *CharacterProcessor) appendActionTimestamp(ctx context.Context, tx pgx.Tx, env bus.Envelope, occurredAt time.Time) error {
	return p.mutateInfo(ctx, tx, env, env.AggregateID, func(info map[string]any) {
		actions, _ := info["actions"].([]any)
		info["actions"] = append(actions, occurredAt.UTC().Format(time.RFC3339Nano))
	})
}

// mutateInfo loads characterID, parses its current Info into a generic map (starting fresh on
// any parse failure — the existing info isn't necessarily valid JSON or even a JSON object at
// all, since SetInfo accepts any string), applies mutate to update exactly the key(s) it owns,
// re-marshals, and saves via SetInfo. Shared by handleCharacterCreated ("stats", a plain
// idempotent overwrite) and appendActionTimestamp ("actions", an append that is NOT idempotent
// under event replay — a checkpoint reset would re-append every historical timestamp rather than
// converge) so each call only ever touches the key(s) it owns. Deliberately NOT used by
// tryAddTrait — see that function's own doc comment for why.
func (p *CharacterProcessor) mutateInfo(ctx context.Context, tx pgx.Tx, env bus.Envelope, characterID uuid.UUID, mutate func(map[string]any)) error {
	txCtx := postgres.WithTx(observability.WithCorrelationID(ctx, env.CorrelationID()), tx)

	c, err := p.characters.Load(txCtx, characterID)
	if err != nil {
		return fmt.Errorf(errPrefix+"load character %s: %w", characterID, err)
	}

	info := map[string]any{}
	if raw := c.Info(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &info) // best-effort; info stays {} on failure
	}
	mutate(info)

	newInfo, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf(errPrefix+"marshal updated info for character %s: %w", characterID, err)
	}
	if err := c.SetInfo(string(newInfo)); err != nil {
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
