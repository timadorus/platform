package timadorus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/domain/campaign"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/domain/character"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
)

// ReconcilerInterval is how often Reconciler's sweep runs. 30 seconds converges any residual gap
// almost immediately from a user's perspective, and is cheap at this project's current scale (see
// reconcileCampaigns'/reconcileCharacters' own doc comments on the full-table-scan tradeoff).
const ReconcilerInterval = 30 * time.Second

// Reconciler periodically scans every "timadorus"-ruleset Campaign and Character for missing
// default configuration/stats fields and fills in exactly what's missing, additively — never
// overwriting a value that's already present. This is a completely separate code path from
// CampaignProcessor/CharacterProcessor's own event-driven Handle logic: no NATS, no Router, no
// checkpoint, no dead-letter concept — a plain periodic background job using the same
// eventsourcing.Repository Load/Save primitives command-line tooling already uses directly. It
// exists to self-heal two related gaps neither event-driven processor can close on its own:
//
//   - A Campaign created before this engine's traits/max-stat-budget defaults shipped never gets
//     them (CampaignCreated already fired and won't fire again) — see docs/BACKLOG.md's (now
//     resolved) "no backfill for pre-existing Campaigns" entry.
//   - A Character created immediately after its own Campaign can still, in principle, race ahead
//     of CampaignProcessor's own async seeding of that Campaign's defaults and miss
//     stats.statBudget/stats.attributes — see docs/BACKLOG.md's (now resolved) URGENT entry.
//     handleCharacterCreated reads the Campaign's write-side aggregate directly to shrink this
//     race (see its own doc comment), but doesn't eliminate it; this sweep provides the guarantee.
//
// Every sweep runs two passes, Campaign then Character (in that order, so a Campaign backfilled
// moments ago is already visible to the Character pass in the same sweep). Each pass does a cheap
// read-model scan first to find *candidate* targets, then re-checks each target fresh against its
// live write-side aggregate immediately before writing — so a scan based on a briefly stale read
// model can never cause an incorrect write; correctness is enforced at write time, not scan time.
type Reconciler struct {
	pool       *pgxpool.Pool
	campaigns  *eventsourcing.Repository[*campaign.Campaign]
	characters *eventsourcing.Repository[*character.Character]
	logger     *slog.Logger
}

// NewReconciler builds its own Registries/Stores scoped to Campaign and Character, independently
// of CampaignProcessor/CharacterProcessor's own — mirroring this package's established convention
// of each processor/job constructing its own repositories rather than sharing instances (see
// NewCampaignProcessor/NewCharacterProcessor).
func NewReconciler(pool *pgxpool.Pool, logger *slog.Logger) *Reconciler {
	campaignRegistry := eventsourcing.NewRegistry()
	campaignevents.Register(campaignRegistry)
	campaignStore := postgres.NewStore(pool, campaignRegistry)

	characterRegistry := eventsourcing.NewRegistry()
	characterevents.Register(characterRegistry)
	characterStore := postgres.NewStore(pool, characterRegistry)

	return &Reconciler{
		pool: pool,
		campaigns: eventsourcing.NewRepository(campaignStore, campaign.AggregateType, func() *campaign.Campaign {
			return &campaign.Campaign{}
		}),
		characters: eventsourcing.NewRepository(characterStore, character.AggregateType, func() *character.Character {
			return &character.Character{}
		}),
		logger: logger,
	}
}

// Run sweeps once immediately (so a freshly-deployed engine catches up right away) and then every
// interval, until ctx is cancelled.
func (r *Reconciler) Run(ctx context.Context, interval time.Duration) {
	r.SweepOnce(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.SweepOnce(ctx)
		}
	}
}

// SweepOnce runs one Campaign-then-Character reconciliation pass. Exported so tests can drive a
// single, synchronous sweep directly without a ticker/goroutine.
func (r *Reconciler) SweepOnce(ctx context.Context) {
	r.reconcileCampaigns(ctx)
	r.reconcileCharacters(ctx)
}

// reconcileCampaignConfig mirrors campaignConfiguration's shape but uses a pointer for Traits too
// (not campaignConfiguration's plain []string), so "the key was never set" (nil) is never confused
// with "the key was deliberately set to an empty array" (non-nil, empty). Today no code path lets
// a GM actually clear the trait list back to empty (no such edit action exists), so that state is
// unreachable in practice — but this parses it correctly regardless, in case that ever changes.
type reconcileCampaignConfig struct {
	Traits            *[]string `json:"traits"`
	CharacterCreation struct {
		MaxStatBudget *float64 `json:"maxStatBudget"`
	} `json:"characterCreation"`
}

// reconcileCampaigns scans campaigns_read_model for "timadorus" Campaigns whose configuration
// looks like it might be missing traits and/or characterCreation.maxStatBudget, then re-checks and
// backfills each one against its live aggregate. A full table scan every 30s is fine at this
// project's current scale (see design spec's Out of Scope section); revisit with an index or a
// dedicated queue if ever measured to matter.
func (r *Reconciler) reconcileCampaigns(ctx context.Context) {
	rows, err := r.pool.Query(ctx,
		`SELECT c.id, c.configuration
		 FROM campaigns_read_model c
		 JOIN rulesets_read_model rs ON rs.id = c.ruleset_id
		 WHERE rs.name ILIKE $1 AND c.is_archived = false`, targetRulesetName)
	if err != nil {
		r.logger.Error(errPrefix+"reconcile: query campaigns", "error", err)
		return
	}
	defer rows.Close()

	var targets []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		var raw string
		if err := rows.Scan(&id, &raw); err != nil {
			r.logger.Error(errPrefix+"reconcile: scan campaign row", "error", err)
			return
		}
		var config reconcileCampaignConfig
		if raw != "" {
			_ = json.Unmarshal([]byte(raw), &config)
		}
		if config.Traits == nil || config.CharacterCreation.MaxStatBudget == nil {
			targets = append(targets, id)
		}
	}
	if err := rows.Err(); err != nil {
		r.logger.Error(errPrefix+"reconcile: iterate campaign rows", "error", err)
		return
	}

	for _, id := range targets {
		if err := r.backfillCampaign(ctx, id); err != nil {
			r.logger.Error(errPrefix+"reconcile: backfill campaign", "campaignID", id, "error", err)
		}
	}
}

// backfillCampaign re-checks campaignID's live configuration (not trusting the scan's possibly-
// stale read-model snapshot) and fills in only whichever of traits/characterCreation.maxStatBudget
// is genuinely absent, via a plain map-key-presence check — never overwriting a key that already
// exists, however that value looks. A concurrent edit (e.g. a GM changing the budget at this exact
// moment) surfaces as eventsourcing.ErrConcurrencyConflict, logged and left for the next sweep —
// no special handling needed beyond what Save already returns.
func (r *Reconciler) backfillCampaign(ctx context.Context, campaignID uuid.UUID) error {
	c, err := r.campaigns.Load(ctx, campaignID)
	if err != nil {
		return fmt.Errorf(errPrefix+"reconcile: load campaign %s: %w", campaignID, err)
	}

	config := map[string]any{}
	if raw := c.Configuration(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &config)
	}

	changed := false
	if _, ok := config["traits"]; !ok {
		config["traits"] = defaultTraits
		changed = true
	}
	cc, _ := config["characterCreation"].(map[string]any)
	if cc == nil {
		cc = map[string]any{}
	}
	if _, ok := cc["maxStatBudget"]; !ok {
		cc["maxStatBudget"] = defaultMaxStatBudget
		changed = true
	}
	config["characterCreation"] = cc

	if !changed {
		return nil
	}

	newConfig, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf(errPrefix+"reconcile: marshal backfilled configuration for campaign %s: %w", campaignID, err)
	}
	if err := c.SetConfiguration(string(newConfig)); err != nil {
		if errors.Is(err, campaign.ErrArchived) {
			return nil
		}
		return fmt.Errorf(errPrefix+"reconcile: set configuration for campaign %s: %w", campaignID, err)
	}
	if err := r.campaigns.Save(ctx, c); err != nil {
		return fmt.Errorf(errPrefix+"reconcile: save campaign %s: %w", campaignID, err)
	}
	r.logger.Info(errPrefix+"reconcile: backfilled Campaign configuration defaults", "campaignID", campaignID)
	return nil
}

// reconcileCharacters scans characters_read_model (joined against campaigns_read_model and
// rulesets_read_model) for "timadorus" Characters whose stats look like they might be missing
// attributes/statBudget entirely, or missing stats altogether, then re-checks and backfills each
// one against its live aggregate — same full-scan tradeoff as reconcileCampaigns.
func (r *Reconciler) reconcileCharacters(ctx context.Context) {
	rows, err := r.pool.Query(ctx,
		`SELECT ch.id, ch.info, cp.configuration
		 FROM characters_read_model ch
		 JOIN campaigns_read_model cp ON cp.id = ch.campaign_id
		 JOIN rulesets_read_model rs ON rs.id = cp.ruleset_id
		 WHERE rs.name ILIKE $1 AND ch.is_archived = false`, targetRulesetName)
	if err != nil {
		r.logger.Error(errPrefix+"reconcile: query characters", "error", err)
		return
	}
	defer rows.Close()

	var targets []uuid.UUID
	for rows.Next() {
		var characterID uuid.UUID
		var infoRaw, configRaw string
		if err := rows.Scan(&characterID, &infoRaw, &configRaw); err != nil {
			r.logger.Error(errPrefix+"reconcile: scan character row", "error", err)
			return
		}
		var config reconcileCampaignConfig
		if configRaw != "" {
			_ = json.Unmarshal([]byte(configRaw), &config)
		}
		if config.CharacterCreation.MaxStatBudget == nil {
			continue // this Character's own Campaign has no budget yet — nothing to backfill from
		}

		info := map[string]any{}
		if infoRaw != "" {
			_ = json.Unmarshal([]byte(infoRaw), &info)
		}
		stats, _ := info["stats"].(map[string]any)
		if stats == nil {
			targets = append(targets, characterID)
			continue
		}
		_, hasAttributes := stats["attributes"]
		_, hasStatBudget := stats["statBudget"]
		if !hasAttributes || !hasStatBudget {
			targets = append(targets, characterID)
		}
	}
	if err := rows.Err(); err != nil {
		r.logger.Error(errPrefix+"reconcile: iterate character rows", "error", err)
		return
	}

	for _, id := range targets {
		if err := r.backfillCharacter(ctx, id); err != nil {
			r.logger.Error(errPrefix+"reconcile: backfill character", "characterID", id, "error", err)
		}
	}
}

// backfillCharacter re-checks characterID's own Campaign and its own current stats fresh (not
// trusting the scan's possibly-stale read-model snapshot), then fills in only whichever of
// attributes/statBudget is genuinely absent — or seeds the complete default stats object if stats
// is absent entirely (a Character older than the whole feature). Never touches traitPoints/traits
// once stats exists: those are legitimately mutated by addTrait (a spent-down traitPoints: 0 is a
// normal, common state), and this reconciler's job is limited to attributes/statBudget — see the
// design spec's Out of Scope section.
func (r *Reconciler) backfillCharacter(ctx context.Context, characterID uuid.UUID) error {
	c, err := r.characters.Load(ctx, characterID)
	if err != nil {
		return fmt.Errorf(errPrefix+"reconcile: load character %s: %w", characterID, err)
	}

	var campaignID uuid.UUID
	if err := r.pool.QueryRow(ctx,
		`SELECT campaign_id FROM characters_read_model WHERE id = $1`, characterID,
	).Scan(&campaignID); err != nil {
		return fmt.Errorf(errPrefix+"reconcile: look up campaign for character %s: %w", characterID, err)
	}

	campaignAgg, err := r.campaigns.Load(ctx, campaignID)
	if err != nil {
		return fmt.Errorf(errPrefix+"reconcile: load campaign %s for character %s: %w", campaignID, characterID, err)
	}
	config := parseCampaignConfiguration(campaignAgg.Configuration())
	if config.CharacterCreation.MaxStatBudget == nil {
		return nil // this Character's own Campaign still has no budget — retry next sweep
	}

	info := map[string]any{}
	if raw := c.Info(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &info)
	}
	stats, _ := info["stats"].(map[string]any)
	changed := false
	if stats == nil {
		stats = map[string]any{
			"traitPoints": defaultTraitPoints,
			"traits":      []string{},
			"attributes":  defaultAttributes(),
			"statBudget":  *config.CharacterCreation.MaxStatBudget,
		}
		changed = true
	} else {
		if _, ok := stats["attributes"]; !ok {
			stats["attributes"] = defaultAttributes()
			changed = true
		}
		if _, ok := stats["statBudget"]; !ok {
			stats["statBudget"] = *config.CharacterCreation.MaxStatBudget
			changed = true
		}
	}
	if !changed {
		return nil
	}
	info["stats"] = stats

	newInfo, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf(errPrefix+"reconcile: marshal backfilled info for character %s: %w", characterID, err)
	}
	if err := c.SetInfo(string(newInfo)); err != nil {
		if errors.Is(err, character.ErrArchived) {
			return nil
		}
		return fmt.Errorf(errPrefix+"reconcile: set info for character %s: %w", characterID, err)
	}
	if err := r.characters.Save(ctx, c); err != nil {
		return fmt.Errorf(errPrefix+"reconcile: save character %s: %w", characterID, err)
	}
	r.logger.Info(errPrefix+"reconcile: backfilled Character stats defaults", "characterID", characterID, "campaignID", campaignID)
	return nil
}
