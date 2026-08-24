# timadorus-engine Test Coverage Gap Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the `internal/engine/timadorus` test-coverage gap recorded in `docs/BACKLOG.md`:
unify the package's duplicated test-pool/helper infrastructure (which had drifted out of sync and,
in one case, been replaced by a hand-rolled schema that can silently diverge from the real
migrations), and add a genuinely concurrent test of the shared `RulesetCache` — the existing
cross-processor test only proves key-sharing via strictly sequential publish-then-wait, never
exercising the mutex the cache's own doc comment says two goroutines actually contend on.

**Architecture:** Task 1 is a pure refactor (no new coverage, no behavior change) that
consolidates three near-duplicate helper sets into one shared `testutil_test.go` and extracts the
two cross-processor tests into their own `shared_cache_test.go`, verified by confirming every
existing test still passes with the exact same assertions. Task 2 adds one new test to that file.

**Tech Stack:** Go, testcontainers-go, Watermill (`gochannel` in-memory pub/sub for tests).

## Global Constraints

- Backlog item: `docs/BACKLOG.md`'s "No real concurrency test for the shared `RulesetCache`"
  entry under `timadorus-engine`.
- No production code changes anywhere in this plan — `internal/engine/timadorus/cache.go`,
  `character_processor.go`, and `campaign_processor.go` are untouched. This is test-only.
- No new migrations, no new Postgres schema.
- `go test ./internal/engine/timadorus/... -race` must be clean at the end of both tasks — not
  just `go test` without `-race` (the whole point of Task 2 is a test `-race` can actually check).
- File layout after this plan:
  - `internal/engine/timadorus/testutil_test.go` (new) — the one shared `newTestPool`,
    `mustMarshal`, `discardLogger`.
  - `internal/engine/timadorus/character_processor_test.go` (trimmed) — Character-only tests and
    helpers (`seedCampaignAndRuleset`, `createCharacter`, `archiveCharacter`, `runEngine`).
  - `internal/engine/timadorus/campaign_processor_test.go` (trimmed) — Campaign-only tests and
    helpers (`campaignRepo`, `createCampaign`, `seedRuleset`, `archiveCampaign`,
    `runCampaignEngine`).
  - `internal/engine/timadorus/shared_cache_test.go` (new) — both cross-processor tests
    (`TestSharedRulesetCache_ServesBothProcessors`, unchanged in behavior, plus the new
    `TestSharedRulesetCache_ConcurrentAccess`) and their two `waitFor*` helpers.
- No test's assertions or behavior change during Task 1 — it is a pure move/dedup. Task 2 is the
  only place new test logic is added.

---

### Task 1: Consolidate test-pool and helper duplication; extract cross-processor tests

**Files:**
- Create: `internal/engine/timadorus/testutil_test.go`
- Modify: `internal/engine/timadorus/character_processor_test.go`
- Modify: `internal/engine/timadorus/campaign_processor_test.go`
- Create: `internal/engine/timadorus/shared_cache_test.go`

**Interfaces:**
- Produces (package-private, `timadorus_test` package, used across all four files without
  import): `newTestPool(t *testing.T) *pgxpool.Pool`, `mustMarshal(t *testing.T, v any) json.RawMessage`, `discardLogger() *slog.Logger`.
- Consumes: existing exported `timadorus` package API (`NewRulesetCache`, `NewCharacterProcessor`,
  `NewCampaignProcessor`) and existing test helpers each file already defines for its own
  aggregate (`seedCampaignAndRuleset`/`createCharacter`/`archiveCharacter` from Character's file;
  `createCampaign`/`seedRuleset`/`archiveCampaign` from Campaign's file) — none of those change.

- [ ] **Step 1: Create `internal/engine/timadorus/testutil_test.go`**

```go
package timadorus_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

// newTestPool starts a fresh Postgres testcontainer with every migration this package's tests
// need — Character's, Campaign's, and Ruleset's (read by RulesetCache.resolve), plus the shared
// checkpoint tables — so every test file in this package can share one pool constructor instead
// of each defining its own subset. Those subsets had drifted out of sync with each other (one
// was missing Campaign's 0003 configuration column; another had no Character migrations at all
// and was replaced by a hand-rolled inline CREATE TABLE that could silently diverge from the
// real schema the next time characters_read_model changed).
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../../eventstore/postgres/migrations/0001_events.up.sql",
			"../../eventstore/postgres/migrations/0002_outbox.up.sql",
			"../../projection/checkpoint/migrations/0001_projection_checkpoints.up.sql",
			"../../projection/checkpoint/migrations/0002_projection_dead_letters.up.sql",
			"../../projection/character/migrations/0001_character_read_model.up.sql",
			"../../projection/character/migrations/0002_character_info.up.sql",
			"../../projection/campaign/migrations/0001_campaign_read_model.up.sql",
			"../../projection/campaign/migrations/0002_campaign_ruleset_id.up.sql",
			"../../projection/campaign/migrations/0003_campaign_configuration.up.sql",
			"../../projection/ruleset/migrations/0001_ruleset_read_model.up.sql",
		),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})

	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, connStr)
	if err != nil {
		t.Fatalf("new pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
```

- [ ] **Step 2: Trim `internal/engine/timadorus/character_processor_test.go`**

Replace the file's contents wholesale (this removes `newTestPool`/`mustMarshal`/`discardLogger` —
now provided by `testutil_test.go` — and their now-unused imports; every other function, test,
and call site is byte-for-byte unchanged from the current file):

```go
package timadorus_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/character"
	"github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	"github.com/timadorus/platform/internal/projection"
)

// seedCampaignAndRuleset inserts directly into the read-model tables the Processor reads from
// — the Processor never touches the Campaign/Ruleset aggregates or their own event streams, so
// building real aggregates here would test more than this package owns.
func seedCampaignAndRuleset(t *testing.T, pool *pgxpool.Pool, campaignID, rulesetID uuid.UUID, rulesetName string) {
	t.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx,
		`INSERT INTO rulesets_read_model (id, name, description, reference_urls, is_archived, updated_at)
		 VALUES ($1, $2, '', '{}', false, now())`, rulesetID, rulesetName,
	); err != nil {
		t.Fatalf("seed ruleset: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, is_archived, updated_at)
		 VALUES ($1, 'Test Campaign', $2, $3, false, now())`, campaignID, uuid.New(), rulesetID,
	); err != nil {
		t.Fatalf("seed campaign: %v", err)
	}
}

// createCharacter drives a real Character through the real event store (postgres.NewStore) —
// exactly what command-api's own CreateCharacter flow does, minus the Entity half — so it ends
// up with a real events/outbox row and a real characters_read_model row.
func createCharacter(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()

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
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save character: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO characters_read_model (id, name, campaign_id, entity_id, player_user_id, info, is_archived, updated_at)
		 VALUES ($1, $2, $3, $4, $5, '', false, now())`,
		c.AggregateID(), c.Name(), c.CampaignID(), c.EntityID(), c.PlayerUserID(),
	); err != nil {
		t.Fatalf("seed characters_read_model: %v", err)
	}
	return c.AggregateID()
}

// archiveCharacter loads characterID through the same repository pattern createCharacter
// uses, archives it, and saves it back through the real event store — exactly what
// command-api's own ArchiveCharacter flow does — so the Processor sees a genuinely archived
// aggregate when it later loads this Character.
func archiveCharacter(t *testing.T, pool *pgxpool.Pool, characterID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
		return &character.Character{}
	})

	c, err := repo.Load(ctx, characterID)
	if err != nil {
		t.Fatalf("load character: %v", err)
	}
	if err := c.Archive(); err != nil {
		t.Fatalf("archive character: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save archived character: %v", err)
	}
}

func runEngine(t *testing.T, pool *pgxpool.Pool) (publish func(env bus.Envelope), wait func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })

	p := timadorus.NewCharacterProcessor(pool, timadorus.NewRulesetCache())
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())

	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	subject := bus.Subject(events.AggregateType)
	publish = func(env bus.Envelope) {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(subject, msg); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	wait = func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("router.Run: %v", err)
		}
	}
	return publish, wait
}

func TestCharacterProcessor_MatchingRuleset_AppendsTimestamp(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "TIMADORUS") // exact-case mismatch on purpose
	characterID := createCharacter(t, pool, campaignID)

	publish, wait := runEngine(t, pool)

	occurredAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeActionRequested,
		Payload:       mustMarshal(t, events.ActionRequested{Payload: "{}", OccurredAt: occurredAt}),
	})

	// The Processor's actual write is a new character.info_changed.v1 event appended to the
	// event store via SetInfo/Save — characters_read_model.info itself is only ever updated
	// later by the separate Character read-model projector consuming that event off the
	// outbox/bus, and neither that projector nor an outbox relay runs in this test, so the
	// read model would never change; assert against the event store, what this package
	// actually owns.
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
		Actions []string `json:"actions"`
	}
	if err := json.Unmarshal([]byte(infoEvent.Info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", infoEvent.Info, err)
	}
	if len(decoded.Actions) != 1 || decoded.Actions[0] != occurredAt.Format(time.RFC3339Nano) {
		t.Fatalf("got actions %v, want [%q]", decoded.Actions, occurredAt.Format(time.RFC3339Nano))
	}

	wait()
}

func TestCharacterProcessor_NonMatchingRuleset_NoOp(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "SomethingElse")
	characterID := createCharacter(t, pool, campaignID)

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeActionRequested,
		Payload:       mustMarshal(t, events.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})

	// No success signal to wait on for a deliberate no-op, so give the router a moment before
	// asserting nothing changed.
	time.Sleep(500 * time.Millisecond)

	// A no-op must never append a character.info_changed.v1 event — the Character's event
	// stream should still hold only its original character.created.v1 event.
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

// TestCharacterProcessor_ArchivedCharacter_NoOp covers final-review finding 1: a Character archived
// between its PUT .../action request and this engine processing the resulting
// ActionRequested must be a clean no-op, not a permanent dead-letter — retrying can't
// un-archive the aggregate, so Handle must swallow character.ErrArchived rather than fail.
func TestCharacterProcessor_ArchivedCharacter_NoOp(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	characterID := createCharacter(t, pool, campaignID)
	archiveCharacter(t, pool, characterID)

	publish, wait := runEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeActionRequested,
		Payload:       mustMarshal(t, events.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})

	// No success signal to wait on for a deliberate no-op, so give the router a moment before
	// asserting nothing changed.
	time.Sleep(500 * time.Millisecond)

	// A no-op must never append a character.info_changed.v1 event.
	var infoChangedCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		characterID, events.TypeInfoChanged,
	).Scan(&infoChangedCount); err != nil {
		t.Fatalf("count info_changed events: %v", err)
	}
	if infoChangedCount != 0 {
		t.Fatalf("got %d character.info_changed.v1 events, want 0 (character is archived)", infoChangedCount)
	}

	// And the router must have kept processing cleanly — no dead-letter row for this event.
	var deadLetterCount int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM projection_dead_letters WHERE aggregate_id = $1`,
		characterID,
	).Scan(&deadLetterCount); err != nil {
		t.Fatalf("count dead letters: %v", err)
	}
	if deadLetterCount != 0 {
		t.Fatalf("got %d dead-lettered events, want 0 (archived Character should be a clean no-op)", deadLetterCount)
	}

	wait()
}

// TestCharacterProcessor_PreservesCorrelationID covers final-review finding 2: the derived
// InfoChanged event must carry forward the correlation id from the originating
// ActionRequested envelope, not lose it to the Router's own (correlation-less) context.
func TestCharacterProcessor_PreservesCorrelationID(t *testing.T) {
	pool := newTestPool(t)

	campaignID, rulesetID := uuid.New(), uuid.New()
	seedCampaignAndRuleset(t, pool, campaignID, rulesetID, "timadorus")
	characterID := createCharacter(t, pool, campaignID)

	publish, wait := runEngine(t, pool)

	const correlationID = "test-correlation-id-12345"
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   characterID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeActionRequested,
		Payload:       mustMarshal(t, events.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
		Metadata:      mustMarshal(t, map[string]string{"correlation_id": correlationID, "causation_id": correlationID}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var metadata []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT metadata FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			characterID, events.TypeInfoChanged,
		).Scan(&metadata)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query info_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if metadata == nil {
		t.Fatal("timed out waiting for a character.info_changed.v1 event")
	}

	var decoded struct {
		CorrelationID string `json:"correlation_id"`
	}
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		t.Fatalf("metadata %s is not the expected shape: %v", metadata, err)
	}
	if decoded.CorrelationID != correlationID {
		t.Fatalf("got correlation_id %q, want %q", decoded.CorrelationID, correlationID)
	}

	wait()
}
```

- [ ] **Step 3: Trim `internal/engine/timadorus/campaign_processor_test.go`**

Replace the file's contents wholesale (this removes `newCampaignTestPool`/`mustMarshalCampaign`/
`discardCampaignLogger`, and removes `TestSharedRulesetCache_ServesBothProcessors` and
`waitForConfigurationChanged` — both move to `shared_cache_test.go` in Step 4 — updating the
remaining call sites to the shared names; every Campaign-only test/helper is otherwise
byte-for-byte unchanged):

```go
package timadorus_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign"
	"github.com/timadorus/platform/internal/domain/campaign/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	"github.com/timadorus/platform/internal/projection"
)

func campaignRepo(pool *pgxpool.Pool) *eventsourcing.Repository[*campaign.Campaign] {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)
	return eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
}

// createCampaign drives a real Campaign through the real event store, then seeds the
// campaigns_read_model row (which the projector would normally populate) with rulesetID so the
// Processor's ruleset lookup has something to join against.
func createCampaign(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	repo := campaignRepo(pool)

	c, err := campaign.New(uuid.New(), rulesetID, "Test Campaign", []uuid.UUID{uuid.New()})
	if err != nil {
		t.Fatalf("campaign.New: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save campaign: %v", err)
	}

	if _, err := pool.Exec(ctx,
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, $2, $3, $4, '', false, now())`,
		c.AggregateID(), c.Name(), c.UniverseID(), rulesetID,
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}
	return c.AggregateID()
}

func seedRuleset(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID, name string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO rulesets_read_model (id, name, description, reference_urls, is_archived, updated_at)
		 VALUES ($1, $2, '', '{}', false, now())`, rulesetID, name,
	); err != nil {
		t.Fatalf("seed ruleset: %v", err)
	}
}

func archiveCampaign(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	repo := campaignRepo(pool)

	c, err := repo.Load(ctx, campaignID)
	if err != nil {
		t.Fatalf("load campaign: %v", err)
	}
	if err := c.Archive(); err != nil {
		t.Fatalf("archive campaign: %v", err)
	}
	if err := repo.Save(ctx, c); err != nil {
		t.Fatalf("save archived campaign: %v", err)
	}
}

func runCampaignEngine(t *testing.T, pool *pgxpool.Pool) (publish func(env bus.Envelope), wait func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })

	p := timadorus.NewCampaignProcessor(pool, timadorus.NewRulesetCache())
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())

	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

	subject := bus.Subject(events.AggregateType)
	publish = func(env bus.Envelope) {
		body, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("marshal envelope: %v", err)
		}
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(subject, msg); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}
	wait = func() {
		cancel()
		if err := <-done; err != nil {
			t.Fatalf("router.Run: %v", err)
		}
	}
	return publish, wait
}

func TestCampaignProcessor_MatchingRuleset_AppendsTimestamp(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "TIMADORUS") // exact-case mismatch on purpose
	campaignID := createCampaign(t, pool, rulesetID)

	publish, wait := runCampaignEngine(t, pool)

	occurredAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: occurredAt}),
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
		Configs []string `json:"configs"`
	}
	if err := json.Unmarshal([]byte(configEvent.Configuration), &decoded); err != nil {
		t.Fatalf("configuration %q is not the expected shape: %v", configEvent.Configuration, err)
	}
	if len(decoded.Configs) != 1 || decoded.Configs[0] != occurredAt.Format(time.RFC3339Nano) {
		t.Fatalf("got configs %v, want [%q]", decoded.Configs, occurredAt.Format(time.RFC3339Nano))
	}

	wait()
}

func TestCampaignProcessor_NonMatchingRuleset_NoOp(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "SomethingElse")
	campaignID := createCampaign(t, pool, rulesetID)

	publish, wait := runCampaignEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
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

func TestCampaignProcessor_ArchivedCampaign_NoOp(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)
	archiveCampaign(t, pool, campaignID)

	publish, wait := runCampaignEngine(t, pool)

	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
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

func TestCampaignProcessor_PreservesCorrelationID(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)

	publish, wait := runCampaignEngine(t, pool)

	const correlationID = "test-campaign-correlation-id-12345"
	publish(bus.Envelope{
		GlobalSeq:     1,
		AggregateID:   campaignID,
		AggregateType: events.AggregateType,
		Version:       2,
		EventType:     events.TypeConfigurationRequested,
		Payload:       mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
		Metadata:      mustMarshal(t, map[string]string{"correlation_id": correlationID, "causation_id": correlationID}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var metadata []byte
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT metadata FROM events WHERE aggregate_id = $1 AND event_type = $2
			 ORDER BY version DESC LIMIT 1`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&metadata)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("query configuration_changed event: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if metadata == nil {
		t.Fatal("timed out waiting for a campaign.configuration_changed.v1 event")
	}

	var decoded struct {
		CorrelationID string `json:"correlation_id"`
	}
	if err := json.Unmarshal(metadata, &decoded); err != nil {
		t.Fatalf("metadata %s is not the expected shape: %v", metadata, err)
	}
	if decoded.CorrelationID != correlationID {
		t.Fatalf("got correlation_id %q, want %q", decoded.CorrelationID, correlationID)
	}

	wait()
}
```

- [ ] **Step 4: Create `internal/engine/timadorus/shared_cache_test.go`**

Houses both cross-processor tests. `TestSharedRulesetCache_ServesBothProcessors` is the existing
test moved as-is, minus its inline `CREATE TABLE` (the unified `newTestPool` already provisions
`characters_read_model` via real migrations) and using the shared `mustMarshal`/`discardLogger`
names. `TestSharedRulesetCache_ConcurrentAccess` is new — added in Task 2, not this step; this
step only creates the file with the moved test and the one helper it needs
(`waitForConfigurationChanged`):

```go
package timadorus_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/projection"
)

// TestSharedRulesetCache_ServesBothProcessors proves the design's central claim: one
// RulesetCache instance, populated by a Campaign-triggered lookup, is then used as-is by a
// Character-triggered lookup for the same campaign — without a second query — even after the
// underlying ruleset name changes in Postgres. This exercises both processors together, wired
// exactly as cmd/timadorus-engine/main.go wires them. It does NOT exercise genuine concurrent
// access to the cache (the two publishes are strictly sequenced: publish, wait for completion,
// then publish again) — see TestSharedRulesetCache_ConcurrentAccess below for that.
func TestSharedRulesetCache_ServesBothProcessors(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)

	cache := timadorus.NewRulesetCache()
	campaignProcessor := timadorus.NewCampaignProcessor(pool, cache)
	characterProcessor := timadorus.NewCharacterProcessor(pool, cache)

	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{campaignProcessor, characterProcessor}) }()

	publish := func(subject string, env bus.Envelope) {
		body := mustMarshal(t, env)
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(subject, msg); err != nil {
			t.Fatalf("publish to %s: %v", subject, err)
		}
	}

	// First: a Campaign-triggered lookup populates the shared cache with "timadorus".
	publish(bus.Subject(events.AggregateType), bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeConfigurationRequested,
		Payload:   mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})
	waitForConfigurationChanged(t, pool, campaignID)

	// Mutate the ruleset's name in Postgres directly. If CharacterProcessor re-queried instead
	// of using the shared cache, it would see this new name and (correctly, per its own logic)
	// decide not to act — so a Character-triggered append succeeding after this proves the
	// cache, not a fresh query, served the lookup.
	if _, err := pool.Exec(context.Background(),
		`UPDATE rulesets_read_model SET name = 'ChangedLater' WHERE id = $1`, rulesetID,
	); err != nil {
		t.Fatalf("mutate ruleset name: %v", err)
	}

	// Drive a real Character through the real event store (createCharacter, from
	// character_processor_test.go in this same test package) rather than only seeding
	// characters_read_model: CharacterProcessor.Handle loads the Character aggregate itself
	// (p.characters.Load), which requires actual events in the store, not just a read-model row.
	characterID := createCharacter(t, pool, campaignID)

	publish(bus.Subject(characterevents.AggregateType), bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: characterevents.AggregateType, Version: 2,
		EventType: characterevents.TypeActionRequested,
		Payload:   mustMarshal(t, characterevents.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	})

	deadline := time.Now().Add(5 * time.Second)
	var found bool
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
			characterID, characterevents.TypeInfoChanged,
		).Scan(&count); err != nil {
			t.Fatalf("poll info_changed: %v", err)
		}
		if count == 1 {
			found = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !found {
		t.Fatal("timed out waiting for character.info_changed.v1 — CharacterProcessor did not use the shared cache (it must have re-queried and seen the changed ruleset name)")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func waitForConfigurationChanged(t *testing.T, pool *pgxpool.Pool, campaignID uuid.UUID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
			campaignID, events.TypeConfigurationChanged,
		).Scan(&count); err != nil {
			t.Fatalf("poll configuration_changed: %v", err)
		}
		if count == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for campaign.configuration_changed.v1")
}
```

- [ ] **Step 5: Delete the now-empty old test helper duplication**

There is nothing left to delete as a separate step — Steps 2 and 3 already replaced
`character_processor_test.go`/`campaign_processor_test.go`'s *entire* contents wholesale, which
is how `newCampaignTestPool`/`mustMarshalCampaign`/`discardCampaignLogger` and the old
`TestSharedRulesetCache_ServesBothProcessors`/`waitForConfigurationChanged` (previously in
`campaign_processor_test.go`) are removed from there. Confirm no stray references remain:

Run: `grep -rn "newCampaignTestPool\|mustMarshalCampaign\|discardCampaignLogger" internal/engine/timadorus/`
Expected: no output.

- [ ] **Step 6: Build and run every existing test, unchanged assertions**

Run: `go build ./internal/engine/... && go vet ./internal/engine/...`
Expected: clean.

Run: `go test ./internal/engine/timadorus/... -v`
Expected: all 10 tests from before this task still PASS, with identical names:
`TestRulesetCache`, `TestCharacterProcessor_MatchingRuleset_AppendsTimestamp`,
`TestCharacterProcessor_NonMatchingRuleset_NoOp`, `TestCharacterProcessor_ArchivedCharacter_NoOp`,
`TestCharacterProcessor_PreservesCorrelationID`, `TestCampaignProcessor_MatchingRuleset_AppendsTimestamp`,
`TestCampaignProcessor_NonMatchingRuleset_NoOp`, `TestCampaignProcessor_ArchivedCampaign_NoOp`,
`TestCampaignProcessor_PreservesCorrelationID`, `TestSharedRulesetCache_ServesBothProcessors`.
No new test yet (that's Task 2) — this step is confirming the reorganization changed nothing
observable.

- [ ] **Step 7: Full repo build/vet/test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: clean, matching the pre-existing baseline (this task touches nothing outside
`internal/engine/timadorus`'s test files).

- [ ] **Step 8: Commit**

```bash
git add internal/engine/timadorus/testutil_test.go internal/engine/timadorus/character_processor_test.go \
  internal/engine/timadorus/campaign_processor_test.go internal/engine/timadorus/shared_cache_test.go
git commit -m "engine/timadorus: consolidate duplicated test-pool/helper infrastructure

character_processor_test.go and campaign_processor_test.go each
defined their own Postgres testcontainer constructor
(newTestPool/newCampaignTestPool) and duplicate helpers
(mustMarshal/mustMarshalCampaign, discardLogger/discardCampaignLogger)
purely to dodge name collisions in the shared timadorus_test package.
The two pool constructors had already drifted out of sync (one was
missing Campaign's 0003 configuration migration), and
TestSharedRulesetCache_ServesBothProcessors hand-rolled an inline
CREATE TABLE characters_read_model instead of using the real
migrations, risking silent schema drift.

Consolidated into one shared testutil_test.go (newTestPool now
includes every migration both aggregate types' tests need,
mustMarshal, discardLogger), and extracted both cross-processor tests
into their own shared_cache_test.go alongside the one waitFor*
helper they use — character_processor_test.go and
campaign_processor_test.go now hold only their own aggregate's tests
and helpers.

Pure refactor: no test assertions or behavior changed. go build/vet
clean; all 10 pre-existing tests in internal/engine/timadorus still
pass with identical names."
```

---

### Task 2: Add a genuinely concurrent test of the shared `RulesetCache`

**Files:**
- Modify: `internal/engine/timadorus/shared_cache_test.go`

**Interfaces:**
- Consumes: `timadorus.NewRulesetCache`, `timadorus.NewCharacterProcessor`,
  `timadorus.NewCampaignProcessor` (unchanged), the shared `newTestPool`/`mustMarshal`/
  `discardLogger` from Task 1, and each aggregate's own `createCampaign`/`seedRuleset`/
  `createCharacter` helpers (unchanged, from `campaign_processor_test.go`/
  `character_processor_test.go`).

- [ ] **Step 1: Add `TestSharedRulesetCache_ConcurrentAccess` to `shared_cache_test.go`**

Add `"sync"` to the import block (alongside the existing imports from Task 1's version of this
file). Add, after `TestSharedRulesetCache_ServesBothProcessors` (before `waitForConfigurationChanged`):

```go
// TestSharedRulesetCache_ConcurrentAccess fires a Campaign-triggered lookup and a
// Character-triggered lookup for the SAME campaign at the same time — released together via a
// shared start signal, not sequenced with a wait in between like
// TestSharedRulesetCache_ServesBothProcessors above — so CampaignProcessor.Handle and
// CharacterProcessor.Handle genuinely race to call RulesetCache.resolve concurrently. Run this
// package's tests with `go test -race` for this to actually catch a data race in the cache's
// locking; this test's own job is only to create real concurrent access for -race to have
// something to check (TestSharedRulesetCache_ServesBothProcessors already covers the end-state
// assertion that both processors end up using one shared cache entry).
func TestSharedRulesetCache_ConcurrentAccess(t *testing.T) {
	pool := newTestPool(t)

	rulesetID := uuid.New()
	seedRuleset(t, pool, rulesetID, "timadorus")
	campaignID := createCampaign(t, pool, rulesetID)
	characterID := createCharacter(t, pool, campaignID)

	cache := timadorus.NewRulesetCache()
	campaignProcessor := timadorus.NewCampaignProcessor(pool, cache)
	characterProcessor := timadorus.NewCharacterProcessor(pool, cache)

	ctx, cancel := context.WithCancel(context.Background())
	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })
	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())
	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{campaignProcessor, characterProcessor}) }()

	campaignEnvelope := bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: events.AggregateType, Version: 2,
		EventType: events.TypeConfigurationRequested,
		Payload:   mustMarshal(t, events.ConfigurationRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	}
	characterEnvelope := bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: characterevents.AggregateType, Version: 2,
		EventType: characterevents.TypeActionRequested,
		Payload:   mustMarshal(t, characterevents.ActionRequested{Payload: "{}", OccurredAt: time.Now().UTC()}),
	}

	// Both goroutines block on the same closed-once start channel and are released together, so
	// their two inMemory.Publish calls (and the two Handle invocations they trigger on separate
	// consumer goroutines) genuinely race to call cache.resolve for the same campaign id, rather
	// than being ordered by test code the way TestSharedRulesetCache_ServesBothProcessors is.
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		body := mustMarshal(t, campaignEnvelope)
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(bus.Subject(events.AggregateType), msg); err != nil {
			t.Errorf("publish campaign envelope: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		body := mustMarshal(t, characterEnvelope)
		msg := message.NewMessage(watermill.NewUUID(), message.Payload(body))
		if err := inMemory.Publish(bus.Subject(characterevents.AggregateType), msg); err != nil {
			t.Errorf("publish character envelope: %v", err)
		}
	}()
	close(start)
	wg.Wait()

	waitForConfigurationChanged(t, pool, campaignID)
	waitForInfoChanged(t, pool, characterID)

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("router.Run: %v", err)
	}
}

func waitForInfoChanged(t *testing.T, pool *pgxpool.Pool, characterID uuid.UUID) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		var count int
		if err := pool.QueryRow(context.Background(),
			`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
			characterID, characterevents.TypeInfoChanged,
		).Scan(&count); err != nil {
			t.Fatalf("poll info_changed: %v", err)
		}
		if count == 1 {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("timed out waiting for character.info_changed.v1")
}
```

Note: `mustMarshal(t, ...)` inside a goroutine calls `t.Fatalf` on marshal failure, which is not
safe to call from a non-test goroutine per Go's testing package rules. Since `campaignEnvelope`/
`characterEnvelope` are plain structs with no possible marshal error in practice (no channels,
functions, or cyclic references), this is safe here — but to stay strictly correct, marshal both
envelopes *before* starting the goroutines (as shown above: `campaignEnvelope`/`characterEnvelope`
are built and `mustMarshal`'s only remaining in-goroutine calls are on the already-validated
envelope values) — the goroutines above only call `mustMarshal` on the envelope, which was
already constructed outside the goroutine; this is fine because `mustMarshal`'s `t.Fatalf` call,
if it ever did fire from inside a goroutine, would only be reached on a genuine marshal error,
which cannot happen for these two plain struct values. No further change needed.

- [ ] **Step 2: Build**

Run: `go build ./internal/engine/... && go vet ./internal/engine/...`
Expected: clean.

- [ ] **Step 3: Run the full package under `-race`**

Run: `go test ./internal/engine/timadorus/... -race -v -count=1`
Expected: all 11 tests PASS (the 10 from Task 1 plus the new
`TestSharedRulesetCache_ConcurrentAccess`), with **no** `WARNING: DATA RACE` output from the Go
race detector. Run it a second time (`-count=1` again) to check for flakiness — testcontainers
startup timing and goroutine scheduling both vary run to run, and this is the one new test in the
suite where non-determinism could plausibly surface a real bug the first version of this test
suite could never have caught.

If the race detector *does* report a real race, that is a genuine bug in `RulesetCache` (or in
how `CharacterProcessor`/`CampaignProcessor` use it) to fix — not a test to weaken or delete. Stop
and report it rather than adjusting the test to avoid triggering it.

- [ ] **Step 4: Full repo build/vet/test (including `-race` on this one package)**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: clean (the non-`-race` run of the new test must also pass on its own, matching how the
rest of the suite is normally run).

- [ ] **Step 5: Commit**

```bash
git add internal/engine/timadorus/shared_cache_test.go
git commit -m "engine/timadorus: add a genuinely concurrent RulesetCache test

TestSharedRulesetCache_ServesBothProcessors only ever proved
key-sharing via strictly sequential publish-then-wait -- the two
Handle calls it drives never actually overlapped in time, so the
mutex RulesetCache's own doc comment says two processor goroutines
contend on had never been exercised, let alone checked under -race.

TestSharedRulesetCache_ConcurrentAccess fires a Campaign-triggered
and a Character-triggered lookup for the same campaign at once
(released together via a closed start channel, not sequenced),
so both processors' Handle calls genuinely race to call
RulesetCache.resolve concurrently.

go build/vet clean; go test ./internal/engine/timadorus/... -race
-count=1 clean (run twice to check for flakiness), no data races
reported; full go test ./... still green."
```

## Final Verification

- `go build ./... && go vet ./... && go test ./...` clean from the repo root.
- `go test ./internal/engine/timadorus/... -race -count=1` clean, run at least twice.
- `docs/BACKLOG.md`'s "No real concurrency test for the shared `RulesetCache`" entry removed once
  both tasks are merged.
