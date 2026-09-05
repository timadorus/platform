# Character Traits Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn Character's cosmetic, hardcoded "Traits" display into a real, server-validated,
actively-managed list: seeded with a trait-point budget at Character creation, editable from the
SPA via an "Add Trait" control that spends one point per trait, picking only from the traits the
Character's own Campaign already defines — with the engine, not the SPA, enforcing eligibility and
logging every rejection.

**Architecture:** Reuses the existing `info`/`action` trigger mechanism end to end (the same
mechanism `CampaignProcessor`'s `configuration`/`configure` work already mirrored once this
session) — no new event type, no new command endpoint. `CharacterProcessor` gains a real
`CharacterCreated` handler for the first time (today it's a pure no-op) and an `addTrait` dispatch
on its existing `ActionRequested` handling.

**Tech Stack:** Go, `pgx`, `oapi-codegen`/`openapi-typescript`, Vue 3 `<script setup>`, Playwright,
`log/slog`.

## Global Constraints

- Scope: "Timadorus" Ruleset only (case-insensitive), matching every other `timadorus-engine`
  default-seeding and action-dispatch precedent in this codebase.
- No new event type, no new command endpoint, no new OpenAPI path. The only OpenAPI change is
  loosening `requestCharacterAction`'s existing request-body schema (identical to the earlier,
  already-merged fix for `requestCampaignConfiguration`).
- The pre-existing `ActionRequested` timestamp-append fallback for a non-matching payload
  (including the literal `{}` used by existing tests) must be completely unchanged.
- **The engine is the source of truth for whether a trait can be added — never the SPA.** A
  recognized `addTrait` payload is validated against the Character's own Campaign's configured
  `traits` list, current `traitPoints`, and the Character's own existing traits, before being
  applied. It never falls through to the timestamp-append fallback once recognized, whether
  accepted or rejected.
- **Every rejected `addTrait` is logged** (`p.logger.Warn`, naming the character id, campaign id,
  trait, and specific rejection reason) — never a silent no-op.
- `go build ./... && go vet ./... && go test -count=1 ./...` must stay clean after every task;
  `npm run build` and the full Playwright suite must stay clean after every SPA-touching task.
- Both `api/command/gen/server.gen.go` and `web/src/api/command.types.ts` must be regenerated from
  the one OpenAPI schema change — verify both, not just one.

---

### Task 1: `timadorus-engine` — seed trait data and validate/apply `addTrait`

**Files:**
- Modify: `internal/engine/timadorus/character_processor.go`
- Modify: `cmd/timadorus-engine/main.go`
- Modify: `internal/engine/timadorus/character_processor_test.go`
- Modify: `internal/engine/timadorus/shared_cache_test.go`

**Interfaces:**
- Produces: `NewCharacterProcessor(pool *pgxpool.Pool, cache *RulesetCache, logger *slog.Logger) *CharacterProcessor` — signature change, breaks every existing call site (4 total, all listed below).

- [ ] **Step 1: Update every `NewCharacterProcessor` call site's signature first**

Before writing any new logic, thread a `*slog.Logger` through the constructor so every existing
call site compiles against the new signature (do this first so Step 2's new tests can compile):

`internal/engine/timadorus/character_processor.go`:
```go
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
```

```go
type CharacterProcessor struct {
	characters *eventsourcing.Repository[*character.Character]
	cache      *RulesetCache
	logger     *slog.Logger
}

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
```

`cmd/timadorus-engine/main.go:97`: change
`timadorusengine.NewCharacterProcessor(pool, cache),` to
`timadorusengine.NewCharacterProcessor(pool, cache, logger),` — the `logger` local variable
already exists in `run()`'s scope.

`internal/engine/timadorus/shared_cache_test.go` (two call sites, lines 38 and 132): change
`timadorus.NewCharacterProcessor(pool, cache)` to
`timadorus.NewCharacterProcessor(pool, cache, discardLogger())` at both.

`internal/engine/timadorus/character_processor_test.go`'s `runEngine` helper (line 112): change
`timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache())` to
`timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache(), discardLogger())`.

Run: `go build ./... && go vet ./...` — expect clean (no new behavior yet, just the signature
threaded through).

- [ ] **Step 2: Write the failing tests**

Add to `internal/engine/timadorus/character_processor_test.go`. First, a helper to seed a
Campaign's `configuration` with a real trait list (extend `seedCampaignAndRuleset`'s SQL to accept
one, or add a small new helper — use your judgment on the cleanest way to avoid changing every
existing call site's argument list; a new `seedCampaignConfiguration(t, pool, campaignID, json
string)` helper doing a plain `UPDATE campaigns_read_model SET configuration = $2 WHERE id = $1`
is simplest and touches no existing test):

```go
func seedCampaignConfiguration(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID, configuration string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE campaigns_read_model SET configuration = $2 WHERE id = $1`, campaignID, configuration,
	); err != nil {
		t.Fatalf("seed campaign configuration: %v", err)
	}
}
```

A helper to capture log output for the rejection tests:

```go
func newTestLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}
```

(Add `"bytes"` and `"log/slog"` to this file's imports.)

Then the new tests:

```go
func TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStats(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "TIMADORUS") // exact-case mismatch on purpose
	characterID := uuid.New()

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	c, err := character.New(campaignID, uuid.New(), uuid.New(), "Elminster")
	if err != nil {
		t.Fatalf("character.New: %v", err)
	}
	characterID = c.AggregateID()
	if err := repo.Save(context.Background(), c); err != nil {
		t.Fatalf("save character: %v", err)
	}

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       1,
		EventType:     events.TypeCharacterCreated,
		Payload: mustMarshal(t, events.CharacterCreated{
			ID: characterID, Name: "Elminster", CampaignID: campaignID, EntityID: uuid.New(),
			PlayerUserID: uuid.New(), OccurredAt: time.Now().UTC(),
		}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			characterID, events.TypeInfoChanged,
		).Scan(&payload)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query info_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if payload == nil {
		t.Fatal("timed out waiting for a character.info_changed.v1 event")
	}

	var infoEvent struct {
		Info string `json:"info"`
	}
	if err := json.Unmarshal(payload, &infoEvent); err != nil {
		t.Fatalf("info_changed payload %s is not the expected shape: %v", payload, err)
	}
	var decoded struct {
		Stats struct {
			TraitPoints int      `json:"traitPoints"`
			Traits      []string `json:"traits"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(infoEvent.Info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", infoEvent.Info, err)
	}
	if decoded.Stats.TraitPoints != 2 {
		t.Fatalf("got traitPoints %d, want 2", decoded.Stats.TraitPoints)
	}
	if len(decoded.Stats.Traits) != 0 {
		t.Fatalf("got traits %v, want none", decoded.Stats.Traits)
	}

	wait()
}

func TestCharacterProcessor_CharacterCreated_NonMatchingRuleset_NoOp(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "SomethingElse")

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	c, err := character.New(campaignID, uuid.New(), uuid.New(), "Elminster")
	if err != nil {
		t.Fatalf("character.New: %v", err)
	}
	characterID := c.AggregateID()
	if err := repo.Save(context.Background(), c); err != nil {
		t.Fatalf("save character: %v", err)
	}

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 1,
		EventType: events.TypeCharacterCreated,
		Payload: mustMarshal(t, events.CharacterCreated{
			ID: characterID, Name: "Elminster", CampaignID: campaignID, EntityID: uuid.New(),
			PlayerUserID: uuid.New(), OccurredAt: time.Now().UTC(),
		}),
	})

	time.Sleep(500 * time.Millisecond)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d character.info_changed.v1 events, want 0 (ruleset doesn't match)", count)
	}

	wait()
}

func TestCharacterProcessor_AddTrait_ValidTrait_Succeeds(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	seedCampaignConfiguration(t, pool, campaignID, `{"traits":["strong","agile","quick"]}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `{"stats":{"traitPoints":2,"traits":[]}}`)

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload: mustMarshal(t, events.ActionRequested{
			Payload: `{"action":"addTrait","trait":"strong"}`, OccurredAt: time.Now().UTC(),
		}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var payload []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT payload FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			characterID, events.TypeInfoChanged,
		).Scan(&payload)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query info_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if payload == nil {
		t.Fatal("timed out waiting for a character.info_changed.v1 event")
	}

	var infoEvent struct {
		Info string `json:"info"`
	}
	if err := json.Unmarshal(payload, &infoEvent); err != nil {
		t.Fatalf("info_changed payload %s is not the expected shape: %v", payload, err)
	}
	var decoded struct {
		Stats struct {
			TraitPoints int      `json:"traitPoints"`
			Traits      []string `json:"traits"`
		} `json:"stats"`
	}
	if err := json.Unmarshal([]byte(infoEvent.Info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", infoEvent.Info, err)
	}
	if decoded.Stats.TraitPoints != 1 {
		t.Fatalf("got traitPoints %d, want 1", decoded.Stats.TraitPoints)
	}
	if len(decoded.Stats.Traits) != 1 || decoded.Stats.Traits[0] != "strong" {
		t.Fatalf("got traits %v, want [strong]", decoded.Stats.Traits)
	}

	wait()
}

func TestCharacterProcessor_AddTrait_NotACampaignTrait_RejectedAndLogged(t *testing.T) {
	pool := newTestPool(t)
	logger, logs := newTestLogger()

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	seedCampaignConfiguration(t, pool, campaignID, `{"traits":["strong","agile","quick"]}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `{"stats":{"traitPoints":2,"traits":[]}}`)

	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	body := mustMarshal(t, bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload: mustMarshal(t, events.ActionRequested{
			Payload: `{"action":"addTrait","trait":"not-a-real-trait"}`, OccurredAt: time.Now().UTC(),
		}),
	})
	msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
	if err := inMemory.Publish(bus.Subject(events.AggregateType), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d character.info_changed.v1 events, want 0 (trait is not in the Campaign's list)", count)
	}
	if !strings.Contains(logs.String(), "not-a-real-trait") {
		t.Fatalf("expected a log line naming the rejected trait, got: %s", logs.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func TestCharacterProcessor_AddTrait_NoPointsRemaining_RejectedAndLogged(t *testing.T) {
	pool := newTestPool(t)
	logger, logs := newTestLogger()

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	seedCampaignConfiguration(t, pool, campaignID, `{"traits":["strong","agile","quick"]}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `{"stats":{"traitPoints":0,"traits":[]}}`)

	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	body := mustMarshal(t, bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload: mustMarshal(t, events.ActionRequested{
			Payload: `{"action":"addTrait","trait":"strong"}`, OccurredAt: time.Now().UTC(),
		}),
	})
	msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
	if err := inMemory.Publish(bus.Subject(events.AggregateType), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d character.info_changed.v1 events, want 0 (no traitPoints remaining)", count)
	}
	if !strings.Contains(logs.String(), "traitPoints") {
		t.Fatalf("expected a log line naming the rejection reason, got: %s", logs.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func TestCharacterProcessor_AddTrait_AlreadyHasTrait_RejectedAndLogged(t *testing.T) {
	pool := newTestPool(t)
	logger, logs := newTestLogger()

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	seedCampaignConfiguration(t, pool, campaignID, `{"traits":["strong","agile","quick"]}`)
	characterID := createCharacter(t, pool, campaignID)
	seedCharacterInfo(t, pool, characterID, `{"stats":{"traitPoints":2,"traits":["strong"]}}`)

	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache(), logger)
	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	body := mustMarshal(t, bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeActionRequested,
		Payload: mustMarshal(t, events.ActionRequested{
			Payload: `{"action":"addTrait","trait":"strong"}`, OccurredAt: time.Now().UTC(),
		}),
	})
	msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
	if err := inMemory.Publish(bus.Subject(events.AggregateType), msg); err != nil {
		t.Fatalf("publish: %v", err)
	}

	time.Sleep(500 * time.Millisecond)

	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&count); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d character.info_changed.v1 events, want 0 (already has this trait)", count)
	}
	if !strings.Contains(logs.String(), "already has") {
		t.Fatalf("expected a log line naming the rejection reason, got: %s", logs.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}
```

Add this small helper alongside `createCharacter`/`archiveCharacter` (a direct read-model seed,
matching `seedCampaignConfiguration`'s own shape — the Processor never reads `info` from
`characters_read_model` itself, but tests need a way to seed a Character's starting `info` without
going through a full `ActionRequested` round trip first):

```go
func seedCharacterInfo(t *testing.T, pool *pgxpool.Pool, characterID uuid.UUID, info string) {
	t.Helper()
	// Loads the real aggregate and calls SetInfo/Save through the event store — the Processor's
	// own p.characters.Load reads through postgres.Store, not characters_read_model, so seeding
	// characters_read_model alone (as createCharacter's own INSERT does) would not be visible to
	// tryAddTrait's/mutateInfo's Load call.
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})
	ctx := context.Background()
	c, err := repo.Load(ctx, characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	if err := c.SetInfo(info); err != nil {
		t.Fatalf("set info: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save character info: %v", err)
	}
}
```

- [ ] **Step 3: Run to verify they fail**

Run: `go test ./internal/engine/timadorus/... -run 'TestCharacterProcessor_(CharacterCreated|AddTrait)' -v`
Expected: every new test FAILs — `CharacterCreated` is currently a no-op (never raises
`InfoChanged`), and there is no `addTrait` dispatch at all yet (any `ActionRequested` payload
currently just appends a timestamp, regardless of shape).

- [ ] **Step 4: Implement**

Replace `character_processor.go`'s body (keeping the imports/constructor from Step 1) with:

```go
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
```

Also update the `CharacterProcessor` type's own doc comment (currently describes exactly one
effect, `ActionRequested`'s timestamp append) to mention the new `CharacterCreated` handling and
the `addTrait` dispatch.

- [ ] **Step 5: Run to verify they pass**

Run: `go test ./internal/engine/timadorus/... -v`
Expected: PASS, all tests in the package including every pre-existing one (no regressions —
especially `TestCharacterProcessor_MatchingRuleset_AppendsTimestamp` and
`TestSharedRulesetCache_ServesBothProcessors`/`TestSharedRulesetCache_ConcurrentAccess`, both of
which construct a `CharacterProcessor` directly).

- [ ] **Step 6: Run full regression check**

Run: `go build ./... && go vet ./... && go test -count=1 ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/timadorus/character_processor.go internal/engine/timadorus/character_processor_test.go internal/engine/timadorus/shared_cache_test.go cmd/timadorus-engine/main.go
git commit -m "engine: seed Character trait points/traits on creation, validate+log addTrait"
```

---

### Task 2: Loosen `requestCharacterAction`'s request body and regenerate both clients

**Files:**
- Modify: `api/command/openapi.yaml`
- Regenerate: `api/command/gen/server.gen.go`
- Regenerate: `web/src/api/command.types.ts`

**Interfaces:**
- Produces: `command.types.ts`'s `requestCharacterAction.requestBody.content["application/json"]`
  changes from `Record<string, never>` to `Record<string, unknown>` — Task 3's `useCharacters.ts`
  code depends on this to compile without a type assertion.

- [ ] **Step 1: Edit the schema**

In `api/command/openapi.yaml`, find the `/characters/{characterId}/action` path's
`put.requestBody` and change:

```yaml
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
```

to:

```yaml
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              additionalProperties: true
```

- [ ] **Step 2: Regenerate the Go server**

Run: `go generate ./api/...`
Expected: `git diff api/command/gen/server.gen.go` shows no change, or at most the same
alias-to-named-type formatting shift already seen (and confirmed harmless) for
`requestCampaignConfiguration`'s identical earlier fix.

- [ ] **Step 3: Regenerate the TypeScript client**

From `web/`, run: `npm run generate`
Expected: `git diff web/src/api/command.types.ts` shows exactly one change —
`requestCharacterAction`'s request body content type changes from `Record<string, never>` to
`Record<string, unknown>`. No other type in the file changes.

- [ ] **Step 4: Verify**

Run: `go build ./... && go vet ./...` and, from `web/`, `npm run build`. Both clean.

- [ ] **Step 5: Commit**

```bash
git add api/command/openapi.yaml api/command/gen/server.gen.go web/src/api/command.types.ts
git commit -m "api/command: loosen requestCharacterAction's body to a genuinely free-form object"
```

---

### Task 3: SPA — dynamic traits, "Add Trait", and the Info tab rename

**Files:**
- Modify: `web/src/composables/useCharacters.ts`
- Modify: `web/src/views/CharacterDetailView.vue`
- Modify: `web/src/components/character/CharacterConfigurationPanel.vue`
- Modify: `web/src/components/character/BaseInfoTable.vue`

**Interfaces:**
- Consumes: `command.types.ts`'s loosened `requestCharacterAction` body type (Task 2).
- Produces: `useCharacters().requestAction(id: string, payload: Record<string, unknown>): Promise<void>`.
- `BaseInfoTable.vue` gains new required props: `characterId: string`, `traits: string[]`,
  `traitPoints: number`, `availableTraits: string[]`. No new emit — `BaseInfoTable.vue` calls
  `requestAction` directly (see Step 4's rationale), matching `ConfigurationPanel.vue`'s own
  precedent of calling `useCampaigns().requestConfiguration` directly rather than emitting to its
  parent, rather than the (less apt, for this fire-and-forget-with-local-pending-state shape)
  emit pattern `submit-rename`/`submit-reassign-player` use in this same file for synchronous,
  parent-owned mutations.

- [ ] **Step 1: Add `requestAction` to `useCharacters.ts`**

```ts
async function requestAction(id: string, payload: Record<string, unknown>): Promise<void> {
  const { error: apiError } = await getCommandClient().PUT('/characters/{characterId}/action', {
    params: { path: { characterId: id } },
    body: payload,
  })
  if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to request action.')
}
```

Add `requestAction` to the object returned at the end of `useCharacters()`.

- [ ] **Step 2: Rename the tab and the Info panel's heading**

In `CharacterDetailView.vue`, change:
```ts
const tabs = ['Stats', 'Skills', 'Equipment', 'Journal', 'Configuration']
```
to:
```ts
const tabs = ['Stats', 'Skills', 'Equipment', 'Journal', 'Info']
```
and its template's:
```vue
    <CharacterConfigurationPanel v-else-if="activeTab === 'Configuration'" :key="character.id" :info="character.info" />
```
to:
```vue
    <CharacterConfigurationPanel v-else-if="activeTab === 'Info'" :key="character.id" :info="character.info" />
```

In `CharacterConfigurationPanel.vue`, change the heading `<h2 ...>Configuration</h2>` to
`<h2 ...>Info</h2>` (component file name, props, and doc comments referring to the underlying
`info` field are unchanged — only the visible heading text, matching the renamed tab).

- [ ] **Step 3: `CharacterDetailView.vue` — silent reload, load the Campaign, parse traits**

Add the `silent` option to `load()`, exactly mirroring `CampaignOverviewPanel.vue`'s own fix:

```ts
const { get, rename, archive, setPlayer, waitForCharacter } = useCharacters()
const { get: getCampaign } = useCampaigns()
```

(Add the `useCampaigns` import: `import { useCampaigns, type CampaignSummary } from '@/composables/useCampaigns'`.)

```ts
const campaign = ref<CampaignSummary | null>(null)
```

```ts
// silent: true for a background reload triggered by the change-feed (see the lastAggregateChange
// watch below) — must NOT toggle the full-page loading state, since the template's
// `v-if="character"` gate would otherwise unmount the entire page (including BaseInfoTable's own
// pending-add-trait state) on every unrelated background change. A genuine character switch (the
// watch further down) stays non-silent. Mirrors CampaignOverviewPanel.vue's identical fix.
async function load(opts: { silent?: boolean } = {}) {
  loadController?.abort()
  const controller = new AbortController()
  loadController = controller
  loadTimedOut.value = false
  if (!opts.silent) character.value = null

  const found = await waitForCharacter(characterId.value, { signal: controller.signal })
  if (controller.signal.aborted) return
  await listUsers()
  if (controller.signal.aborted) return
  if (found) {
    character.value = found
    campaign.value = await getCampaign(found.campaignId)
    if (controller.signal.aborted) return
  } else if (!opts.silent) {
    loadTimedOut.value = true
  }
}
onMounted(() => load())
watch(characterId, () => load())
onUnmounted(() => loadController?.abort())

const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'character' && change.aggregateId.toLowerCase() === characterId.value.toLowerCase()) {
      load({ silent: true })
    }
  })
}
```

(Both `onMounted`/`watch(characterId, ...)` call sites wrapped in arrow functions, for the same
reason `CampaignOverviewPanel.vue`'s own fix does this — passed directly, `watch`'s callback
signature would shadow `opts`.)

Note the change from the original `character.value = null` (unconditional) to
`if (!opts.silent) character.value = null`: on a silent reload, `character.value` stays whatever
it currently is until `found`/`waitForCharacter` resolves, so the page never flashes to the
"Loading…"/"Campaign not found" branches for a background refresh. This mirrors
`CampaignOverviewPanel.vue`'s own `if (!opts.silent) loading.value = true/false` shape, adapted to
this file's own "the loaded value itself is the loading gate" structure (this file has no separate
`loading` ref — `v-if="character"` already does double duty).

- [ ] **Step 4: Parse traits, pass new props to `BaseInfoTable`**

```ts
const traits = computed<string[]>(() => {
  try {
    return JSON.parse(character.value?.info || '{}')?.stats?.traits ?? []
  } catch {
    return []
  }
})
const traitPoints = computed<number>(() => {
  try {
    return JSON.parse(character.value?.info || '{}')?.stats?.traitPoints ?? 0
  } catch {
    return 0
  }
})
const availableTraits = computed<string[]>(() => {
  let campaignTraits: string[] = []
  try {
    campaignTraits = JSON.parse(campaign.value?.configuration || '{}')?.traits ?? []
  } catch {
    campaignTraits = []
  }
  return campaignTraits.filter((t: string) => !traits.value.includes(t))
})
```

Update `<BaseInfoTable>`'s call site:
```vue
      <BaseInfoTable
        :key="character.id"
        class="flex-1"
        :character-id="character.id"
        :name="character.name"
        :player-name="playerName"
        :traits="traits"
        :trait-points="traitPoints"
        :available-traits="availableTraits"
        @submit-rename="onSubmitRename"
        @submit-reassign-player="onSubmitReassignPlayer"
        @archive="showArchiveConfirm = true"
      />
```

No new emit handler needed — `submit-add-trait` is deliberately not introduced (see this task's
Interfaces note).

- [ ] **Step 5: `BaseInfoTable.vue` — dynamic traits row and the Add Trait control**

```vue
<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import BaseButton from '@/components/common/BaseButton.vue'
import UserPicker from '@/components/pickers/UserPicker.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import { useCharacters } from '@/composables/useCharacters'

const props = defineProps<{
  characterId: string
  name: string
  playerName: string
  traits: string[]
  traitPoints: number
  availableTraits: string[]
}>()
const emit = defineEmits<{
  'submit-rename': [name: string]
  'submit-reassign-player': [userId: string]
  archive: []
}>()
const { requestAction } = useCharacters()

// ... existing editingName/startEditName/saveName/watch(props.name, ...) unchanged ...

// ... existing editingPlayer/onSelectPlayer unchanged ...

const showAddTrait = ref(false)
const traitToAdd = ref('')
type AddTraitStatus = 'idle' | 'pending' | 'error'
const addTraitStatus = ref<AddTraitStatus>('idle')
const addTraitError = ref<string | null>(null)
// The trait most recently submitted, so the watch below can tell "the loaded traits now include
// my own request" apart from "someone else changed something unrelated".
let pendingTrait: string | null = null

// A rejected addTrait (not a Campaign trait / no points left / already has it) is a clean, logged
// engine-side no-op — no ActionChanged-equivalent event distinguishes "rejected" from "still
// processing," so this timeout is what guarantees the control always resolves, exactly like
// ConfigurationPanel.vue's identical PENDING_TIMEOUT_MS for Max Stat Budget.
const ADD_TRAIT_TIMEOUT_MS = 10000
let addTraitTimeoutHandle: ReturnType<typeof setTimeout> | null = null
function clearAddTraitTimeout() {
  if (addTraitTimeoutHandle) {
    clearTimeout(addTraitTimeoutHandle)
    addTraitTimeoutHandle = null
  }
}

async function submitAddTrait() {
  if (!traitToAdd.value) return
  const trait = traitToAdd.value
  addTraitStatus.value = 'pending'
  addTraitError.value = null
  pendingTrait = trait
  showAddTrait.value = false
  traitToAdd.value = ''
  clearAddTraitTimeout()
  addTraitTimeoutHandle = setTimeout(() => {
    if (addTraitStatus.value === 'pending') {
      addTraitStatus.value = 'error'
      addTraitError.value = 'No confirmation received — this trait may not have been added.'
      pendingTrait = null
    }
  }, ADD_TRAIT_TIMEOUT_MS)
  try {
    await requestAction(props.characterId, { action: 'addTrait', trait })
  } catch (err) {
    clearAddTraitTimeout()
    addTraitStatus.value = 'error'
    addTraitError.value = err instanceof Error ? err.message : 'Failed to request adding this trait.'
  }
}

watch(
  () => props.traits,
  (traits) => {
    if (addTraitStatus.value === 'pending' && pendingTrait !== null && traits.includes(pendingTrait)) {
      clearAddTraitTimeout()
      addTraitStatus.value = 'idle'
      pendingTrait = null
    }
  },
)

onUnmounted(clearAddTraitTimeout)
</script>
```

Template's Traits row:
```vue
        <tr class="border-b border-slate-50">
          <td class="py-1.5 pr-3 font-medium text-slate-500">Traits</td>
          <td class="py-1.5" colspan="2">
            <div class="flex flex-wrap items-center gap-2">
              <span>{{ traits.length > 0 ? traits.join(', ') : '(no traits selected)' }}</span>
              <template v-if="traitPoints > 0">
                <button v-if="!showAddTrait" class="text-xs text-indigo-600 hover:underline" @click="showAddTrait = true">
                  Add Trait
                </button>
                <template v-else>
                  <select v-model="traitToAdd" class="rounded-md border border-slate-300 px-2 py-1 text-xs">
                    <option value="" disabled>Select a trait…</option>
                    <option v-for="t in availableTraits" :key="t" :value="t">{{ t }}</option>
                  </select>
                  <button class="text-xs text-indigo-600 hover:underline" :disabled="!traitToAdd" @click="submitAddTrait">Add</button>
                  <button class="text-xs text-slate-400 hover:underline" @click="showAddTrait = false">Cancel</button>
                </template>
              </template>
              <span v-if="addTraitStatus === 'pending'" class="text-xs text-slate-400">Update requested — refreshing…</span>
            </div>
            <ErrorBanner v-if="addTraitStatus === 'error'" :message="addTraitError" @dismiss="addTraitStatus = 'idle'" />
          </td>
        </tr>
```

- [ ] **Step 6: Verify**

From `web/`, run `npm run build`. Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add web/src/composables/useCharacters.ts web/src/views/CharacterDetailView.vue web/src/components/character/CharacterConfigurationPanel.vue web/src/components/character/BaseInfoTable.vue
git commit -m "web: turn Character traits into an actively-managed, engine-validated list"
```

---

### Task 4: e2e coverage for the SPA flow

**Files:**
- Modify: `web/e2e/support/mockBackend.ts`
- Create: `web/e2e/character-traits.spec.ts`

**Interfaces:**
- Consumes: `MockState.campaigns`/`MockState.characters` (both already have `configuration?`/
  `info?` fields), `MockState.changes`.

This cannot exercise the real engine's validation logic (that's Task 1's Go tests) — it proves the
SPA's own flow: the traits list renders from `info`, the empty-state message shows, the Add Trait
control only appears when eligible, submitting it calls the right endpoint with the right payload,
shows the pending status, and the control picks up the new value once the mock's own change-feed
reports a `character` change — mirroring `campaign-configuration.spec.ts`'s established pattern
exactly (false-success/draft-loss/timeout scenarios included).

- [ ] **Step 1: Add a mock route for the `action` trigger**

`mockBackend.ts` has no route yet for `PUT /api/command/characters/{characterId}/action` — add
one mirroring the `PUT /api/command/campaigns/{campaignId}/configure` route added for the earlier
Max Stat Budget feature exactly (bare `route.fulfill({ status: 204, body: '' })`, no state
mutation — the whole point of this test is proving the SPA doesn't assume synchronous success).

- [ ] **Step 2: Write the test**

Create `web/e2e/character-traits.spec.ts`, seeding a Timadorus-named Campaign (`rulesets: [{ id:
'r1', name: 'Timadorus' }]`) with `configuration: JSON.stringify({ traits: ['strong', 'agile',
'quick'] })` and a Character with `info: JSON.stringify({ stats: { traitPoints: 2, traits:
['agile'] } })`. Cover, at minimum:
- The traits row shows "agile" and an "Add Trait" button (since `traitPoints > 0`).
- Clicking "Add Trait" reveals a `<select>` offering exactly `strong` and `quick` (not `agile` —
  already held).
- Selecting `strong` and confirming sends `PUT /api/command/characters/ch1/action` with body
  `{"action":"addTrait","trait":"strong"}`, shows "Update requested — refreshing…", then — after
  pushing a matching `character`-type change into `state.changes` and updating
  `state.characters`'s `info` to include `strong` — the pending status clears and the traits row
  now reads "agile, strong".
- A Character with `traitPoints: 0` shows no "Add Trait" button at all.
- A Character with `stats.traits: []` shows "(no traits selected)".

Adjust the exact assertions/selectors to this file's own established e2e conventions (`getByRole`,
`getByTestId`, scoped locators) — read `campaign-configuration.spec.ts` and
`character-configuration.spec.ts` first and mirror their patterns rather than inventing new ones.

- [ ] **Step 3: Run**

Run the full Playwright suite (this session's established `LD_LIBRARY_PATH` workaround for the
sandbox's `libnspr4.so` issue, if it reproduces).
Expected: the new test(s) pass alongside every pre-existing one.

- [ ] **Step 4: Commit**

```bash
git add web/e2e/support/mockBackend.ts web/e2e/character-traits.spec.ts
git commit -m "test/e2e: cover the Character traits list and Add Trait flow"
```

---

### Task 5: Real end-to-end coverage (Go, real cluster)

**Files:**
- Modify: `test/e2e/e2e_test.go`

Mirrors the earlier Max Stat Budget feature's real-cluster `It` exactly: resolve the real
"Timadorus" Ruleset by name, create a real User/Universe/Campaign (with the Campaign's
`configuration` seeded via the existing `configure` trigger — `PUT /campaigns/{id}/configure`
raising `ConfigurationRequested` doesn't set `traits` directly; instead, rely on the engine's own
`CampaignCreated` handling, which already seeds `traits: ["strong","agile","quick"]` for any
Timadorus-ruleset Campaign — no special setup needed), then a real Character under it, and prove
the full round trip:

- [ ] **Step 1: Add a new `It` block**

```go
	It("adding a trait to a Character validates against its Campaign's own trait list and eventually lands", func() {
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
			commandgen.CreateUserRequest{Name: "e2e-traits-user"}, &user)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var universe commandgen.UniverseCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/universes", env.BearerToken,
			commandgen.CreateUniverseRequest{Name: "e2e-traits-universe", CreatorUserIds: []uuid.UUID{user.Id}}, &universe)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var campaignResp commandgen.CampaignCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/campaigns", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateCampaignRequest{Name: "e2e-traits-campaign", RulesetId: timadorusRuleset.Id, GamemasterUserIds: []uuid.UUID{user.Id}}, &campaignResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		var characterResp commandgen.CharacterCreatedResponse
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/campaigns/%s/characters", env.CommandAPIBaseURL, campaignResp.Id), env.BearerToken,
			commandgen.CreateCharacterRequest{Name: "e2e-traits-character", PlayerUserId: user.Id}, &characterResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))

		// The default stats object should already be present once the engine's CharacterCreated
		// handling catches up.
		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var info map[string]any
			g.Expect(json.Unmarshal([]byte(got.Info), &info)).To(Succeed())
			stats, _ := info["stats"].(map[string]any)
			g.Expect(stats["traitPoints"]).To(Equal(float64(2)))
			g.Expect(stats["traits"]).To(BeEmpty())
		}, time.Minute, time.Second).Should(Succeed())

		// A trait NOT in the Campaign's own list must be rejected — no mutation.
		resp, err = doJSON(http.MethodPut, fmt.Sprintf("%s/characters/%s/action", env.CommandAPIBaseURL, characterResp.CharacterId), env.BearerToken,
			map[string]any{"action": "addTrait", "trait": "not-a-real-trait"}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))
		Consistently(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			var info map[string]any
			g.Expect(json.Unmarshal([]byte(got.Info), &info)).To(Succeed())
			stats, _ := info["stats"].(map[string]any)
			g.Expect(stats["traits"]).To(BeEmpty())
		}, 5*time.Second, time.Second).Should(Succeed())

		// A trait that IS in the Campaign's default seeded list ("strong") must succeed.
		resp, err = doJSON(http.MethodPut, fmt.Sprintf("%s/characters/%s/action", env.CommandAPIBaseURL, characterResp.CharacterId), env.BearerToken,
			map[string]any{"action": "addTrait", "trait": "strong"}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNoContent))

		Eventually(func(g Gomega) {
			var got querygen.Character
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/characters/%s", env.QueryAPIBaseURL, characterResp.CharacterId), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			var info map[string]any
			g.Expect(json.Unmarshal([]byte(got.Info), &info)).To(Succeed())
			stats, _ := info["stats"].(map[string]any)
			g.Expect(stats["traitPoints"]).To(Equal(float64(1)))
			g.Expect(stats["traits"]).To(ConsistOf("strong"))
		}, time.Minute, time.Second).Should(Succeed())
	})
```

`Consistently` is already part of this file's Ginkgo/Gomega import set if used elsewhere in this
repo's e2e suite — if not already imported, add it alongside `Eventually`. Confirm
`commandgen.CharacterCreatedResponse`'s exact id field name (`CharacterId`, per this file's own
existing usage) and `querygen.Character`'s `Info` field name against the real generated types
before finalizing — both already confirmed correct in this plan's own research, but re-verify
against the live generated files at implementation time in case they've drifted.

- [ ] **Step 2: Run against a real cluster**

`make dev-up` then `make test-e2e` (or however this repo's e2e suite is normally invoked in the
implementing environment).
Expected: PASS, alongside every pre-existing `It` in this file.

- [ ] **Step 3: Commit**

```bash
git add test/e2e/e2e_test.go
git commit -m "test/e2e: cover the real addTrait validation round trip against a live cluster"
```

---

## Final Verification

- `go build ./... && go vet ./... && go test -count=1 ./...` clean.
- `npm run build` clean; full Playwright suite clean, including Task 4's new test(s).
- Full Go e2e suite clean against a real cluster, including Task 5's new `It`.
- `git diff api/command/gen/server.gen.go web/src/api/command.types.ts` confirms both were
  regenerated from the same schema change with no other drift.
- Every `NewCharacterProcessor` call site in the repo (4 total, listed in Task 1) compiles against
  the new 3-argument signature.
