# statBudget Race Fix: Direct-Aggregate Read + Reconciliation Sweep Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the URGENT `docs/BACKLOG.md` item — a Character created immediately after its
Campaign can permanently miss `info.stats.statBudget` — without touching `internal/projection` or
`internal/bus`, which is what sank both prior fix attempts. Also closes the separate, previously
non-urgent "no backfill for pre-existing Campaigns" item using the same new mechanism.

**Architecture:** Two independent, additive mechanisms. (1) `handleCharacterCreated` reads the
Campaign's `characterCreation.maxStatBudget` from the write-side `Campaign` aggregate directly
instead of the lagging `campaigns_read_model` projection, shrinking the race. (2) A new, completely
separate periodic `Reconciler` (no NATS, no Router, no checkpoint) sweeps every 30 seconds,
additively backfilling any Timadorus Campaign/Character missing defaults — closing any residual
race and any pre-existing historical gap.

**Tech Stack:** Go, `pgx`, `eventsourcing.Repository`, Ginkgo/Gomega.

## Global Constraints

- No changes to `internal/projection` or `internal/bus` — both prior fix attempts touched these
  and both were reverted; this plan does not repeat that mistake.
- Every backfill (Campaign or Character) is **additive-only**: fill in a field only if its JSON key
  is genuinely absent (checked via map-key presence or a `*T` pointer being nil — never by
  zero/empty-value), and never touch a field that already has a value, however "small" that value
  looks (e.g. `traitPoints: 0` is a normal, common, real state — never reseed it).
- The reconciler is a plain background job using `eventsourcing.Repository` Load/Save directly —
  no envelope, no ambient transaction, no dead-letter table, no relationship to
  `CampaignProcessor`/`CharacterProcessor`'s own event-driven `Handle` logic.
- A sweep over an already-healthy platform performs zero writes and raises zero events.
- `go build ./... && go vet ./... && go test -count=1 ./...` must stay clean after every task.

---

### Task 1: Read the Campaign aggregate directly in `handleCharacterCreated`

**Files:**
- Modify: `internal/engine/timadorus/character_processor.go`
- Modify: `internal/engine/timadorus/character_processor_test.go`

**Interfaces:**
- Produces: `parseCampaignConfiguration(raw string) campaignConfiguration` (package-private) —
  extracted from `loadCampaignConfiguration`'s existing parse logic, reused by both the read-model
  path (`loadCampaignConfiguration`, used by `tryAddTrait`, unchanged) and the new aggregate-read
  path in `handleCharacterCreated`.
- Produces (test helper): `seedRealCampaign(t, pool, rulesetID uuid.UUID, rulesetName, configuration string) uuid.UUID` — Task 2's `reconcile_test.go` reuses this.

- [ ] **Step 1: Add a `campaigns` repository to `CharacterProcessor` and extract `parseCampaignConfiguration`**

Add two imports to `internal/engine/timadorus/character_processor.go` (alongside the existing
`"github.com/timadorus/platform/internal/domain/character"` /
`"github.com/timadorus/platform/internal/domain/character/events"` pair):

```go
	"github.com/timadorus/platform/internal/domain/campaign"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
```

Replace the `CharacterProcessor` struct and `NewCharacterProcessor` with:

```go
type CharacterProcessor struct {
	characters *eventsourcing.Repository[*character.Character]
	campaigns  *eventsourcing.Repository[*campaign.Campaign]
	cache      *RulesetCache
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
// CampaignProcessor — see RulesetCache's doc comment.
func NewCharacterProcessor(pool *pgxpool.Pool, cache *RulesetCache, logger *slog.Logger) *CharacterProcessor {
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
		logger: logger,
	}
}
```

Replace `handleCharacterCreated`'s doc comment and body with:

```go
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
```

(`p.campaigns.Load` reads via the pool directly, not the ambient `tx` — same as every other
`Repository.Load` call in this package, per `postgres.Store.Load`'s own implementation — so no
`postgres.WithTx` wrapping is needed for this read-only call.)

Replace `loadCampaignConfiguration` with a version that delegates its parsing to a new, extracted
`parseCampaignConfiguration` function (the `campaignConfiguration` type itself is unchanged):

```go
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
```

Finally, update `CharacterProcessor`'s own type doc comment — replace this sentence:

```go
// Reacts to two event types on a "timadorus"-ruleset Character's Campaign: CharacterCreated seeds
// stats.traitPoints/stats.traits/stats.attributes with their starting defaults, plus
// stats.statBudget read from the Campaign's own configuration (see handleCharacterCreated's doc
// comment), and ActionRequested recognizes a {"action":"addTrait","trait":"<name>"} payload —
```

with:

```go
// Reacts to two event types on a "timadorus"-ruleset Character's Campaign: CharacterCreated seeds
// stats.traitPoints/stats.traits/stats.attributes with their starting defaults, plus
// stats.statBudget read from the Campaign's own write-side configuration (see
// handleCharacterCreated's doc comment), and ActionRequested recognizes a
// {"action":"addTrait","trait":"<name>"} payload —
```

Run: `go build ./... && go vet ./...` — expect clean.

- [ ] **Step 2: Add the `seedRealCampaign` test helper and update the two affected tests**

`handleCharacterCreated` now genuinely loads the write-side Campaign aggregate, so the two
`CharacterCreated`-success-path tests can no longer fake the Campaign via a bare
`campaigns_read_model` row (`seedCampaignAndRuleset`'s existing shape) plus
`seedCampaignConfiguration`'s raw `UPDATE` — a `p.campaigns.Load` against a campaign id with no
real event stream would fail. Add this helper to
`internal/engine/timadorus/character_processor_test.go`, immediately after the existing
`seedCampaignAndRuleset` function:

```go
// seedRealCampaign creates a real Campaign aggregate (via campaign.New, with SetConfiguration
// applied if configuration is non-empty) and saves it through the real event store, then seeds
// the matching campaigns_read_model/rulesets_read_model rows via seedCampaignAndRuleset (still
// needed for RulesetCache.resolve's ruleset-name lookup, unchanged by this fix) — needed now that
// handleCharacterCreated loads the write-side Campaign aggregate directly instead of reading
// campaigns_read_model.configuration.
func seedRealCampaign(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID, rulesetName, configuration string) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	registry := eventsourcing.NewRegistry()
	campaignevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})

	c, err := campaign.New(uuid.New(), rulesetID, "Test Campaign", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("campaign.New: %v", err)
	}
	if configuration != "" {
		if err := c.SetConfiguration(configuration); err != nil {
			t.Fatalf("set configuration: %v", err)
		}
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save campaign: %v", err)
	}

	seedCampaignAndRuleset(t, pool, c.AggregateID(), rulesetID, rulesetName)
	return c.AggregateID()
}
```

Add the two new imports this helper needs, to the top of `character_processor_test.go`'s import
block (alongside the existing `"github.com/timadorus/platform/internal/domain/character"` /
`"github.com/timadorus/platform/internal/domain/character/events"` pair):

```go
	"github.com/timadorus/platform/internal/domain/campaign"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
```

In `TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStats`, replace:

```go
	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "TIMADORUS") // exact-case mismatch on purpose
	characterID := uuid.New()
```

with:

```go
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "TIMADORUS", "") // exact-case mismatch on purpose
	characterID := uuid.New()
```

In `TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStatBudgetFromCampaign`, replace:

```go
	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "Timadorus")
	seedCampaignConfiguration(t, pool, campaignID, `{"characterCreation":{"maxStatBudget":40}}`)
```

with:

```go
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", `{"characterCreation":{"maxStatBudget":40}}`)
```

Nothing else in either test changes — the Character-creation, publish, polling, and assertion code
that follows is unaffected. `TestCharacterProcessor_CharacterCreated_NonMatchingRuleset_NoOp` also
needs no change: its non-matching ruleset name means `handleCharacterCreated` returns before ever
reaching `p.campaigns.Load`, so it never needs a real Campaign aggregate to exist.

Run: `go test ./internal/engine/timadorus/... -run TestCharacterProcessor_CharacterCreated -v` —
expect all three PASS.

- [ ] **Step 3: Run full regression check**

Run: `go build ./... && go vet ./... && go test -count=1 ./internal/engine/timadorus/...`
Expected: clean, all tests pass, including every `AddTrait`/timestamp-append test (unaffected —
`loadCampaignConfiguration`'s external contract, used only by `tryAddTrait`, is unchanged).

- [ ] **Step 4: Commit**

```bash
git add internal/engine/timadorus/character_processor.go \
        internal/engine/timadorus/character_processor_test.go
git commit -m "engine: read the Campaign aggregate directly for statBudget, shrinking the creation race"
```

---

### Task 2: Reconciliation sweep

**Files:**
- Create: `internal/engine/timadorus/reconcile.go`
- Create: `internal/engine/timadorus/reconcile_test.go`
- Modify: `cmd/timadorus-engine/main.go`

**Depends on:** Task 1 (`reconcile_test.go` reuses `seedRealCampaign`).

**Interfaces:**
- Produces: `Reconciler` (exported type), `NewReconciler(pool *pgxpool.Pool, logger *slog.Logger) *Reconciler`, `(*Reconciler).SweepOnce(ctx context.Context)`, `(*Reconciler).Run(ctx context.Context, interval time.Duration)`, `ReconcilerInterval` (exported `time.Duration` constant, `30 * time.Second`).

- [ ] **Step 1: Create `internal/engine/timadorus/reconcile.go`**

```go
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
```

`campaignConfiguration`'s sibling `parseCampaignConfiguration` (Task 1) and package-level defaults
`defaultTraits`/`defaultMaxStatBudget` (from `campaign_processor.go`) and `defaultTraitPoints`/
`defaultAttributes` (from `character_processor.go`) are all in this same package (`timadorus`) and
need no import — used directly.

Run: `go build ./... && go vet ./...` — expect clean.

- [ ] **Step 2: Create `internal/engine/timadorus/reconcile_test.go`**

```go
package timadorus_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/campaign"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/domain/character"
	"github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
)

func TestReconciler_Campaign_MissingBoth_Backfilled(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", "") // no configuration at all

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	campaignevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
	c, err := repo.Load(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("load campaign: %v", err)
	}
	if c.Version() != 2 { // 1 for Create, 2 for the sweep's own ConfigurationChanged
		t.Fatalf("got version %d, want 2 (exactly one backfill event)", c.Version())
	}

	var config struct {
		Traits            []string `json:"traits"`
		CharacterCreation struct {
			MaxStatBudget *float64 `json:"maxStatBudget"`
		} `json:"characterCreation"`
	}
	if err := json.Unmarshal([]byte(c.Configuration()), &config); err != nil {
		t.Fatalf("unmarshal configuration: %v", err)
	}
	if len(config.Traits) != 3 {
		t.Fatalf("got traits %v, want 3 defaults", config.Traits)
	}
	if config.CharacterCreation.MaxStatBudget == nil || *config.CharacterCreation.MaxStatBudget != 35 {
		t.Fatalf("got maxStatBudget %v, want 35", config.CharacterCreation.MaxStatBudget)
	}
}

func TestReconciler_Campaign_AlreadyCustomized_NotTouched(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus",
		`{"traits":["custom-trait"],"characterCreation":{"maxStatBudget":50}}`)

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	campaignevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
	c, err := repo.Load(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("load campaign: %v", err)
	}
	if c.Version() != 1 { // only the initial Create — no ConfigurationChanged from the sweep
		t.Fatalf("got version %d, want 1 (sweep must not touch an already-configured Campaign)", c.Version())
	}

	var config struct {
		Traits            []string `json:"traits"`
		CharacterCreation struct {
			MaxStatBudget *float64 `json:"maxStatBudget"`
		} `json:"characterCreation"`
	}
	if err := json.Unmarshal([]byte(c.Configuration()), &config); err != nil {
		t.Fatalf("unmarshal configuration: %v", err)
	}
	if len(config.Traits) != 1 || config.Traits[0] != "custom-trait" {
		t.Fatalf("got traits %v, want [custom-trait] preserved", config.Traits)
	}
	if config.CharacterCreation.MaxStatBudget == nil || *config.CharacterCreation.MaxStatBudget != 50 {
		t.Fatalf("got maxStatBudget %v, want 50 preserved", config.CharacterCreation.MaxStatBudget)
	}
}

func TestReconciler_Character_MissingAttributesAndStatBudget_Backfilled(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", `{"characterCreation":{"maxStatBudget":40}}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `{"stats":{"traitPoints":1,"traits":["agile"]}}`)

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	c, err := repo.Load(context.Background(), characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	var decoded struct {
		Stats struct {
			TraitPoints int            `json:"traitPoints"`
			Traits      []string       `json:"traits"`
			Attributes  map[string]any `json:"attributes"`
			StatBudget  *float64       `json:"statBudget"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(c.Info()), &decoded); err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if decoded.Stats.TraitPoints != 1 {
		t.Fatalf("got traitPoints %d, want 1 (unchanged)", decoded.Stats.TraitPoints)
	}
	if len(decoded.Stats.Traits) != 1 || decoded.Stats.Traits[0] != "agile" {
		t.Fatalf("got traits %v, want [agile] (unchanged)", decoded.Stats.Traits)
	}
	if len(decoded.Stats.Attributes) != 10 {
		t.Fatalf("got %d attributes, want 10 backfilled", len(decoded.Stats.Attributes))
	}
	if decoded.Stats.StatBudget == nil || *decoded.Stats.StatBudget != 40 {
		t.Fatalf("got statBudget %v, want 40 backfilled", decoded.Stats.StatBudget)
	}
}

func TestReconciler_Character_NoStatsAtAll_FullyBackfilled(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus", `{"characterCreation":{"maxStatBudget":35}}`)
	characterID := createCharacter(t, pool, campaignID) // leaves info == "" — no stats at all

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	c, err := repo.Load(context.Background(), characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	var decoded struct {
		Stats struct {
			TraitPoints int            `json:"traitPoints"`
			Traits      []string       `json:"traits"`
			Attributes  map[string]any `json:"attributes"`
			StatBudget  *float64       `json:"statBudget"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(c.Info()), &decoded); err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if decoded.Stats.TraitPoints != 2 {
		t.Fatalf("got traitPoints %d, want 2", decoded.Stats.TraitPoints)
	}
	if len(decoded.Stats.Traits) != 0 {
		t.Fatalf("got traits %v, want none", decoded.Stats.Traits)
	}
	if len(decoded.Stats.Attributes) != 10 {
		t.Fatalf("got %d attributes, want 10", len(decoded.Stats.Attributes))
	}
	if decoded.Stats.StatBudget == nil || *decoded.Stats.StatBudget != 35 {
		t.Fatalf("got statBudget %v, want 35", decoded.Stats.StatBudget)
	}
}

func TestReconciler_HealthyPlatform_NoOp(t *testing.T) {
	pool := newTestPool(t)
	rulesetID := uuid.New()
	campaignID := seedRealCampaign(t, pool, rulesetID, "Timadorus",
		`{"traits":["strong","agile","quick"],"characterCreation":{"maxStatBudget":35}}`)
	characterID := createCharacter(t, pool, campaignID)

	attrs := map[string]any{}
	for _, abbr := range []string{"ST", "AG", "CO", "QU", "SD", "ME", "RE", "EM", "PR", "IN"} {
		attrs[abbr] = map[string]any{"temp": 50, "pot": 50, "bonus": 0}
	}
	healthyInfo, err := json.Marshal(map[string]any{
		"stats": map[string]any{
			"traitPoints": 2,
			"traits":      []string{},
			"attributes":  attrs,
			"statBudget":  35,
		},
	})
	if err != nil {
		t.Fatalf("marshal healthy info: %v", err)
	}
	seedCharacterInfo(t, pool, characterID, string(healthyInfo))

	timadorus.NewReconciler(pool, discardLogger()).SweepOnce(context.Background())

	campaignRegistry := eventsourcing.NewRegistry()
	campaignevents.Register(campaignRegistry)
	campaignStore := postgres.NewStore(pool, campaignRegistry)
	campaignRepo := eventsourcing.NewRepository(campaignStore, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
	campaignAgg, err := campaignRepo.Load(context.Background(), campaignID)
	if err != nil {
		t.Fatalf("load campaign: %v", err)
	}
	if campaignAgg.Version() != 1 {
		t.Fatalf("got campaign version %d, want 1 (sweep must not touch an already-healthy Campaign)", campaignAgg.Version())
	}

	characterRegistry := eventsourcing.NewRegistry()
	events.Register(characterRegistry)
	characterStore := postgres.NewStore(pool, characterRegistry)
	characterRepo := eventsourcing.NewRepository(characterStore, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	characterAgg, err := characterRepo.Load(context.Background(), characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	if characterAgg.Version() != 2 { // 1 for Create, 2 for seedCharacterInfo's own SetInfo — no 3rd
		t.Fatalf("got character version %d, want 2 (sweep must not touch an already-healthy Character)", characterAgg.Version())
	}
}
```

Run: `go test ./internal/engine/timadorus/... -run TestReconciler -v` — expect all five PASS.

- [ ] **Step 3: Wire the Reconciler into `cmd/timadorus-engine/main.go`**

In `run()`, replace:

```go
	cache := timadorusengine.NewRulesetCache()
	processors := []projection.Projector{
		timadorusengine.NewCharacterProcessor(pool, cache, logger),
		timadorusengine.NewCampaignProcessor(pool, cache),
	}
```

with:

```go
	cache := timadorusengine.NewRulesetCache()
	processors := []projection.Projector{
		timadorusengine.NewCharacterProcessor(pool, cache, logger),
		timadorusengine.NewCampaignProcessor(pool, cache),
	}

	reconciler := timadorusengine.NewReconciler(pool, logger)
	go reconciler.Run(ctx, timadorusengine.ReconcilerInterval)
```

Update the "Connection budget" comment above `pgxpool.ParseConfig` — replace:

```go
	// Connection budget: each in-flight Router.Handle call can hold up to 2 pool connections
	// at once — one for the Router's own transaction, and one for the aggregate's Load, which
	// reads via the pool rather than the ambient tx (see postgres.Store.Load). So N processors
	// sharing this one pool can peak at 2N connections, on top of /readyz's own Ping. With the
	// 2 processors registered below that's 4, plus Ping — comfortably under the default of 8.
	// Configurable via TIMADORUS_ENGINE_POOL_MAX_CONNS (internal/config.LoadTimadorusEngine) if
	// a 3rd processor or heavier load ever needs more headroom, with no code change required.
```

with:

```go
	// Connection budget: each in-flight Router.Handle call can hold up to 2 pool connections
	// at once — one for the Router's own transaction, and one for the aggregate's Load, which
	// reads via the pool rather than the ambient tx (see postgres.Store.Load). So N processors
	// sharing this one pool can peak at 2N connections, on top of /readyz's own Ping. With the
	// 2 processors registered below that's 4, plus Ping — comfortably under the default of 8.
	// The Reconciler (below) adds at most 1-2 more, sequentially, only during its own 30s-interval
	// sweep — still comfortably within budget. Configurable via TIMADORUS_ENGINE_POOL_MAX_CONNS
	// (internal/config.LoadTimadorusEngine) if a 3rd processor or heavier load ever needs more
	// headroom, with no code change required.
```

Update the package doc comment at the top of the file — replace:

```go
// timadorus-engine subscribes to the Character and Campaign event streams and reacts to their
// respective trigger events (ActionRequested, ConfigurationRequested, CampaignCreated) — see
// internal/engine/timadorus for the actual logic. Structurally identical to cmd/projector (same
// Router/checkpoint machinery), but registers two processors sharing one RulesetCache instance
```

with:

```go
// timadorus-engine subscribes to the Character and Campaign event streams and reacts to their
// respective trigger events (ActionRequested, ConfigurationRequested, CampaignCreated) — see
// internal/engine/timadorus for the actual logic. Also runs a Reconciler: a completely separate
// periodic background sweep (no NATS/Router/checkpoint involvement) that self-heals any Timadorus
// Campaign/Character missing its default configuration/stats fields — see reconcile.go's own doc
// comment. Structurally identical to cmd/projector (same Router/checkpoint machinery for the two
// event-driven processors), but registers two processors sharing one RulesetCache instance
```

Run: `go build ./... && go vet ./...` — expect clean.

- [ ] **Step 4: Run full regression check**

Run: `go build ./... && go vet ./... && go test -count=1 ./...`
Expected: clean, all tests pass.

- [ ] **Step 5: Commit**

```bash
git add internal/engine/timadorus/reconcile.go \
        internal/engine/timadorus/reconcile_test.go \
        cmd/timadorus-engine/main.go
git commit -m "engine: add a periodic Reconciler to self-heal missing Campaign/Character defaults"
```

---

### Task 3: Real-cluster e2e coverage and BACKLOG update

**Files:**
- Modify: `test/e2e/e2e_test.go`
- Modify: `docs/BACKLOG.md`

**Depends on:** Task 2 (the Reconciler must exist and run in the real binary).

- [ ] **Step 1: Add a new `It` proving the sweep heals a live gap**

Add this `It` to `test/e2e/e2e_test.go`, immediately before the `Describe` block's closing `})`
(i.e. after the last existing `It`):

```go
	It("a Character whose statBudget is missing is healed by the reconciliation sweep", func() {
		var rulesets []querygen.Ruleset
		resp, err := doJSON(http.MethodGet, env.QueryAPIBaseURL+"/rulesets", env.BearerToken, nil, &rulesets)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))

		var timadorusRuleset *querygen.Ruleset
		for i := range rulesets {
			if rulesets[i].Name == "Timadorus" {
				timadorusRuleset = &rulesets[i]
				break
			}
		}
		Expect(timadorusRuleset).NotTo(BeNil(), "expected timadorus-engine to have registered a Ruleset named \"Timadorus\" at startup")

		var user commandgen.UserCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/users", env.BearerToken,
			commandgen.CreateUserRequest{Name: "e2e-reconcile-user"}, &user)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var universe commandgen.UniverseCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/universes", env.BearerToken,
			commandgen.CreateUniverseRequest{Name: "e2e-reconcile-universe", CreatorUserIds: []uuid.UUID{user.Id}}, &universe)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var campaignResp commandgen.CampaignCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/campaigns", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateCampaignRequest{Name: "e2e-reconcile-campaign", RulesetId: timadorusRuleset.Id, GamemasterUserIds: []uuid.UUID{user.Id}}, &campaignResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var characterResp commandgen.CharacterCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/campaigns/%s/characters", env.CommandAPIBaseURL, campaignResp.Id), env.BearerToken,
			commandgen.CreateCharacterRequest{Name: "e2e-reconcile-character", PlayerUserId: user.Id}, &characterResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		// Wait for the engine's normal CharacterCreated seeding to land first, then deliberately
		// overwrite info with a hand-crafted shape that simulates exactly the gap the
		// reconciliation sweep exists to close: stats present (traitPoints/traits/attributes as
		// usual), but statBudget missing.
		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Info).NotTo(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		gappedInfo, err := json.Marshal(map[string]any{
			"stats": map[string]any{
				"traitPoints": 2,
				"traits":      []string{},
				"attributes":  map[string]any{},
			},
		})
		Expect(err).NotTo(HaveOccurred())
		resp, err = doJSON(http.MethodPut, fmt.Sprintf("%s/characters/%s/info", env.CommandAPIBaseURL, characterResp.CharacterId), env.BearerToken,
			commandgen.SetCharacterInfoRequest{Info: string(gappedInfo)}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))

		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var info map[string]any
			g.Expect(json.Unmarshal([]byte(got.Info), &info)).To(Succeed())
			stats, _ := info["stats"].(map[string]any)
			g.Expect(stats["statBudget"]).To(Equal(float64(35)))
		}, time.Minute, time.Second).Should(Succeed())
	})
```

- [ ] **Step 2: Run against a real cluster**

`make dev-up` then `make test-e2e`.
Expected: PASS, alongside every pre-existing `It` in this file.

- [ ] **Step 3: Update `docs/BACKLOG.md`**

Change the URGENT item's leading checkbox and title line from:

```markdown
- [ ] **URGENT — a Character created immediately after its Campaign can permanently miss
  `stats.statBudget`, and the obvious fix (retry via Nack) is unsafe with this codebase's
  checkpoint model.**
```

to:

```markdown
- [x] **Fixed.** `handleCharacterCreated` now reads `characterCreation.maxStatBudget` from the
  write-side Campaign aggregate directly (not `campaigns_read_model`), removing the
  `ConfigurationChanged`-projection hop that made the race easy to lose. A new periodic
  `Reconciler` (`internal/engine/timadorus/reconcile.go`, no NATS/Router/checkpoint involvement —
  see its own doc comment) sweeps every 30 seconds and additively backfills any residual gap,
  including historical ones — this also closes the separate "no backfill for pre-existing
  Campaigns" item below, which the sweep uses the same mechanism to fix. Full history of why the
  two earlier retry-based attempts failed is preserved below for anyone who reaches for that
  approach again.
```

Leave the rest of the entry (the "Two attempts to fix this properly..." and "A real fix needs one
of..." paragraphs) **unchanged** — that history remains valuable context even after the fix lands.

Also mark the separate, now-also-resolved entry (further down the same section) as fixed — change:

```markdown
- [ ] **No backfill for pre-existing Campaigns — default traits only apply going forward.**
```

to:

```markdown
- [x] **Fixed** (same commits as the URGENT statBudget entry above). Reconciler's Campaign pass
  backfills `traits`/`characterCreation.maxStatBudget` onto any Timadorus Campaign missing them,
  regardless of when it was created — additively, never overwriting a GM's own customization.
```

Leave the rest of that entry's body (describing why a checkpoint reset isn't a safe backfill)
unchanged for context.

- [ ] **Step 4: Commit**

```bash
git add test/e2e/e2e_test.go docs/BACKLOG.md
git commit -m "test/e2e, docs: cover the reconciliation sweep against a live cluster, mark BACKLOG items fixed"
```

---

## Final Verification

- `go build ./... && go vet ./... && go test -count=1 ./...` clean.
- Full Go e2e suite clean against a real cluster, including Task 3's new `It`.
- `npm run build` and the full Playwright suite clean (no SPA files touched by this plan, but
  confirm nothing else regressed).
- `docs/BACKLOG.md`'s URGENT item and the "no backfill for pre-existing Campaigns" item are both
  marked `[x] **Fixed**`.
