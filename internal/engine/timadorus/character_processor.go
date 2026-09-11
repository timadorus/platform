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
	"github.com/timadorus/platform/internal/domain/campaign"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/domain/character"
	"github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/engine/timadorus/tables"
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

// attributeAbbreviations is timadorus-engine's own hardcoded canonical list of the ten
// character-sheet attributes a Character starts with — mirrors defaultTraits' shape as a
// package-level default seed list, not something read from anywhere in the Campaign's
// configuration.
var attributeAbbreviations = []string{"ST", "AG", "CO", "QU", "SD", "ME", "RE", "EM", "PR", "IN"}

// initialAttributeValue is both Temp and Pot's starting value for every attribute of a newly
// created "timadorus"-ruleset Character.
var initialAttributeValue = 50

// setAttributeTemp sets attr's own "temp" key and recomputes "bonus" from it via GetStatBonus
// (stat_bonus.go) in the same call — the one function every timadorus-engine code path that
// changes a Character's Temp must go through, so Bonus can never drift out of sync with Temp.
// Pot is left untouched. defaultAttributes (below) is this function's first caller, seeding a
// freshly created Character's own baseline Temp; a future engine function that changes Temp on
// an already-loaded Character calls it the same way tryAddTrait's attributeBonusHook
// (trait_hooks.go) already mutates an existing attribute map's "pot" in place.
func setAttributeTemp(attr map[string]any, temp int) {
	attr["temp"] = temp
	attr["bonus"] = GetStatBonus(temp)
}

// potCost computes the statBudget cost of raising a single attribute's Pot from initial to
// target, per the tiered rule from docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md
// Decision 1: the portion of the increase at or below Pot 90 costs 1 statBudget point per Pot
// point; the portion above Pot 90 costs 5 statBudget points per Pot point. Callers must ensure
// target >= initial themselves (trySubmitPot rejects a decrease before ever calling this) — for
// target < initial this still returns 0 (both tiers clamp negative contributions to zero), it
// just isn't a meaningful "cost" in that case.
func potCost(initial, target int) int {
	below := min(target, 90) - initial
	if below < 0 {
		below = 0
	}
	above := target - max(initial, 90)
	if above < 0 {
		above = 0
	}
	return below + above*5
}

// defaultAttributes builds the starting attributes object for a newly created
// "timadorus"-ruleset Character: all ten of the engine's own hardcoded attributes, Pot at
// initialAttributeValue, Temp and Bonus set together via setAttributeTemp.
func defaultAttributes() map[string]any {
	attrs := make(map[string]any, len(attributeAbbreviations))
	for _, abbr := range attributeAbbreviations {
		attr := map[string]any{"pot": initialAttributeValue}
		setAttributeTemp(attr, initialAttributeValue)
		attrs[abbr] = attr
	}
	return attrs
}

// CharacterProcessor implements projection.Projector unmodified, but — unlike every read-model
// projector — legitimately re-enters the write side (loads and saves a Character aggregate).
// This is why it lives in its own binary (cmd/timadorus-engine) rather than alongside the
// read-only projectors in cmd/projector: importing domain/character/eventsourcing/eventstore
// here would break that binary's documented "never imports domain invariant code" guarantee.
//
// Reacts to two event types on a "timadorus"-ruleset Character's Campaign: CharacterCreated seeds
// stats.traitPoints/stats.traits/stats.attributes with their starting defaults, plus
// stats.statBudget read from the Campaign's own write-side configuration (see
// handleCharacterCreated's doc comment), and ActionRequested recognizes two payload shapes —
// {"action":"addTrait","trait":"<name>"}, validated against the Campaign's own configured trait
// list, the Character's current traitPoints, and its existing traits before applying it; and
// {"action":"submitPot","pot":{"<abbr>":<target>,...}}, a batch of per-attribute Pot increases
// validated as a whole against the Character's own remaining stats.statBudget before any of it is
// applied (see trySubmitPot). Both log (and no-op) any rejection instead of erroring the event. A
// successfully applied addTrait also fires that trait's own hook, if traits.yaml registers one
// for it — "strong"/"agile"/"quick" each grant a flat +5 Pot bonus to their own attribute (see
// trait_hooks.go). Any other ActionRequested payload falls back to appending occurredAt to info's
// "actions" array, exactly as before.
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
	campaigns  *eventsourcing.Repository[*campaign.Campaign]
	cache      *RulesetCache
	traits     *tables.TraitsTable
	logger     *slog.Logger
}

// NewCharacterProcessor builds its own Registry scoped to just Character — the only aggregate
// type this processor ever loads/saves — mirroring cmd/command-api/main.go's construction
// pattern (registry -> postgres.NewStore(pool, registry) -> eventsourcing.NewRepository(store,
// ...)). Also builds a second, independent Repository scoped to Campaign, read-only in practice
// (handleCharacterCreated only ever Loads it, never Saves) — needed to read a Campaign's own
// characterCreation.maxStatBudget from its authoritative write-side state rather than the lagging
// campaigns_read_model projection (see handleCharacterCreated's doc comment and
// docs/superpowers/specs/2026-09-05-statbudget-race-reconciliation-design.md). Mirrors
// CampaignProcessor's own construction of a second repository (its Ruleset one) for the same
// reason: a second, narrowly-scoped read into a different aggregate type. cache is shared with
// CampaignProcessor — see RulesetCache's doc comment. traits is the Timadorus Ruleset's own
// traits.yaml table, already loaded and hook-registered by the caller (cmd/timadorus-engine's
// startup calls tables.LoadTraits then RegisterTraitHooks before constructing this processor) —
// tryAddTrait uses it to fire each trait's own stat-bonus hook, if it has one (see trait_hooks.go).
func NewCharacterProcessor(pool *pgxpool.Pool, cache *RulesetCache, traits *tables.TraitsTable, logger *slog.Logger) *CharacterProcessor {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	campaignRegistry := eventsourcing.NewRegistry()
	campaignevents.Register(campaignRegistry)
	campaignStore := postgres.NewStore(pool, campaignRegistry)

	return &CharacterProcessor{
		characters: eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
			return &character.Character{}
		}),
		campaigns: eventsourcing.NewRepository(campaignStore, campaign.AggregateType, func() *campaign.Campaign {
			return &campaign.Campaign{}
		}),
		cache:  cache,
		traits: traits,
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
//
// Reads the Campaign's own characterCreation.maxStatBudget directly from the write-side Campaign
// aggregate (p.campaigns.Load), NOT from campaigns_read_model — the engine derives this itself
// rather than trusting a client-submitted value (the same "engine, not the SPA, is the source of
// truth" principle tryAddTrait already applies to trait eligibility), and reading the aggregate
// instead of the read-model projection matters here specifically: CampaignCreated and
// CharacterCreated are handled by two independent NATS consumers with no ordering guarantee
// between them, even though CampaignProcessor's own handleCampaignCreated always seeds a default
// characterCreation.maxStatBudget for a "timadorus" Campaign in reaction to the very same
// CampaignCreated event. Reading campaigns_read_model (as this function used to) added a second,
// slower asynchronous hop on top of that race — ConfigurationChanged's own outbox-relay-poll-then-
// projector round trip — that a Character created immediately after its Campaign could easily
// lose, permanently missing statBudget with no error or trace (see BACKLOG.md's now-resolved
// URGENT entry for the full history, including two reverted attempts to fix this via
// Nack-based retry). Reading the aggregate directly removes that second hop, since it reflects
// SetConfiguration the instant it's saved, with zero projection lag — narrowing the remaining race
// to just the two same-binary consumers' own relative scheduling, which ordinary usage doesn't
// hit. Any residual case (or a Campaign/Character that predates this fix) self-heals via
// Reconciler (reconcile.go), a completely independent periodic sweep — not via retrying here.
// If the Campaign has no maxStatBudget yet, statBudget is simply omitted from stats this time;
// Reconciler fills it in once available.
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

	campaignAgg, err := p.campaigns.Load(ctx, e.CampaignID)
	if err != nil {
		return fmt.Errorf(errPrefix+"load campaign %s: %w", e.CampaignID, err)
	}
	config := parseCampaignConfiguration(campaignAgg.Configuration())

	return p.mutateInfo(ctx, tx, env, env.AggregateID, func(info map[string]any) {
		stats := map[string]any{
			"traitPoints": defaultTraitPoints,
			"traits":      []string{},
			"attributes":  defaultAttributes(),
		}
		if config.CharacterCreation.MaxStatBudget != nil {
			stats["statBudget"] = *config.CharacterCreation.MaxStatBudget
		}
		info["stats"] = stats
	})
}

// characterAction is the two recognized shapes of a PUT .../action payload today —
// {"action":"addTrait","trait":"<name>"} and {"action":"submitPot","pot":{"<abbr>":<target>,...}}
// — everything else (including the empty {} the CLI's generic `action` verb and this package's
// own tests send) falls through to the pre-existing timestamp-append behavior below. Extend this
// dispatch, not the fallback, when the next real action is added.
type characterAction struct {
	Action string             `json:"action"`
	Trait  string             `json:"trait"`
	Pot    map[string]float64 `json:"pot"`
}

// handleActionRequested is Handle's original logic, renamed to make room for
// handleCharacterCreated as its sibling. A recognized {"action":"addTrait","trait":"<name>"}
// payload is validated against the Character's own Campaign's configured trait list before being
// applied (see tryAddTrait); a recognized {"action":"submitPot","pot":{...}} payload is validated
// as a whole batch against the Character's own remaining stats.statBudget before being applied
// (see trySubmitPot). Either way, once the action is recognized, this commits to that outcome (a
// mutation, or a logged rejection) and does NOT fall through to the timestamp-append fallback,
// since that fallback is for a genuinely different, unrecognized action, not a rejected one. Any
// other payload appends occurredAt to the "actions" array instead, exactly as before.
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
	if err := json.Unmarshal([]byte(e.Payload), &action); err == nil {
		switch action.Action {
		case "addTrait":
			return p.tryAddTrait(ctx, tx, env, campaignID, action.Trait)
		case "submitPot":
			return p.trySubmitPot(ctx, tx, env, action.Pot)
		}
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
//
// Once the trait is confirmed eligible, this also fires that trait's own hook, if it has one
// (see trait_hooks.go), before saving — via p.traits.Dispatch against the same in-progress stats
// map, not a second Load/Save (see withCharacterStats's own doc comment for why a second
// Load/Save would race this one's). A trait absent from traits.yaml entirely (a Campaign-defined
// trait with no special engine effect) is skipped via the Row(trait) existence check below,
// rather than treated as an error — Dispatch is only ever called for a trait known to have a row.
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

	if _, ok := p.traits.Row(trait); ok {
		hookCtx := withCharacterStats(txCtx, stats)
		if err := p.traits.Dispatch(hookCtx, tx, trait, env); err != nil {
			return fmt.Errorf(errPrefix+"run %q trait hooks for character %s: %w", trait, characterID, err)
		}
	}

	info["stats"] = stats

	newInfo, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf(errPrefix+"marshal updated info for character %s: %w", characterID, err)
	}
	if err := c.SetInfo(string(newInfo)); err != nil {
		if errors.Is(err, character.ErrArchived) {
			p.logger.Warn("addTrait rejected: Character is archived",
				"characterID", characterID, "campaignID", campaignID, "trait", trait)
			return nil
		}
		return fmt.Errorf(errPrefix+"set info for character %s: %w", characterID, err)
	}
	if err := p.characters.Save(txCtx, c); err != nil {
		return fmt.Errorf(errPrefix+"save character %s: %w", characterID, err)
	}
	return nil
}

// trySubmitPot validates a batch of target Pot values against the Character's own current
// stored attributes/statBudget before applying any of them — the engine, never the SPA, computes
// and checks the cost (see potCost and
// docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md). Any single invalid entry (an
// abbreviation not among the Character's existing attributes, a decrease, or a target over 100)
// or a total cost exceeding the current statBudget rejects the WHOLE batch — logged, not silent,
// exactly like tryAddTrait's own rejections — leaving every attribute and statBudget completely
// untouched. Deliberately NOT built on mutateInfo for the same reason tryAddTrait isn't: this
// mutation is conditional, and mutateInfo's contract is unconditional.
func (p *CharacterProcessor) trySubmitPot(ctx context.Context, tx pgx.Tx, env bus.Envelope, pot map[string]float64) error {
	characterID := env.AggregateID
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
		p.logger.Warn("submitPot rejected: Character has no stats seeded yet", "characterID", characterID)
		return nil
	}
	attributes, _ := stats["attributes"].(map[string]any)
	if attributes == nil {
		p.logger.Warn("submitPot rejected: Character has no attributes seeded yet", "characterID", characterID)
		return nil
	}
	statBudget, ok := stats["statBudget"].(float64)
	if !ok {
		p.logger.Warn("submitPot rejected: Character has no statBudget", "characterID", characterID)
		return nil
	}

	totalCost := 0
	for abbr, targetVal := range pot {
		attr, ok := attributes[abbr].(map[string]any)
		if !ok {
			p.logger.Warn("submitPot rejected: unknown attribute abbreviation",
				"characterID", characterID, "abbr", abbr)
			return nil
		}
		currentPot, ok := attr["pot"].(float64)
		if !ok {
			p.logger.Warn("submitPot rejected: attribute has no pot value",
				"characterID", characterID, "abbr", abbr)
			return nil
		}
		target, current := int(targetVal), int(currentPot)
		if target < current {
			p.logger.Warn("submitPot rejected: target pot is below the attribute's current pot",
				"characterID", characterID, "abbr", abbr, "current", current, "target", target)
			return nil
		}
		if target > 100 {
			p.logger.Warn("submitPot rejected: target pot exceeds 100",
				"characterID", characterID, "abbr", abbr, "target", target)
			return nil
		}
		totalCost += potCost(current, target)
	}

	if totalCost > int(statBudget) {
		p.logger.Warn("submitPot rejected: total cost exceeds remaining statBudget",
			"characterID", characterID, "cost", totalCost, "statBudget", statBudget)
		return nil
	}

	for abbr, targetVal := range pot {
		attr, _ := attributes[abbr].(map[string]any)
		attr["pot"] = int(targetVal)
	}
	stats["statBudget"] = int(statBudget) - totalCost
	info["stats"] = stats

	newInfo, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf(errPrefix+"marshal updated info for character %s: %w", characterID, err)
	}
	if err := c.SetInfo(string(newInfo)); err != nil {
		if errors.Is(err, character.ErrArchived) {
			p.logger.Warn("submitPot rejected: Character is archived", "characterID", characterID)
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

// campaignConfiguration is the subset of a Campaign's own opaque configuration JSON this package
// reads: its own configured trait list (tryAddTrait's trait-eligibility check) and its
// character-creation stat budget (handleCharacterCreated's stats.statBudget seeding, and
// Reconciler's own backfilling — reconcile.go). The trait list is read from
// campaigns_read_model.configuration via loadCampaignConfiguration (tryAddTrait's use);
// handleCharacterCreated instead reads the write-side Campaign aggregate's configuration directly
// via parseCampaignConfiguration — see that function's own doc comment for why.
type campaignConfiguration struct {
	Traits            []string `json:"traits"`
	CharacterCreation struct {
		MaxStatBudget *float64 `json:"maxStatBudget"`
	} `json:"characterCreation"`
}

// parseCampaignConfiguration parses a Campaign's raw configuration JSON into campaignConfiguration,
// best-effort: an unparseable or empty string yields a zero value (not an error) — same "start
// fresh rather than error" philosophy mutateInfo/mutateConfiguration already use for a malformed
// opaque JSON field. Shared by loadCampaignConfiguration (reads campaigns_read_model, used by
// tryAddTrait) and handleCharacterCreated (reads the write-side Campaign aggregate directly).
func parseCampaignConfiguration(raw string) campaignConfiguration {
	var config campaignConfiguration
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &config) // best-effort; zero value on failure
	}
	return config
}

// loadCampaignConfiguration reads and parses a Campaign's own configuration column directly from
// campaigns_read_model — used by tryAddTrait's trait-eligibility check (loadCampaignTraits), which
// only ever runs well after Campaign creation (an addTrait action on an existing Character), so
// read-model lag is not a correctness concern there, unlike handleCharacterCreated's own need
// (see that function's doc comment for why it reads the aggregate directly instead).
func loadCampaignConfiguration(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID) (campaignConfiguration, error) {
	var raw string
	if err := tx.QueryRow(ctx,
		`SELECT configuration FROM campaigns_read_model WHERE id = $1`, campaignID,
	).Scan(&raw); err != nil {
		return campaignConfiguration{}, fmt.Errorf(errPrefix+"look up configuration for campaign %s: %w", campaignID, err)
	}
	return parseCampaignConfiguration(raw), nil
}

// loadCampaignTraits extracts the Campaign's own configured trait list from campaigns_read_model
// — see loadCampaignConfiguration for the underlying read. Used only by tryAddTrait; unrelated to
// handleCharacterCreated's own statBudget read, which goes through the write-side Campaign
// aggregate instead (see parseCampaignConfiguration's doc comment).
func loadCampaignTraits(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID) ([]string, error) {
	config, err := loadCampaignConfiguration(ctx, tx, campaignID)
	if err != nil {
		return nil, err
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
