# Campaign Creation: Default Traits via timadorus-engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** On `CampaignCreated`, for a Campaign using the "timadorus" Ruleset, have
`timadorus-engine`'s `CampaignProcessor` merge `"traits": ["strong", "agile", "loyal"]` into that
Campaign's `configuration`, with the default trait list stored as an editable Go value.

**Architecture:** Extend `CampaignProcessor.Handle` (already subscribed to every Campaign event on
its one JetStream subject, already filtering by event type) to also react to `CampaignCreated`,
alongside its existing `ConfigurationRequested` handling. Both paths funnel through one new shared
"load → parse `configuration` → mutate one key → marshal → save" helper, replacing the
`ConfigurationRequested` path's current inline body. The `CampaignCreated` path resolves its
Ruleset name directly from `rulesets_read_model` via the id already carried in the event payload,
avoiding a real race against the read-model projector that also consumes that same event.

**Tech Stack:** Go, `jackc/pgx/v5`, Watermill (via `internal/bus`/`internal/projection`),
testcontainers-go (Postgres) for integration tests — all already in place, no new dependencies.

## Global Constraints

- Ruleset gate: default traits apply only to Campaigns whose Ruleset name case-insensitively
  matches `targetRulesetName` ("timadorus") — the same gate every other behavior in this package
  uses. Never make this unconditional.
- `defaultTraits` is `var defaultTraits = []string{"strong", "agile", "loyal"}` (Go has no slice
  `const`) in `internal/engine/timadorus/campaign_processor.go`, unexported, declared once, never
  mutated after initialization.
- The merge touches only the `"traits"` top-level key of the `configuration` JSON object — any
  other key already present (notably `"configs"`, from `ConfigurationRequested`) must survive
  untouched, in both directions (traits-then-configs and configs-then-traits).
- No change to `internal/command/campaign`, `internal/domain/campaign`, the OpenAPI specs, or the
  web SPA — this is entirely inside `internal/engine/timadorus`.
- No new migration — `rulesets_read_model` already exists and already has the `name`/`id` columns
  this plan reads.

---

### Task 1: `RulesetCache.resolveByRulesetID` + `CampaignProcessor`'s `CampaignCreated` handling

**Files:**
- Modify: `internal/engine/timadorus/cache.go`
- Modify: `internal/engine/timadorus/campaign_processor.go`
- Test: `internal/engine/timadorus/campaign_processor_test.go`

**Interfaces:**
- Consumes: `events.CampaignCreated{ID, Name, UniverseID, RulesetID, GamemasterUserIDs,
  OccurredAt}` (`internal/domain/campaign/events`, unchanged); `bus.Envelope{GlobalSeq,
  AggregateID, AggregateType, Version, EventType, Payload, Metadata}` and its `CorrelationID()
  string` method (`internal/bus`, unchanged); `campaign.Campaign.Configuration() string` /
  `SetConfiguration(string) error` / `campaign.ErrArchived` (unchanged).
- Produces: `RulesetCache.resolveByRulesetID(ctx, tx, campaignID, rulesetID uuid.UUID) (string,
  error)` — a new unexported method, for Task 1's own use only (no other task needs it).
  `CampaignProcessor.Handle` now also produces a `campaign.configuration_changed.v1` event
  whenever a "timadorus"-ruleset Campaign is created, with `configuration` containing
  `{"traits":["strong","agile","loyal"]}` (merged with any other pre-existing top-level key).

- [ ] **Step 1: Add `resolveByRulesetID` to `RulesetCache`**

In `internal/engine/timadorus/cache.go`, add this method after the existing `resolve` method
(after line 83's closing `}`):

```go
// resolveByRulesetID is resolve's sibling for callers that already know the Ruleset id directly
// (CampaignProcessor's CampaignCreated handling, whose triggering event carries RulesetID) rather
// than only the campaign id. Unlike resolve, this never joins through campaigns_read_model — that
// table's row for a freshly created Campaign is written by a different, independently-racing
// projector consuming the exact same CampaignCreated event this method is called for, with no
// ordering guarantee between the two. Querying rulesets_read_model directly by its own primary
// key sidesteps that race entirely: a Ruleset must already exist (and, in practice, has almost
// certainly been projected already — it was created in an earlier, already-completed request)
// before any Campaign can reference it. The resolved name is cached under campaignID via the same
// map resolve uses, so a call to resolve for the same campaign right after this one hits the
// cache instead of re-running the (racy) join at all.
func (c *RulesetCache) resolveByRulesetID(ctx context.Context, tx pgx.Tx, campaignID, rulesetID uuid.UUID) (string, error) {
	if name, ok := c.get(campaignID); ok {
		return name, nil
	}

	var name string
	err := tx.QueryRow(ctx,
		`SELECT name FROM rulesets_read_model WHERE id = $1`, rulesetID,
	).Scan(&name)
	if err != nil {
		return "", fmt.Errorf(errPrefix+"look up ruleset %s for campaign %s: %w", rulesetID, campaignID, err)
	}

	c.set(campaignID, name)
	return name, nil
}
```

No new imports needed — `context`, `fmt`, `uuid.UUID`, and `pgx.Tx` are already imported in this
file.

No direct unit test is added for `resolveByRulesetID` here: `cache_test.go` (`package timadorus`,
white-box) today only tests `get`/`set` directly — `resolve` itself has no direct unit test either,
since exercising it needs a real `pgx.Tx` against a seeded `campaigns_read_model`/`rulesets_read_model`,
which is what `campaign_processor_test.go`'s testcontainers-backed integration tests already
provide indirectly. `resolveByRulesetID` follows the same precedent: it's exercised end-to-end by
Task 1's own new integration tests (Step 4 below), which is sufficient and consistent with how
`resolve` is covered today.

- [ ] **Step 2: Extract the shared configuration-mutation helper and rewrite `Handle`**

In `internal/engine/timadorus/campaign_processor.go`, replace the entire file's `Handle` and
`appendConfigurationTimestamp` (currently lines 58-127) with:

```go
// targetRulesetName's sibling: the "timadorus" Ruleset's default set of Traits, merged into
// every newly created Campaign that uses that Ruleset (see handleCampaignCreated). Not a `const`
// — Go has no slice constants — but never mutated after initialization; edit this list in place
// to change what new "timadorus" Campaigns start with.
var defaultTraits = []string{"strong", "agile", "loyal"}

func (p *CampaignProcessor) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	switch env.EventType {
	case events.TypeCampaignCreated:
		return p.handleCampaignCreated(ctx, tx, env)
	case events.TypeConfigurationRequested:
		return p.handleConfigurationRequested(ctx, tx, env)
	default:
		return nil
	}
}

// handleCampaignCreated merges defaultTraits into a newly created Campaign's configuration, but
// only if that Campaign uses the "timadorus" Ruleset. Resolves the Ruleset name directly from
// RulesetID (carried in the event's own payload) via RulesetCache.resolveByRulesetID — see that
// method's doc comment for why this path deliberately avoids the campaigns_read_model join
// handleConfigurationRequested still uses.
func (p *CampaignProcessor) handleCampaignCreated(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.CampaignCreated
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf(errPrefix+"unmarshal %s: %w", env.EventType, err)
	}

	rulesetName, err := p.cache.resolveByRulesetID(ctx, tx, e.ID, e.RulesetID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	return p.mutateConfiguration(ctx, tx, env, e.ID, func(config map[string]any) {
		config["traits"] = defaultTraits
	})
}

// handleConfigurationRequested mirrors handleCampaignCreated's shape, appending occurredAt to
// the "configs" array instead of overwriting "traits" — the one difference being this mutation
// is not idempotent under event replay (see mutateConfiguration's own doc comment).
func (p *CampaignProcessor) handleConfigurationRequested(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.ConfigurationRequested
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf(errPrefix+"unmarshal %s: %w", env.EventType, err)
	}

	campaignID := env.AggregateID
	rulesetName, err := p.cache.resolve(ctx, tx, campaignID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	occurredAt := e.OccurredAt
	return p.mutateConfiguration(ctx, tx, env, campaignID, func(config map[string]any) {
		configs, _ := config["configs"].([]any)
		config["configs"] = append(configs, occurredAt.UTC().Format(time.RFC3339Nano))
	})
}

// mutateConfiguration loads campaignID, parses its current Configuration into a generic map
// (starting fresh on any parse failure — the existing configuration isn't necessarily valid JSON
// or even a JSON object at all, since SetConfiguration accepts any string; erroring here would
// get a Campaign permanently stuck instead of self-healing on retry), applies mutate to update
// exactly the key(s) it owns, re-marshals, and saves via SetConfiguration. Shared by
// handleCampaignCreated ("traits", a plain idempotent overwrite) and
// handleConfigurationRequested ("configs", an append that is NOT idempotent under event replay —
// a checkpoint reset would re-append every historical timestamp rather than converge) so each
// only ever touches its own top-level key and never clobbers the other's.
func (p *CampaignProcessor) mutateConfiguration(ctx context.Context, tx pgx.Tx, env bus.Envelope, campaignID uuid.UUID, mutate func(map[string]any)) error {
	txCtx := postgres.WithTx(observability.WithCorrelationID(ctx, env.CorrelationID()), tx)

	c, err := p.campaigns.Load(txCtx, campaignID)
	if err != nil {
		return fmt.Errorf(errPrefix+"load campaign %s: %w", campaignID, err)
	}

	config := map[string]any{}
	if raw := c.Configuration(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &config) // best-effort; config stays {} on failure
	}
	mutate(config)

	newConfig, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf(errPrefix+"marshal updated configuration for campaign %s: %w", campaignID, err)
	}

	if err := c.SetConfiguration(string(newConfig)); err != nil {
		// A Campaign archived between the triggering request and this engine processing the
		// resulting event is a legitimate, expected race.
		if errors.Is(err, campaign.ErrArchived) {
			return nil
		}
		return fmt.Errorf(errPrefix+"set configuration for campaign %s: %w", campaignID, err)
	}
	if err := p.campaigns.Save(txCtx, c); err != nil {
		return fmt.Errorf(errPrefix+"save campaign %s: %w", campaignID, err)
	}
	return nil
}
```

This needs one new import: `"github.com/google/uuid"` (for `campaignID uuid.UUID` in
`mutateConfiguration`'s signature — `e.ID`/`e.RulesetID`/`env.AggregateID` are already `uuid.UUID`
elsewhere in this file, but the type itself wasn't previously named directly in this file's own
code, only used via inference). Add it to the existing `import (...)` block, grouped with the
other third-party imports (alongside `"github.com/jackc/pgx/v5"` and
`"github.com/jackc/pgx/v5/pgxpool"`).

No other lines in this file change — `CampaignProcessorName`, `CampaignProcessor` struct,
`NewCampaignProcessor`, `Name()`, and `Subjects()` (lines 1-56 today) stay exactly as they are.

- [ ] **Step 3: Update the package-level doc comment on `CampaignProcessor`**

Immediately above `type CampaignProcessor struct` in the same file, the existing comment says (in
part) "reacting to Campaign's own ConfigurationRequested instead of Character's ActionRequested."
Update it to also mention `CampaignCreated`:

```go
// CampaignProcessor is CharacterProcessor's sibling: same shape, same rationale for living in
// this binary, reacting to two of Campaign's own events — CampaignCreated (merging default
// traits) and ConfigurationRequested (appending a timestamp) — instead of Character's single
// ActionRequested. Its ruleset lookup for ConfigurationRequested is simpler than
// CharacterProcessor's: a Campaign event's own env.AggregateID already is the campaign id, so no
// characters_read_model hop is needed. CampaignCreated resolves its Ruleset name a third way —
// directly from the event's own RulesetID, via RulesetCache.resolveByRulesetID — since
// campaigns_read_model's row for a brand-new Campaign may not exist yet (see that method's doc
// comment). Not naturally idempotent on replay for the ConfigurationRequested path (the "configs"
// append), for the same reason CharacterProcessor isn't — see that type's doc comment. The
// CampaignCreated path (a plain "traits" overwrite) is naturally idempotent on replay by
// contrast.
```

- [ ] **Step 4: Add the integration tests**

Steps 1-3 above are already applied at this point. In
`internal/engine/timadorus/campaign_processor_test.go`, add these four tests after the existing
`TestCampaignProcessor_PreservesCorrelationID` (end of file):

```go
func TestCampaignProcessor_CampaignCreated_MatchingRuleset_MergesDefaultTraits(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "TIMADORUS") // exact-case mismatch on purpose

	ctx := context.Background()
	repo := campaignRepo(pool)
	c, err := campaign.New(uuid.New(), rulesetID, "Test Campaign", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("campaign.New: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	campaignID := c.AggregateID()
	// Deliberately NOT seeding campaigns_read_model — proves handleCampaignCreated's ruleset
	// lookup does not depend on that table's row existing yet (the real race this plan fixes).

	publish, wait := runCampaignEngine(t, pool)

	occurredAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       1,
		EventType:     events.TypeCampaignCreated,
		Payload: mustMarshal(t, events.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: c.UniverseID(), RulesetID: rulesetID,
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: occurredAt,
		}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&payload)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query configuration_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if payload == nil {
		t.Fatal("timed out waiting for a campaign.configuration_changed.v1 event")
	}

	var configEvent struct {
		Configuration string `json:"configuration"`
	}
	if err := json.Unmarshal(payload, &configEvent); err != nil {
		t.Fatalf("configuration_changed payload %s is not the expected shape: %v", payload, err)
	}

	var decoded struct {
		Traits []string `json:"traits"`
	}
	if err := json.Unmarshal([]byte(configEvent.Configuration), &decoded); err != nil {
		t.Fatalf("configuration %q is not the expected shape: %v", configEvent.Configuration, err)
	}
	want := []string{"strong", "agile", "loyal"}
	if len(decoded.Traits) != len(want) {
		t.Fatalf("got traits %v, want %v", decoded.Traits, want)
	}
	for i := range want {
		if decoded.Traits[i] != want[i] {
			t.Fatalf("got traits %v, want %v", decoded.Traits, want)
		}
	}

	wait()
}

func TestCampaignProcessor_CampaignCreated_NonMatchingRuleset_NoOp(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "SomethingElse")

	ctx := context.Background()
	repo := campaignRepo(pool)
	c, err := campaign.New(uuid.New(), rulesetID, "Test Campaign", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("campaign.New: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save campaign: %v", err)
	}
	campaignID := c.AggregateID()

	publish, wait := runCampaignEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       1,
		EventType:     events.TypeCampaignCreated,
		Payload: mustMarshal(t, events.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: c.UniverseID(), RulesetID: rulesetID,
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC(),
		}),
	})

	time.Sleep(500 * time.Millisecond)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		campaignID, events.TypeConfigurationChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count configuration_changed events: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d campaign.configuration_changed.v1 events, want 0 (ruleset doesn't match)", count)
	}

	wait()
}

func TestCampaignProcessor_TraitsAndConfigsCoexist(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID) // seeds campaigns_read_model too, fine here

	publish, wait := runCampaignEngine(t, pool)

	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 1,
		EventType: events.TypeCampaignCreated,
		Payload: mustMarshal(t, events.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: uuid.New(), RulesetID: rulesetID,
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: createdAt,
		}),
	})

	// Wait for the traits-driven ConfigurationChanged before publishing the second event, so the
	// two don't race each other for who loads/saves the aggregate first.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&count); err != nil {
			t.Fatalf("count configuration_changed events: %v", err)
		} else if count == 1 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	occurredAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq: 2, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeConfigurationRequested,
		Payload:   mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: occurredAt}),
	})

	deadline = time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&payload)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query configuration_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if payload == nil {
		t.Fatal("timed out waiting for the second campaign.configuration_changed.v1 event")
	}

	var configEvent struct {
		Configuration string `json:"configuration"`
	}
	if err := json.Unmarshal(payload, &configEvent); err != nil {
		t.Fatalf("configuration_changed payload %s is not the expected shape: %v", payload, err)
	}

	var decoded struct {
		Traits  []string `json:"traits"`
		Configs []string `json:"configs"`
	}
	if err := json.Unmarshal([]byte(configEvent.Configuration), &decoded); err != nil {
		t.Fatalf("configuration %q is not the expected shape: %v", configEvent.Configuration, err)
	}
	wantTraits := []string{"strong", "agile", "loyal"}
	if len(decoded.Traits) != len(wantTraits) {
		t.Fatalf("got traits %v, want %v (traits should survive the configs append)", decoded.Traits, wantTraits)
	}
	for i := range wantTraits {
		if decoded.Traits[i] != wantTraits[i] {
			t.Fatalf("got traits %v, want %v (traits should survive the configs append)", decoded.Traits, wantTraits)
		}
	}
	if len(decoded.Configs) != 1 || decoded.Configs[0] != occurredAt.Format(time.RFC3339Nano) {
		t.Fatalf("got configs %v, want [%q]", decoded.Configs, occurredAt.Format(time.RFC3339Nano))
	}

	wait()
}

func TestCampaignProcessor_CampaignCreated_ArchivedCampaign_NoOp(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)
	archiveCampaign(t, pool, campaignID)

	publish, wait := runCampaignEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 1,
		EventType: events.TypeCampaignCreated,
		Payload: mustMarshal(t, events.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: uuid.New(), RulesetID: rulesetID,
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC(),
		}),
	})

	time.Sleep(500 * time.Millisecond)

	var configChangedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		campaignID, events.TypeConfigurationChanged,
	).Scan(&configChangedCount); err != nil {
		t.Fatalf("count configuration_changed events: %v", err)
	}
	if configChangedCount != 0 {
		t.Fatalf("got %d campaign.configuration_changed.v1 events, want 0 (campaign is archived)", configChangedCount)
	}

	var deadLetterCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM projection_dead_letters WHERE aggregate_id = $1`,
		campaignID,
	).Scan(&deadLetterCount); err != nil {
		t.Fatalf("count dead letters: %v", err)
	}
	if deadLetterCount != 0 {
		t.Fatalf("got %d dead-lettered events, want 0 (archived Campaign should be a clean no-op)", deadLetterCount)
	}

	wait()
}
```

No new imports needed in this test file — `campaign`, `events`, `bus`, `uuid`, `pgx`, `time`,
`json`, `errors` are all already imported.

- [ ] **Step 5: Run the full package test suite**

Run: `go test ./internal/engine/timadorus/... -v`
Expected: every test in the package passes, including all four new
`TestCampaignProcessor_CampaignCreated_*`/`TestCampaignProcessor_TraitsAndConfigsCoexist` tests
and every pre-existing test (`TestCampaignProcessor_MatchingRuleset_AppendsTimestamp`,
`TestCampaignProcessor_NonMatchingRuleset_NoOp`, `TestCampaignProcessor_ArchivedCampaign_NoOp`,
`TestCampaignProcessor_PreservesCorrelationID`, `TestCharacterProcessor_*`, `TestRegisterRuleset_*`,
`TestRulesetCache_*`) unaffected by this refactor.

- [ ] **Step 6: Repo-wide verification**

Run: `go build ./... && go vet ./... && go test ./...` (from repo root)
Expected: all clean — this plan touches only `internal/engine/timadorus`, but confirm no other
package imports anything from this package in a way that could break (it shouldn't; nothing in
this plan changes any exported symbol's signature except adding the new unexported
`resolveByRulesetID`/`handleCampaignCreated`/`handleConfigurationRequested`/`mutateConfiguration`,
none of which are exported).

- [ ] **Step 7: Commit**

```bash
git add internal/engine/timadorus/cache.go internal/engine/timadorus/campaign_processor.go internal/engine/timadorus/campaign_processor_test.go
git commit -m "timadorus-engine: merge default traits into a new Campaign's configuration

On CampaignCreated, for a Campaign using the timadorus Ruleset, merge
{\"traits\":[\"strong\",\"agile\",\"loyal\"]} into its configuration, alongside the existing
ConfigurationRequested-triggered \"configs\" timestamp append. Resolves the Ruleset name directly
from the event's own RulesetID (via a new RulesetCache.resolveByRulesetID) rather than joining
through campaigns_read_model, avoiding a real race against that table's own, independently-racing
projector for the same event."
```

---

## Final Verification

- `go build ./... && go vet ./... && go test ./...` (repo root) all clean.
- `go test ./internal/engine/timadorus/... -v` shows every test passing, old and new.
- Read the final `internal/engine/timadorus/campaign_processor.go` and confirm: `Handle` has no
  other event-type branch than the two switch cases plus `default: return nil`; `defaultTraits` is
  declared exactly once, unexported, never reassigned anywhere in the file.
- Grep `internal/engine/timadorus` for `campaigns_read_model` and confirm the only remaining
  reference is inside `RulesetCache.resolve` (the `ConfigurationRequested` path) — `resolveByRulesetID`
  must not reference it.
