# Realtime Aggregate Updates — Backend Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build `cmd/realtime`, a new service that pushes matching aggregate-change events to browser clients over Server-Sent Events, filtered server-side to exactly what each connection asked to watch.

**Architecture:** Extract the change-feed projectors' universe/campaign-id resolution logic into a shared package (`internal/aggregateresolve`) usable read-only, without a transaction. Add a new ephemeral (non-durable) NATS subscriber primitive (`internal/bus.NewEphemeralSubscriber`). A new in-memory fan-out registry (`internal/realtimehub`) matches each connected client's filter set against resolved events. `cmd/realtime` wires these together: 5 process-lifetime ephemeral NATS subscriptions (one per aggregate type, mirroring `cmd/projector`'s existing per-subject pattern) feed the hub; a hand-written SSE HTTP handler (long-lived streaming doesn't fit this codebase's `oapi-codegen` strict-handler pattern) serves connected browsers.

**Tech Stack:** Go 1.26, NATS JetStream (`watermill-nats`), `pgx`/`pgxpool`, `testcontainers-go` (Postgres and NATS modules) for integration tests.

This is the backend half of a two-plan feature. The frontend plan (composables, component migration, e2e tests) is written separately, after this one lands — see `docs/superpowers/specs/2026-09-12-realtime-aggregate-updates-design.md` for the full design covering both halves.

## Global Constraints

- No new migration and no new database table — `cmd/realtime` only reads existing read-model tables (`campaigns_read_model`, `entities_read_model`, `objects_read_model`, `characters_read_model`); `universe_changes_read_model` and its projectors are untouched.
- `cmd/realtime` keeps no checkpoint and performs no retry/dead-lettering — a resolve failure or a dropped connection is logged and dropped, never retried; the existing polling endpoint (unchanged, out of scope for this plan) is the client's own recovery path.
- `internal/aggregateresolve`'s functions take a `Querier` interface (`QueryRow` only) so both a `pgx.Tx` (existing projectors) and a `*pgxpool.Pool` (`cmd/realtime`) satisfy it without new adapter code.
- `bus.NewEphemeralSubscriber` must not replay history — a message published before `Subscribe` is called must never be delivered.
- The SSE endpoint is exactly as permissive as every existing GET endpoint in this codebase today (no new authorization/visibility model — none exists anywhere yet).
- Full design spec: `docs/superpowers/specs/2026-09-12-realtime-aggregate-updates-design.md`.

---

### Task 1: `internal/aggregateresolve` — shared Universe/Campaign resolvers

**Files:**
- Create: `internal/aggregateresolve/resolve.go`
- Test: `internal/aggregateresolve/resolve_test.go`, `internal/aggregateresolve/testutil_test.go`

**Interfaces:**
- Produces: `Querier` interface; `Campaign`, `Entity`, `Object` (`func(ctx, Querier, bus.Envelope) (uuid.UUID, error)`); `Character` (`func(ctx, Querier, bus.Envelope) (universeID, campaignID uuid.UUID, err error)`) — all consumed by Task 2 (the refactored projectors) and Task 6 (`cmd/realtime`).

- [ ] **Step 1: Write the package**

Create `internal/aggregateresolve/resolve.go`:

```go
// Package aggregateresolve resolves a Universe-scoped aggregate event's own owning Universe (and,
// for Character events, owning Campaign) id — the one piece of cross-projection lookup logic
// every Universe/Campaign/Entity/Object/Character event needs to answer "which Universe does this
// belong to," shared between internal/projection/universechanges (the write side, called with a
// transaction) and cmd/realtime (the read side, called with a plain pool — no ambient
// transaction). Universe itself needs no resolver: a Universe event's own aggregate id already is
// the Universe id.
package aggregateresolve

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	entityevents "github.com/timadorus/platform/internal/domain/entity/events"
	objectevents "github.com/timadorus/platform/internal/domain/object/events"
)

// Querier is satisfied by both pgx.Tx (the write side's ambient transaction) and *pgxpool.Pool
// (cmd/realtime's own pool, with no transaction) — mirrors the identical minimal-interface
// pattern already used for the same reason by the unexported querier in
// internal/eventstore/postgres/store.go.
type Querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Campaign resolves a Campaign event's own UniverseID: free (straight from the event's own
// payload) for CampaignCreated, a single read-model query for every other Campaign event.
func Campaign(ctx context.Context, q Querier, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == campaignevents.TypeCampaignCreated {
		var e campaignevents.CampaignCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("aggregateresolve: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}
	var universeID uuid.UUID
	if err := q.QueryRow(ctx,
		`SELECT universe_id FROM campaigns_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("aggregateresolve: resolve universe for campaign %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}

// Entity resolves an Entity event's own UniverseID — mirrors Campaign's exact shape.
func Entity(ctx context.Context, q Querier, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == entityevents.TypeEntityCreated {
		var e entityevents.EntityCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("aggregateresolve: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}
	var universeID uuid.UUID
	if err := q.QueryRow(ctx,
		`SELECT universe_id FROM entities_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("aggregateresolve: resolve universe for entity %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}

// Object resolves an Object event's own UniverseID — mirrors Campaign's exact shape.
func Object(ctx context.Context, q Querier, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == objectevents.TypeObjectCreated {
		var e objectevents.ObjectCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("aggregateresolve: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}
	var universeID uuid.UUID
	if err := q.QueryRow(ctx,
		`SELECT universe_id FROM objects_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("aggregateresolve: resolve universe for object %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}

// Character resolves a Character event's own UniverseID *and* CampaignID — the one resolver that
// needs a second return value, since nothing needed CampaignID before cmd/realtime's own
// campaign-scoped Character-list filter (see the design spec's Decision 3/4). CharacterCreated
// carries CampaignID directly in its own payload (so UniverseID needs one query, CampaignID needs
// none); every other event resolves both in a single joined query.
func Character(ctx context.Context, q Querier, env bus.Envelope) (universeID, campaignID uuid.UUID, err error) {
	if env.EventType == characterevents.TypeCharacterCreated {
		var e characterevents.CharacterCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("aggregateresolve: unmarshal %s: %w", env.EventType, err)
		}
		if err := q.QueryRow(ctx,
			`SELECT universe_id FROM campaigns_read_model WHERE id = $1`, e.CampaignID,
		).Scan(&universeID); err != nil {
			return uuid.Nil, uuid.Nil, fmt.Errorf("aggregateresolve: resolve universe for campaign %s (character %s create): %w", e.CampaignID, env.AggregateID, err)
		}
		return universeID, e.CampaignID, nil
	}

	if err := q.QueryRow(ctx,
		`SELECT c.universe_id, ch.campaign_id FROM characters_read_model ch
		 JOIN campaigns_read_model c ON c.id = ch.campaign_id
		 WHERE ch.id = $1`, env.AggregateID,
	).Scan(&universeID, &campaignID); err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("aggregateresolve: resolve universe/campaign for character %s: %w", env.AggregateID, err)
	}
	return universeID, campaignID, nil
}
```

- [ ] **Step 2: Write the test helpers**

Create `internal/aggregateresolve/testutil_test.go`:

```go
package aggregateresolve_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../projection/campaign/migrations/0001_campaign_read_model.up.sql",
			"../projection/campaign/migrations/0002_campaign_ruleset_id.up.sql",
			"../projection/campaign/migrations/0003_campaign_configuration.up.sql",
			"../projection/entity/migrations/0001_entity_read_model.up.sql",
			"../projection/object/migrations/0001_object_read_model.up.sql",
			"../projection/character/migrations/0001_character_read_model.up.sql",
			"../projection/character/migrations/0002_character_info.up.sql",
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

// seedCampaign inserts a minimal campaigns_read_model row — shared by every test that needs an
// existing Campaign to resolve a Character/Campaign event's UniverseID against.
func seedCampaign(t *testing.T, pool *pgxpool.Pool, campaignID, universeID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, 'Test Campaign', $2, $3, '', false, now())`,
		campaignID, universeID, uuid.New(),
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}
}
```

- [ ] **Step 3: Write the tests**

Create `internal/aggregateresolve/resolve_test.go`:

```go
package aggregateresolve_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/bus"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	entityevents "github.com/timadorus/platform/internal/domain/entity/events"
	objectevents "github.com/timadorus/platform/internal/domain/object/events"
)

func TestCampaign_Created_UsesPayloadUniverseID(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID := uuid.New(), uuid.New()

	got, err := aggregateresolve.Campaign(context.Background(), pool, bus.Envelope{
		AggregateID: campaignID, EventType: campaignevents.TypeCampaignCreated,
		Payload: mustMarshal(t, campaignevents.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: universeID, RulesetID: uuid.New(),
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC(),
		}),
	})
	if err != nil {
		t.Fatalf("Campaign: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s (from the payload, no query needed)", got, universeID)
	}
}

func TestCampaign_OtherEvent_ResolvesViaReadModel(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID := uuid.New(), uuid.New()
	seedCampaign(t, pool, campaignID, universeID)

	got, err := aggregateresolve.Campaign(context.Background(), pool, bus.Envelope{
		AggregateID: campaignID, EventType: campaignevents.TypeCampaignRenamed,
		Payload: mustMarshal(t, campaignevents.CampaignRenamed{Name: "Renamed", OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Campaign: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s (resolved via campaigns_read_model)", got, universeID)
	}
}

func TestEntity_Created_UsesPayloadUniverseID(t *testing.T) {
	pool := newTestPool(t)
	entityID, universeID := uuid.New(), uuid.New()

	got, err := aggregateresolve.Entity(context.Background(), pool, bus.Envelope{
		AggregateID: entityID, EventType: entityevents.TypeEntityCreated,
		Payload: mustMarshal(t, entityevents.EntityCreated{ID: entityID, Name: "Test Entity", UniverseID: universeID, OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Entity: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s", got, universeID)
	}
}

func TestEntity_OtherEvent_ResolvesViaReadModel(t *testing.T) {
	pool := newTestPool(t)
	entityID, universeID := uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO entities_read_model (id, name, universe_id, is_archived, updated_at) VALUES ($1, 'Test Entity', $2, false, now())`,
		entityID, universeID,
	); err != nil {
		t.Fatalf("seed entities_read_model: %v", err)
	}

	got, err := aggregateresolve.Entity(context.Background(), pool, bus.Envelope{
		AggregateID: entityID, EventType: entityevents.TypeEntityRenamed,
		Payload: mustMarshal(t, entityevents.EntityRenamed{Name: "Renamed", OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Entity: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s", got, universeID)
	}
}

func TestObject_Created_UsesPayloadUniverseID(t *testing.T) {
	pool := newTestPool(t)
	objectID, universeID := uuid.New(), uuid.New()

	got, err := aggregateresolve.Object(context.Background(), pool, bus.Envelope{
		AggregateID: objectID, EventType: objectevents.TypeObjectCreated,
		Payload: mustMarshal(t, objectevents.ObjectCreated{ID: objectID, Name: "Test Object", UniverseID: universeID, OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Object: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s", got, universeID)
	}
}

func TestObject_OtherEvent_ResolvesViaReadModel(t *testing.T) {
	pool := newTestPool(t)
	objectID, universeID := uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO objects_read_model (id, name, universe_id, is_archived, updated_at) VALUES ($1, 'Test Object', $2, false, now())`,
		objectID, universeID,
	); err != nil {
		t.Fatalf("seed objects_read_model: %v", err)
	}

	got, err := aggregateresolve.Object(context.Background(), pool, bus.Envelope{
		AggregateID: objectID, EventType: objectevents.TypeObjectRenamed,
		Payload: mustMarshal(t, objectevents.ObjectRenamed{Name: "Renamed", OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Object: %v", err)
	}
	if got != universeID {
		t.Fatalf("got %s, want %s", got, universeID)
	}
}

func TestCharacter_Created_ResolvesUniverseViaCampaignAndReturnsCampaignID(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID := uuid.New(), uuid.New()
	seedCampaign(t, pool, campaignID, universeID)

	characterID, entityID, playerID := uuid.New(), uuid.New(), uuid.New()
	gotUniverse, gotCampaign, err := aggregateresolve.Character(context.Background(), pool, bus.Envelope{
		AggregateID: characterID, EventType: characterevents.TypeCharacterCreated,
		Payload: mustMarshal(t, characterevents.CharacterCreated{
			ID: characterID, Name: "Aragorn", CampaignID: campaignID, EntityID: entityID,
			PlayerUserID: playerID, Info: "", OccurredAt: time.Now().UTC(),
		}),
	})
	if err != nil {
		t.Fatalf("Character: %v", err)
	}
	if gotUniverse != universeID {
		t.Fatalf("got universeID %s, want %s", gotUniverse, universeID)
	}
	if gotCampaign != campaignID {
		t.Fatalf("got campaignID %s, want %s (straight from the payload, no query needed)", gotCampaign, campaignID)
	}
}

func TestCharacter_OtherEvent_ResolvesBothViaJoin(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID, characterID, entityID, playerID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	seedCampaign(t, pool, campaignID, universeID)
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO characters_read_model (id, name, campaign_id, entity_id, player_user_id, info, is_archived, updated_at)
		 VALUES ($1, 'Aragorn', $2, $3, $4, '', false, now())`,
		characterID, campaignID, entityID, playerID,
	); err != nil {
		t.Fatalf("seed characters_read_model: %v", err)
	}

	gotUniverse, gotCampaign, err := aggregateresolve.Character(context.Background(), pool, bus.Envelope{
		AggregateID: characterID, EventType: characterevents.TypeCharacterRenamed,
		Payload: mustMarshal(t, characterevents.CharacterRenamed{Name: "Strider", OccurredAt: time.Now().UTC()}),
	})
	if err != nil {
		t.Fatalf("Character: %v", err)
	}
	if gotUniverse != universeID {
		t.Fatalf("got universeID %s, want %s", gotUniverse, universeID)
	}
	if gotCampaign != campaignID {
		t.Fatalf("got campaignID %s, want %s (resolved via the characters_read_model/campaigns_read_model join)", gotCampaign, campaignID)
	}
}
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/aggregateresolve/... -v`
Expected: PASS, all 8 tests. (Requires Docker — starts a real Postgres testcontainer.)

- [ ] **Step 5: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/aggregateresolve/
git commit -m "feat(realtime): add shared internal/aggregateresolve package

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 2: Refactor the 4 existing projectors to use `internal/aggregateresolve`

**Files:**
- Modify: `internal/projection/universechanges/campaign_projector.go`
- Modify: `internal/projection/universechanges/entity_projector.go`
- Modify: `internal/projection/universechanges/object_projector.go`
- Modify: `internal/projection/universechanges/character_projector.go`

**Interfaces:**
- Consumes: `aggregateresolve.Campaign`, `aggregateresolve.Entity`, `aggregateresolve.Object`, `aggregateresolve.Character` (Task 1).
- Produces: no change to any exported type's behavior — `NewCampaignProjector()` etc. and their `Handle` methods keep their exact existing signatures and observable behavior. This task is a pure internal refactor.

- [ ] **Step 1: Replace `campaign_projector.go`**

Replace its full contents with:

```go
package universechanges

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign/events"
)

const campaignProjectorName = "universe-changes-campaign"

type CampaignProjector struct{}

func NewCampaignProjector() *CampaignProjector { return &CampaignProjector{} }

func (p *CampaignProjector) Name() string { return campaignProjectorName }

func (p *CampaignProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

// Handle resolves this Campaign event's own UniverseID via the shared internal/aggregateresolve
// package (see that package's own doc comment for why it's shared with cmd/realtime) and records
// the change.
func (p *CampaignProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := aggregateresolve.Campaign(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}
```

- [ ] **Step 2: Replace `entity_projector.go`**

Replace its full contents with:

```go
package universechanges

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/entity/events"
)

const entityProjectorName = "universe-changes-entity"

type EntityProjector struct{}

func NewEntityProjector() *EntityProjector { return &EntityProjector{} }

func (p *EntityProjector) Name() string { return entityProjectorName }

func (p *EntityProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

// Handle resolves this Entity event's own UniverseID via the shared internal/aggregateresolve
// package — see CampaignProjector.Handle's doc comment for the full reasoning, which applies
// identically here.
func (p *EntityProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := aggregateresolve.Entity(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}
```

- [ ] **Step 3: Replace `object_projector.go`**

Replace its full contents with:

```go
package universechanges

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/object/events"
)

const objectProjectorName = "universe-changes-object"

type ObjectProjector struct{}

func NewObjectProjector() *ObjectProjector { return &ObjectProjector{} }

func (p *ObjectProjector) Name() string { return objectProjectorName }

func (p *ObjectProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

// Handle resolves this Object event's own UniverseID via the shared internal/aggregateresolve
// package — see CampaignProjector.Handle's doc comment for the full reasoning, which applies
// identically here.
func (p *ObjectProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := aggregateresolve.Object(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}
```

- [ ] **Step 4: Replace `character_projector.go`**

Replace its full contents with:

```go
package universechanges

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/character/events"
)

const characterProjectorName = "universe-changes-character"

type CharacterProjector struct{}

func NewCharacterProjector() *CharacterProjector { return &CharacterProjector{} }

func (p *CharacterProjector) Name() string { return characterProjectorName }

func (p *CharacterProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

// Handle resolves this Character event's own UniverseID via the shared internal/aggregateresolve
// package and records the change. aggregateresolve.Character also returns the Character's
// CampaignID — cmd/realtime needs it for campaign-scoped filtering, but this write side has no
// use for it, so it's discarded here.
func (p *CharacterProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, _, err := aggregateresolve.Character(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}
```

- [ ] **Step 5: Run the existing regression suite — must pass unmodified**

Run: `go test ./internal/projection/universechanges/... -v`
Expected: PASS, every existing test in `projector_test.go` (including `TestCampaignProjector_Created_UsesPayloadUniverseID`, `TestCampaignProjector_Renamed_ResolvesViaReadModel`, `TestEntityProjector_CreatedAndRenamed`, `TestObjectProjector_CreatedAndRenamed`, `TestCharacterProjector_Created_ResolvesViaCampaign`, `TestCharacterProjector_Renamed_ResolvesViaJoin`, `TestUniverseProjector_InsertsRow`) — none of these test files are touched by this task; a green run here is the proof the refactor changed nothing observable.

- [ ] **Step 6: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/projection/universechanges/campaign_projector.go internal/projection/universechanges/entity_projector.go internal/projection/universechanges/object_projector.go internal/projection/universechanges/character_projector.go
git commit -m "refactor(universechanges): resolve via shared internal/aggregateresolve, not duplicated SQL

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 3: `bus.NewEphemeralSubscriber`

**Files:**
- Modify: `internal/bus/nats.go`
- Test: `internal/bus/ephemeral_subscriber_test.go` (new)

**Interfaces:**
- Produces: `NewEphemeralSubscriber(url string, logger watermill.LoggerAdapter) (message.Subscriber, error)` — consumed by Task 6 (`cmd/realtime`).

- [ ] **Step 1: Write the failing test**

Create `internal/bus/ephemeral_subscriber_test.go`:

```go
package bus_test

import (
	"context"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/nats-io/nats.go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"

	"github.com/timadorus/platform/internal/bus"
)

func newTestNATSURL(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	container, err := tcnats.Run(ctx, "nats:2.10-alpine")
	if err != nil {
		t.Fatalf("start nats container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return connStr
}

// TestNewEphemeralSubscriber_DoesNotReplayHistory is the load-bearing proof this function exists
// for: a message published BEFORE the ephemeral subscription starts must never be delivered,
// unlike NewSubscriber's durable consumers (which redeliver everything since their last ack,
// including from a stream's very start on first use). A message published AFTER the subscription
// starts is still delivered live, proving this isn't simply "receives nothing."
func TestNewEphemeralSubscriber_DoesNotReplayHistory(t *testing.T) {
	url := newTestNATSURL(t)
	const subject = "test-ephemeral-subject"

	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	if _, err := js.AddStream(&nats.StreamConfig{Name: subject, Subjects: []string{subject}}); err != nil {
		t.Fatalf("add stream: %v", err)
	}
	if _, err := js.Publish(subject, []byte("before-subscribe")); err != nil {
		t.Fatalf("publish before-subscribe: %v", err)
	}

	sub, err := bus.NewEphemeralSubscriber(url, watermill.NopLogger{})
	if err != nil {
		t.Fatalf("NewEphemeralSubscriber: %v", err)
	}
	defer sub.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	msgs, err := sub.Subscribe(ctx, subject)
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	select {
	case msg := <-msgs:
		t.Fatalf("got a message (%q) before publishing anything post-subscribe, want none (no history replay)", msg.Payload)
	case <-time.After(500 * time.Millisecond):
		// expected: nothing delivered
	}

	if _, err := js.Publish(subject, []byte("after-subscribe")); err != nil {
		t.Fatalf("publish after-subscribe: %v", err)
	}

	select {
	case msg := <-msgs:
		if string(msg.Payload) != "after-subscribe" {
			t.Fatalf("got payload %q, want %q", msg.Payload, "after-subscribe")
		}
		msg.Ack()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the live (post-subscribe) message")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/bus/... -run TestNewEphemeralSubscriber -v`
Expected: FAIL — build error, `undefined: bus.NewEphemeralSubscriber`.

- [ ] **Step 3: Implement it**

In `internal/bus/nats.go`, add directly after `NewSubscriber`'s closing `}`:

```go
// NewEphemeralSubscriber constructs a Watermill Subscriber backed by a non-durable (ephemeral)
// JetStream consumer — unlike NewSubscriber, delivery starts from "now," not from any previously
// acknowledged position, and nothing persists across a call to Subscribe/Close. For a live
// notification service (cmd/realtime) that keeps no checkpoint and should never replay history it
// wasn't running to see, this is the correct semantic, not durable-but-uncheckpointed: a durable
// consumer only ever resumes from its last ack, so restarting such a service without an ephemeral
// consumer would either replay everything since the durable name was first created (unbounded,
// unwanted for a live-only feed) or require its own checkpoint table it has no other use for.
func NewEphemeralSubscriber(url string, logger watermill.LoggerAdapter) (message.Subscriber, error) {
	sub, err := nats.NewSubscriber(nats.SubscriberConfig{
		URL:              url,
		SubscribersCount: 1, // serial processing, same reasoning as NewSubscriber (docs/adr/0002)
		Unmarshaler:      &nats.NATSMarshaler{},
		JetStream: nats.JetStreamConfig{
			AutoProvision: true,
			// Deliberately no DurablePrefix/DurableCalculator — omitting them is what makes
			// JetStream create a genuinely ephemeral consumer per Subscribe call, cleaned up
			// automatically rather than needing an explicit delete (contrast
			// internal/bus.DurableName, which exists specifically because a durable consumer
			// needs one).
		},
	}, logger)
	if err != nil {
		return nil, fmt.Errorf("bus: new ephemeral NATS subscriber: %w", err)
	}
	return sub, nil
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/bus/... -run TestNewEphemeralSubscriber -v`
Expected: PASS. (Requires Docker — starts a real NATS testcontainer.)

- [ ] **Step 5: Run the full package suite and build/vet**

Run: `go test ./internal/bus/... && go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/bus/nats.go internal/bus/ephemeral_subscriber_test.go
git commit -m "feat(bus): add NewEphemeralSubscriber for non-durable, non-replaying consumption

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 4: `config.LoadRealtime`

**Files:**
- Modify: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `type Realtime struct { HTTPAddr, DatabaseURL, NATSURL string; PoolMaxConns int32; JWT JWT }`, `func LoadRealtime() (Realtime, error)` — consumed by Task 6.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
func TestLoadRealtime_PoolMaxConnsDefault(t *testing.T) {
	cfg, err := config.LoadRealtime()
	if err != nil {
		t.Fatalf("got error %v, want nil (variable unset must not be an error)", err)
	}
	if cfg.PoolMaxConns != 8 {
		t.Fatalf("got PoolMaxConns %d, want default 8", cfg.PoolMaxConns)
	}
}

func TestLoadRealtime_PoolMaxConnsOverride(t *testing.T) {
	t.Setenv("REALTIME_POOL_MAX_CONNS", "16")
	cfg, err := config.LoadRealtime()
	if err != nil {
		t.Fatalf("got error %v, want nil for a valid positive override", err)
	}
	if cfg.PoolMaxConns != 16 {
		t.Fatalf("got PoolMaxConns %d, want overridden 16", cfg.PoolMaxConns)
	}
}

func TestLoadRealtime_PoolMaxConnsUnparseable_ReturnsError(t *testing.T) {
	t.Setenv("REALTIME_POOL_MAX_CONNS", "not-a-number")
	_, err := config.LoadRealtime()
	if err == nil {
		t.Fatal("got nil error, want an error naming the invalid value instead of silently defaulting")
	}
	if !strings.Contains(err.Error(), "REALTIME_POOL_MAX_CONNS") {
		t.Fatalf("error %q does not name the offending environment variable", err.Error())
	}
}

func TestLoadRealtime_HTTPAddrDefault(t *testing.T) {
	cfg, err := config.LoadRealtime()
	if err != nil {
		t.Fatalf("got error %v, want nil", err)
	}
	if cfg.HTTPAddr != ":8085" {
		t.Fatalf("got HTTPAddr %q, want %q", cfg.HTTPAddr, ":8085")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/... -run TestLoadRealtime -v`
Expected: FAIL — build error, `undefined: config.LoadRealtime`.

- [ ] **Step 3: Implement it**

In `internal/config/config.go`, add directly after `LoadTimadorusEngine`'s closing `}` (before `loadJWT`):

```go
// Realtime serves /healthz, /readyz, /metrics like Projector/TimadorusEngine, plus one public
// route (GET /changes/stream) — small and hand-written enough (not oapi-codegen generated, since
// a long-lived streaming response doesn't fit that pattern) that it needs JWT config
// (CommandAPI/QueryAPI's shape) alongside Projector/TimadorusEngine's NATS/pool shape.
type Realtime struct {
	HTTPAddr     string
	DatabaseURL  string
	NATSURL      string
	PoolMaxConns int32
	JWT          JWT
}

// LoadRealtime returns an error only when REALTIME_POOL_MAX_CONNS is explicitly set to something
// invalid (see parsePoolMaxConns) — every other field is best-effort, matching this package's
// other Load* functions. Leaving the variable unset is not an error.
func LoadRealtime() (Realtime, error) {
	poolMaxConns, err := parsePoolMaxConns("REALTIME_POOL_MAX_CONNS", 8)
	if err != nil {
		return Realtime{}, err
	}
	return Realtime{
		HTTPAddr:     getEnv("REALTIME_ADDR", ":8085"),
		DatabaseURL:  getEnv("DATABASE_URL", "postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable"),
		NATSURL:      getEnv("NATS_URL", "nats://localhost:4222"),
		PoolMaxConns: poolMaxConns,
		JWT:          loadJWT(),
	}, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./internal/config/... -v`
Expected: PASS, including every pre-existing test in the package (no Docker required — these are pure env-var tests).

- [ ] **Step 5: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add LoadRealtime

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 5: `internal/realtimehub` — connection registry and filter matching

**Files:**
- Create: `internal/realtimehub/hub.go`
- Test: `internal/realtimehub/hub_test.go`

**Interfaces:**
- Produces: `Change`, `WatchClause` (with `Matches(ResolvedEvent) bool`), `ResolvedEvent`, `Hub` (`NewHub()`, `Register([]WatchClause) (<-chan Change, func())`, `Broadcast(ResolvedEvent)`) — consumed by Task 6 (`cmd/realtime`).
- No I/O, no NATS, no Postgres — pure in-memory logic, fully unit-testable on its own.

- [ ] **Step 1: Write the package**

Create `internal/realtimehub/hub.go`:

```go
// Package realtimehub is cmd/realtime's in-memory connection registry and fan-out point — the
// one thing that turns "an event arrived on NATS, already resolved to its owning Universe/
// Campaign" into "deliver it to every currently-matching SSE connection." See
// docs/superpowers/specs/2026-09-12-realtime-aggregate-updates-design.md Decisions 3 and 5.
package realtimehub

import "sync"

// Change is what a matched event becomes for delivery to a browser — the same shape
// web/src/composables/useChangeFeed.ts's AggregateChange already expects, so the SSE payload
// needs no frontend-side translation.
type Change struct {
	GlobalSeq     int64  `json:"globalSeq"`
	AggregateType string `json:"aggregateType"`
	AggregateID   string `json:"aggregateId"`
	EventType     string `json:"eventType"`
	OccurredAt    string `json:"occurredAt"`
}

// ResolvedEvent is one NATS event after cmd/realtime has resolved its UniverseID (and, for
// Character events, CampaignID) — what Broadcast matches WatchClauses against, and, on a match,
// delivers as a Change.
type ResolvedEvent struct {
	Change
	UniverseID string
	CampaignID string // only ever set when AggregateType == "character"
}

// WatchClause is one filter a connected client asks to be notified about — see the design spec's
// Decision 3 for the full ten-entry vocabulary. Exactly one of AggregateID/UniverseID/CampaignID
// is set, except a bare {Type: "universe"} clause (all three empty), which matches every Universe
// event unconditionally (the "watch every Universe" picker-list case — the only type with no
// meaningful scope to narrow by).
type WatchClause struct {
	Type        string `json:"type"`
	AggregateID string `json:"aggregateId,omitempty"`
	UniverseID  string `json:"universeId,omitempty"`
	CampaignID  string `json:"campaignId,omitempty"`
}

// Matches reports whether event satisfies clause.
func (clause WatchClause) Matches(event ResolvedEvent) bool {
	if clause.Type != event.AggregateType {
		return false
	}
	switch {
	case clause.AggregateID != "":
		return clause.AggregateID == event.AggregateID
	case clause.UniverseID != "":
		return clause.UniverseID == event.UniverseID
	case clause.CampaignID != "":
		return clause.CampaignID == event.CampaignID
	default:
		return clause.Type == "universe"
	}
}

// clientBufferSize bounds how many unconsumed Changes a slow client can accumulate before Hub
// disconnects it (see Broadcast) rather than blocking delivery to every other client — a
// disconnected client simply reconnects and catches up via the existing polling endpoint (design
// spec Decision 6), so dropping it is safe, not lossy in any way that matters.
const clientBufferSize = 32

type client struct {
	clauses   []WatchClause
	out       chan Change
	closeOnce sync.Once
}

func (c *client) closeOut() {
	c.closeOnce.Do(func() { close(c.out) })
}

// Hub is safe for concurrent use — Register/Broadcast (and the unregister function Register
// returns) are called from different goroutines (one per SSE connection, one per NATS subject).
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
}

func NewHub() *Hub {
	return &Hub{clients: make(map[*client]struct{})}
}

// Register adds a new client with clauses, returning its outbound channel and an unregister
// function the caller must invoke exactly once (typically via defer) when the connection ends.
// unregister is safe to call even if Broadcast has already disconnected this client itself (see
// Broadcast's own doc comment) — closeOnce makes double-closing the channel harmless.
func (h *Hub) Register(clauses []WatchClause) (out <-chan Change, unregister func()) {
	c := &client{clauses: clauses, out: make(chan Change, clientBufferSize)}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()
	return c.out, func() {
		h.mu.Lock()
		delete(h.clients, c)
		h.mu.Unlock()
		c.closeOut()
	}
}

// Broadcast delivers event to every currently-registered client whose filter matches. A client
// whose buffer is already full is disconnected (removed from the registry, its channel closed)
// rather than allowed to block delivery to every other client — deleting from h.clients while
// ranging over it is safe (Go guarantees this), and holding the single mutex for the whole call
// (rather than an RLock that would race a concurrent delete) is what keeps this correct without
// a separate remove path that would need to re-acquire the same lock reentrantly.
func (h *Hub) Broadcast(event ResolvedEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		matched := false
		for _, clause := range c.clauses {
			if clause.Matches(event) {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		select {
		case c.out <- event.Change:
		default:
			delete(h.clients, c)
			c.closeOut()
		}
	}
}
```

- [ ] **Step 2: Write the tests**

Create `internal/realtimehub/hub_test.go`:

```go
package realtimehub_test

import (
	"testing"
	"time"

	"github.com/timadorus/platform/internal/realtimehub"
)

func TestHub_Broadcast_DeliversToMatchingClauseOnly(t *testing.T) {
	h := realtimehub.NewHub()

	matchingOut, unregisterMatching := h.Register([]realtimehub.WatchClause{{Type: "character", AggregateID: "char-1"}})
	defer unregisterMatching()
	otherOut, unregisterOther := h.Register([]realtimehub.WatchClause{{Type: "character", AggregateID: "char-2"}})
	defer unregisterOther()

	h.Broadcast(realtimehub.ResolvedEvent{
		Change:     realtimehub.Change{GlobalSeq: 1, AggregateType: "character", AggregateID: "char-1", EventType: "character.renamed.v1"},
		UniverseID: "u1",
	})

	select {
	case got := <-matchingOut:
		if got.AggregateID != "char-1" {
			t.Fatalf("got %+v, want AggregateID char-1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("matching client got nothing")
	}

	select {
	case got := <-otherOut:
		t.Fatalf("non-matching client got a delivery it shouldn't have: %+v", got)
	case <-time.After(100 * time.Millisecond):
		// expected: nothing
	}
}

func TestHub_Broadcast_UniverseListMatchesAnyUniverseEvent(t *testing.T) {
	h := realtimehub.NewHub()
	out, unregister := h.Register([]realtimehub.WatchClause{{Type: "universe"}})
	defer unregister()

	h.Broadcast(realtimehub.ResolvedEvent{
		Change: realtimehub.Change{GlobalSeq: 1, AggregateType: "universe", AggregateID: "u1", EventType: "universe.renamed.v1"},
	})

	select {
	case got := <-out:
		if got.AggregateID != "u1" {
			t.Fatalf("got %+v, want AggregateID u1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("bare universe clause got nothing, want it to match any universe event")
	}
}

func TestHub_Broadcast_CharacterCampaignScopeMatches(t *testing.T) {
	h := realtimehub.NewHub()
	out, unregister := h.Register([]realtimehub.WatchClause{{Type: "character", CampaignID: "camp-1"}})
	defer unregister()

	h.Broadcast(realtimehub.ResolvedEvent{
		Change:     realtimehub.Change{GlobalSeq: 1, AggregateType: "character", AggregateID: "char-99", EventType: "character.info_changed.v1"},
		UniverseID: "u1",
		CampaignID: "camp-1",
	})

	select {
	case got := <-out:
		if got.AggregateID != "char-99" {
			t.Fatalf("got %+v, want AggregateID char-99", got)
		}
	case <-time.After(time.Second):
		t.Fatal("campaign-scoped clause got nothing")
	}
}

// TestHub_Broadcast_SlowClientIsDisconnectedNotBlocking is the load-bearing proof for the
// backpressure policy: a client that never drains its buffer must not prevent delivery to any
// other, well-behaved client, and must eventually see its channel closed rather than hang forever.
func TestHub_Broadcast_SlowClientIsDisconnectedNotBlocking(t *testing.T) {
	h := realtimehub.NewHub()
	slowOut, unregisterSlow := h.Register([]realtimehub.WatchClause{{Type: "entity", UniverseID: "u1"}})
	defer unregisterSlow()
	healthyOut, unregisterHealthy := h.Register([]realtimehub.WatchClause{{Type: "entity", UniverseID: "u1"}})
	defer unregisterHealthy()

	for i := 0; i < 40; i++ { // comfortably past the 32-slot buffer
		h.Broadcast(realtimehub.ResolvedEvent{
			Change:     realtimehub.Change{GlobalSeq: int64(i), AggregateType: "entity", AggregateID: "e1", EventType: "entity.renamed.v1"},
			UniverseID: "u1",
		})
		select {
		case <-healthyOut:
		case <-time.After(time.Second):
			t.Fatalf("healthy client stalled on broadcast %d — a slow sibling must not block it", i)
		}
	}

	select {
	case _, ok := <-slowOut:
		if ok {
			t.Fatal("slow client's channel yielded a value instead of being closed — buffer overflow should disconnect it")
		}
	case <-time.After(time.Second):
		t.Fatal("slow client's channel was neither closed nor delivered to — want it disconnected")
	}
}

func TestHub_Unregister_SafeAfterBroadcastAlreadyDisconnected(t *testing.T) {
	h := realtimehub.NewHub()
	out, unregister := h.Register([]realtimehub.WatchClause{{Type: "entity", UniverseID: "u1"}})

	for i := 0; i < 40; i++ {
		h.Broadcast(realtimehub.ResolvedEvent{
			Change:     realtimehub.Change{GlobalSeq: int64(i), AggregateType: "entity", AggregateID: "e1", EventType: "entity.renamed.v1"},
			UniverseID: "u1",
		})
	}
	<-out // drain whatever's buffered; irrelevant to this test

	// Broadcast has already disconnected this client (its buffer overflowed above). Calling the
	// unregister function Register returned must not panic (double-close) or deadlock.
	unregister()
}
```

- [ ] **Step 3: Run the tests**

Run: `go test ./internal/realtimehub/... -v`
Expected: PASS, all 5 tests. (No Docker needed — pure in-memory logic.)

- [ ] **Step 4: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add internal/realtimehub/
git commit -m "feat(realtime): add internal/realtimehub connection registry and filter matching

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 6: `cmd/realtime` — the service

**Files:**
- Create: `cmd/realtime/main.go`
- Test: `cmd/realtime/main_test.go` (internal test — `package main`, the first `*_test.go` for any `cmd/*` binary in this codebase, since this is the first one with logic worth testing beyond wiring)

**Interfaces:**
- Consumes: `aggregateresolve.{Campaign,Entity,Object,Character}` (Task 1), `bus.NewEphemeralSubscriber` (Task 3), `config.LoadRealtime` (Task 4), `realtimehub.{Hub,WatchClause,ResolvedEvent,Change}` (Task 5), `auth.NewVerifierFromConfig`/`auth.Verifier.Verify` (existing).
- Produces: a running service exposing `GET /changes/stream?watch=<json>&access_token=<jwt>`, `/healthz`, `/readyz`, `/metrics`.

- [ ] **Step 1: Write `cmd/realtime/main.go`**

```go
// Command realtime subscribes to the same NATS event stream the change-feed projectors do
// (internal/projection/universechanges) and pushes matching events to connected browser clients
// over Server-Sent Events — see
// docs/superpowers/specs/2026-09-12-realtime-aggregate-updates-design.md for the full design.
// Unlike every other consumer in this codebase, it keeps no checkpoint and writes nothing to
// Postgres: its NATS subscriptions are ephemeral (internal/bus.NewEphemeralSubscriber), existing
// only to serve currently-connected clients live state, not to build a durable read model. A
// client that misses something (a dropped connection, a cmd/realtime restart) recovers via the
// existing polling endpoint (GET /universes/{id}/changes), unchanged by this binary.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/timadorus/platform/internal/aggregateresolve"
	"github.com/timadorus/platform/internal/auth"
	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/config"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	entityevents "github.com/timadorus/platform/internal/domain/entity/events"
	objectevents "github.com/timadorus/platform/internal/domain/object/events"
	universeevents "github.com/timadorus/platform/internal/domain/universe/events"
	"github.com/timadorus/platform/internal/observability"
	"github.com/timadorus/platform/internal/realtimehub"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("realtime: fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.LoadRealtime()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	poolCfg, err := pgxpool.ParseConfig(cfg.DatabaseURL)
	if err != nil {
		return err
	}
	poolCfg.MaxConns = cfg.PoolMaxConns
	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	verifier, err := auth.NewVerifierFromConfig(ctx, auth.Config(cfg.JWT), logger, "realtime")
	if err != nil {
		return err
	}

	hub := realtimehub.NewHub()
	if err := subscribeAll(ctx, cfg.NATSURL, pool, hub, logger); err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", observability.HealthzHandler())
	mux.HandleFunc("/readyz", observability.ReadyzHandler(pool))
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/changes/stream", streamHandler(hub, verifier, logger))

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("realtime: listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		return err
	}
}

// resolver turns one aggregate type's envelope into a ResolvedEvent ready to broadcast.
type resolver func(ctx context.Context, pool *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error)

// aggregateSubscription pairs one aggregate type's NATS subject with its resolver.
type aggregateSubscription struct {
	subject string
	resolve resolver
}

// subscribeAll opens one ephemeral NATS subscription per aggregate type — exactly the same
// per-subject pattern cmd/projector already uses for these same five aggregate types (internal/
// projection.Router.Run), just with ephemeral (internal/bus.NewEphemeralSubscriber) rather than
// durable consumers, and fanning out to hub instead of writing to Postgres.
func subscribeAll(ctx context.Context, natsURL string, pool *pgxpool.Pool, hub *realtimehub.Hub, logger *slog.Logger) error {
	subs := []aggregateSubscription{
		{subject: bus.Subject(universeevents.AggregateType), resolve: resolveUniverse},
		{subject: bus.Subject(campaignevents.AggregateType), resolve: resolveCampaign},
		{subject: bus.Subject(entityevents.AggregateType), resolve: resolveEntity},
		{subject: bus.Subject(objectevents.AggregateType), resolve: resolveObject},
		{subject: bus.Subject(characterevents.AggregateType), resolve: resolveCharacter},
	}

	for _, s := range subs {
		subscriber, err := bus.NewEphemeralSubscriber(natsURL, watermill.NewSlogLogger(logger))
		if err != nil {
			return fmt.Errorf("new ephemeral subscriber for %q: %w", s.subject, err)
		}
		msgs, err := subscriber.Subscribe(ctx, s.subject)
		if err != nil {
			return fmt.Errorf("subscribe to %q: %w", s.subject, err)
		}
		go consume(ctx, msgs, pool, hub, logger, s.resolve)
	}
	return nil
}

func consume(ctx context.Context, msgs <-chan *message.Message, pool *pgxpool.Pool, hub *realtimehub.Hub, logger *slog.Logger, resolve resolver) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-msgs:
			if !ok {
				return
			}
			var env bus.Envelope
			if err := json.Unmarshal(msg.Payload, &env); err != nil {
				logger.Error("realtime: unmarshal envelope", "error", err)
				msg.Ack() // no retry — see package doc comment
				continue
			}
			resolved, err := resolve(ctx, pool, env)
			if err != nil {
				// A resolve failure (e.g. the referenced row not visible yet — the same narrow
				// race window RulesetCache.resolve/tryAddTrait's own cross-projection reads
				// already tolerate elsewhere) just means this one live update doesn't reach any
				// client; nothing was corrupted, and the next poll catches it up regardless. No
				// retry, no dead letter — this binary keeps no checkpoint to make a retry
				// meaningful against.
				logger.Warn("realtime: resolve failed, dropping this update",
					"aggregate_type", env.AggregateType, "aggregate_id", env.AggregateID, "error", err)
				msg.Ack()
				continue
			}
			hub.Broadcast(resolved)
			msg.Ack()
		}
	}
}

func resolveUniverse(_ context.Context, _ *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error) {
	return realtimehub.ResolvedEvent{Change: toChange(env), UniverseID: env.AggregateID.String()}, nil
}

func resolveCampaign(ctx context.Context, pool *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error) {
	universeID, err := aggregateresolve.Campaign(ctx, pool, env)
	if err != nil {
		return realtimehub.ResolvedEvent{}, err
	}
	return realtimehub.ResolvedEvent{Change: toChange(env), UniverseID: universeID.String()}, nil
}

func resolveEntity(ctx context.Context, pool *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error) {
	universeID, err := aggregateresolve.Entity(ctx, pool, env)
	if err != nil {
		return realtimehub.ResolvedEvent{}, err
	}
	return realtimehub.ResolvedEvent{Change: toChange(env), UniverseID: universeID.String()}, nil
}

func resolveObject(ctx context.Context, pool *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error) {
	universeID, err := aggregateresolve.Object(ctx, pool, env)
	if err != nil {
		return realtimehub.ResolvedEvent{}, err
	}
	return realtimehub.ResolvedEvent{Change: toChange(env), UniverseID: universeID.String()}, nil
}

func resolveCharacter(ctx context.Context, pool *pgxpool.Pool, env bus.Envelope) (realtimehub.ResolvedEvent, error) {
	universeID, campaignID, err := aggregateresolve.Character(ctx, pool, env)
	if err != nil {
		return realtimehub.ResolvedEvent{}, err
	}
	return realtimehub.ResolvedEvent{Change: toChange(env), UniverseID: universeID.String(), CampaignID: campaignID.String()}, nil
}

func toChange(env bus.Envelope) realtimehub.Change {
	return realtimehub.Change{
		GlobalSeq:     env.GlobalSeq,
		AggregateType: env.AggregateType,
		AggregateID:   env.AggregateID.String(),
		EventType:     env.EventType,
		OccurredAt:    env.CreatedAt.Format(time.RFC3339Nano),
	}
}

// streamHandler serves GET /changes/stream?watch=<json>&access_token=<jwt> — see design spec
// Decisions 2/3/7. Hand-written, not oapi-codegen generated: a long-lived streaming response
// doesn't fit the generated strict-handler request/response shape every other endpoint in this
// codebase uses.
func streamHandler(hub *realtimehub.Hub, verifier *auth.Verifier, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := verifier.Verify(r.Context(), r.URL.Query().Get("access_token")); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var clauses []realtimehub.WatchClause
		if raw := r.URL.Query().Get("watch"); raw != "" {
			if err := json.Unmarshal([]byte(raw), &clauses); err != nil {
				http.Error(w, "invalid watch parameter", http.StatusBadRequest)
				return
			}
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		out, unregister := hub.Register(clauses)
		defer unregister()

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case change, ok := <-out:
				if !ok {
					// Disconnected by the hub itself (buffer overflow); ending the handler closes
					// the connection, and the browser's EventSource reconnects on its own.
					return
				}
				payload, err := json.Marshal(change)
				if err != nil {
					logger.Error("realtime: marshal change for SSE", "error", err)
					continue
				}
				if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
					return // client gone
				}
				flusher.Flush()
			}
		}
	}
}
```

- [ ] **Step 2: Write `cmd/realtime/main_test.go`**

```go
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/testcontainers/testcontainers-go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/auth"
	"github.com/timadorus/platform/internal/bus"
	entityevents "github.com/timadorus/platform/internal/domain/entity/events"
	"github.com/timadorus/platform/internal/realtimehub"
)

// This is the first *_test.go file for any cmd/* binary in this codebase — every other binary's
// main.go is pure wiring with nothing worth testing directly. cmd/realtime's main.go has real
// logic (resolving events, matching filters, streaming) worth an end-to-end proof, so this lives
// in `package main` (an internal test) so it can call subscribeAll/streamHandler directly.

func newTestNATSURL(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	container, err := tcnats.Run(ctx, "nats:2.10-alpine")
	if err != nil {
		t.Fatalf("start nats container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	connStr, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	return connStr
}

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../../internal/projection/entity/migrations/0001_entity_read_model.up.sql",
		),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
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

const testHMACSecret = "test-secret-at-least-32-bytes-long!"
const testKeyID = "test"

// newTestVerifierAndToken mirrors test/e2e/internal/jwtsecret.go's own MintToken/NewStaticSecretKeySet
// pairing, self-contained here rather than imported (that package is for the real-cluster e2e
// suite specifically, not a shared test dependency for individual packages).
func newTestVerifierAndToken(t *testing.T) (*auth.Verifier, string) {
	t.Helper()
	keySet, err := auth.NewStaticSecretKeySet(testKeyID, []byte(testHMACSecret))
	if err != nil {
		t.Fatalf("new static secret key set: %v", err)
	}
	verifier := auth.NewVerifier(keySet, "", "")

	key, err := jwk.Import([]byte(testHMACSecret))
	if err != nil {
		t.Fatalf("import key: %v", err)
	}
	if err := key.Set(jwk.KeyIDKey, testKeyID); err != nil {
		t.Fatalf("set kid: %v", err)
	}
	now := time.Now()
	token, err := jwt.NewBuilder().Subject(uuid.NewString()).IssuedAt(now).Expiration(now.Add(time.Hour)).Build()
	if err != nil {
		t.Fatalf("build token: %v", err)
	}
	signed, err := jwt.Sign(token, jwt.WithKey(jwa.HS256(), key))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return verifier, string(signed)
}

// readSSEData reads a single "data: <json>\n\n" frame and returns its data payload (without the
// "data: " prefix). Returns "" on any read error (including EOF/context-cancelled) — callers pass
// the result through a channel and check it in the test's own goroutine (never call t.Fatal from
// inside a spawned goroutine — the testing package requires Fatal/FailNow to run on the goroutine
// executing the test itself).
func readSSEData(r *bufio.Reader) string {
	var data string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return ""
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			return data
		}
		data = strings.TrimPrefix(line, "data: ")
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return url.QueryEscape(string(b))
}

// TestStreamHandler_DeliversOnlyMatchingEvent is cmd/realtime's one end-to-end proof: a real NATS
// event, resolved through the real subscribeAll wiring, reaches a connection whose filter matches
// and does NOT reach one whose filter doesn't — proving the whole pipeline (NATS -> resolve ->
// hub -> SSE wire format) does what the design spec says, not just each piece in isolation.
func TestStreamHandler_DeliversOnlyMatchingEvent(t *testing.T) {
	natsURL := newTestNATSURL(t)
	pool := newTestPool(t)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	verifier, token := newTestVerifierAndToken(t)

	hub := realtimehub.NewHub()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := subscribeAll(ctx, natsURL, pool, hub, logger); err != nil {
		t.Fatalf("subscribeAll: %v", err)
	}
	// subscribeAll's goroutines subscribe asynchronously — give NATS a moment to establish the
	// consumers before publishing, or the first event could be missed the same way any live-only
	// (non-durable) subscriber can miss anything published before it's ready.
	time.Sleep(200 * time.Millisecond)

	srv := httptest.NewServer(streamHandler(hub, verifier, logger))
	defer srv.Close()

	universeID, otherUniverseID, entityID := uuid.New(), uuid.New(), uuid.New()

	matchingReq, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/changes/stream?access_token=%s&watch=%s",
		srv.URL, token, mustJSON(t, []map[string]string{{"type": "entity", "universeId": universeID.String()}})), nil)
	matchingResp, err := http.DefaultClient.Do(matchingReq)
	if err != nil {
		t.Fatalf("connect matching client: %v", err)
	}
	defer matchingResp.Body.Close()
	if matchingResp.StatusCode != http.StatusOK {
		t.Fatalf("matching client got status %d, want 200", matchingResp.StatusCode)
	}

	nonMatchingReq, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/changes/stream?access_token=%s&watch=%s",
		srv.URL, token, mustJSON(t, []map[string]string{{"type": "entity", "universeId": otherUniverseID.String()}})), nil)
	nonMatchingResp, err := http.DefaultClient.Do(nonMatchingReq)
	if err != nil {
		t.Fatalf("connect non-matching client: %v", err)
	}
	defer nonMatchingResp.Body.Close()

	pub, err := bus.NewPublisher(natsURL, watermill.NewSlogLogger(logger))
	if err != nil {
		t.Fatalf("new publisher: %v", err)
	}
	env := bus.Envelope{
		GlobalSeq: 1, AggregateID: entityID, AggregateType: entityevents.AggregateType, Version: 1,
		EventType: entityevents.TypeEntityCreated,
		Payload: mustJSONRaw(t, entityevents.EntityCreated{
			ID: entityID, Name: "Test Entity", UniverseID: universeID, OccurredAt: time.Now().UTC(),
		}),
		CreatedAt: time.Now().UTC(),
	}
	body, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal envelope: %v", err)
	}
	if err := pub.Publish(bus.Subject(entityevents.AggregateType), message.NewMessage(watermill.NewUUID(), body)); err != nil {
		t.Fatalf("publish: %v", err)
	}

	matchingCh := make(chan string, 1)
	go func() { matchingCh <- readSSEData(bufio.NewReader(matchingResp.Body)) }()
	select {
	case data := <-matchingCh:
		if data == "" {
			t.Fatal("matching client: read failed or connection closed unexpectedly")
		}
		var got realtimehub.Change
		if err := json.Unmarshal([]byte(data), &got); err != nil {
			t.Fatalf("unmarshal %q: %v", data, err)
		}
		if got.AggregateID != entityID.String() {
			t.Fatalf("got AggregateID %s, want %s", got.AggregateID, entityID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("matching client received nothing within 5s")
	}

	nonMatchingCh := make(chan string, 1)
	go func() { nonMatchingCh <- readSSEData(bufio.NewReader(nonMatchingResp.Body)) }()
	select {
	case data := <-nonMatchingCh:
		if data != "" {
			t.Fatalf("non-matching client received an event it should have been filtered out of: %s", data)
		}
	case <-time.After(time.Second):
		// expected: nothing arrived within the window
	}
}

func mustJSONRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return b
}
```

- [ ] **Step 3: Run the test**

Run: `go test ./cmd/realtime/... -v`
Expected: PASS. (Requires Docker — starts real NATS and Postgres testcontainers.)

- [ ] **Step 4: Build and vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add cmd/realtime/
git commit -m "feat(realtime): add cmd/realtime service (NATS -> hub -> SSE)

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```

---

### Task 7: Deployment wiring

**Files:**
- Create: `Dockerfile.realtime`
- Create: `api/realtime/openapi.yaml`
- Create: `deploy/helm/timadorus-platform/templates/realtime-deployment.yaml`
- Create: `deploy/helm/timadorus-platform/templates/realtime-service.yaml`
- Modify: `deploy/helm/timadorus-platform/values.yaml`

**Interfaces:**
- No Go code — this task only adds build/deploy artifacts for the binary Task 6 already produces.

- [ ] **Step 1: Create `Dockerfile.realtime`**

```dockerfile
# Build: docker build -f Dockerfile.realtime -t timadorus/realtime .
# Run:   docker run -p 8085:8085 --env-file .env timadorus/realtime

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/realtime ./cmd/realtime

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/realtime /realtime
EXPOSE 8085
ENTRYPOINT ["/realtime"]
```

- [ ] **Step 2: Create `api/realtime/openapi.yaml`**

Documentation only — `cmd/realtime`'s handler is hand-written (Task 6), not generated from this
spec (a long-lived streaming response doesn't fit `oapi-codegen`'s strict-handler request/response
shape). This file exists so the endpoint is discoverable the same way every other HTTP surface in
this codebase is.

```yaml
openapi: "3.0.3"
info:
  title: Timadorus Realtime API
  version: "1.0.0"
  description: >
    Server-Sent Events push for aggregate changes. Unlike api/command and api/query, this spec is
    documentation only — cmd/realtime's handler is hand-written, not oapi-codegen generated, since
    a long-lived streaming response doesn't fit the generated strict-handler request/response
    shape every other endpoint in this codebase uses.
paths:
  /changes/stream:
    get:
      operationId: streamChanges
      summary: >
        Stream aggregate changes matching the given watch filter as Server-Sent Events. Each
        event is a `data: <json>\n\n` frame whose JSON body matches the same shape
        GET /universes/{universeId}/changes (api/query) already returns.
      parameters:
        - name: watch
          in: query
          required: false
          schema:
            type: string
          description: >
            URL-encoded JSON array of watch clauses (see
            docs/superpowers/specs/2026-09-12-realtime-aggregate-updates-design.md Decision 3).
            Omitted or empty matches nothing.
        - name: access_token
          in: query
          required: true
          schema:
            type: string
          description: >
            The same bearer JWT used elsewhere via the Authorization header — passed as a query
            parameter here because EventSource cannot set custom headers (see Decision 7).
      responses:
        "200":
          description: An ongoing `text/event-stream` response.
          content:
            text/event-stream:
              schema:
                type: string
        "400":
          description: The `watch` parameter was present but not valid JSON.
        "401":
          description: Missing or invalid `access_token`.
```

- [ ] **Step 3: Create the Helm deployment template**

Read the actual current `deploy/helm/timadorus-platform/templates/projector-deployment.yaml`
first — the block below reproduces it, but verify labels/selector helper names
(`timadorus-platform.labels`/`timadorus-platform.selectorLabels`/`timadorus-platform.fullname`)
against the real file rather than trusting this plan blindly. Create
`deploy/helm/timadorus-platform/templates/realtime-deployment.yaml`, mirroring
`projector-deployment.yaml`'s NATS/DB env and `query-api-deployment.yaml`'s JWT env (since this
binary validates bearer tokens) — but deliberately WITHOUT projector's `strategy: { type: Recreate
}`: that exists because projector runs a single-active durable consumer per projection, where two
replicas processing the same durable consumer concurrently would be wrong; cmd/realtime's
subscriptions are ephemeral and per-process (Task 6), so multiple replicas each independently
receiving the same broadcast NATS messages is fine, even desirable for availability — the default
`RollingUpdate` strategy applies, matching `query-api-deployment.yaml`'s own omission of `strategy`
for the same reason (stateless replicas, safe to run more than one at a time):

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-realtime
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
    app.kubernetes.io/component: realtime
spec:
  replicas: {{ .Values.realtime.replicas }}
  selector:
    matchLabels:
      {{- include "timadorus-platform.selectorLabels" . | nindent 6 }}
      app.kubernetes.io/component: realtime
  template:
    metadata:
      labels:
        {{- include "timadorus-platform.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: realtime
    spec:
      containers:
        - name: realtime
          image: "{{ .Values.realtime.image.repository }}:{{ .Values.realtime.image.tag | default .Chart.AppVersion }}"
          imagePullPolicy: {{ .Values.realtime.image.pullPolicy }}
          ports:
            - name: http
              containerPort: {{ .Values.realtime.containerPort }}
          env:
            - name: REALTIME_ADDR
              value: ":{{ .Values.realtime.containerPort }}"
            {{- include "timadorus-platform.databaseURLEnv" . | nindent 12 }}
            - name: NATS_URL
              value: {{ include "timadorus-platform.natsURL" . | quote }}
            {{- include "timadorus-platform.jwtEnv" . | nindent 12 }}
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            initialDelaySeconds: 10
            periodSeconds: 15
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            initialDelaySeconds: 5
            periodSeconds: 10
          resources:
            {{- toYaml .Values.realtime.resources | nindent 12 }}
```

- [ ] **Step 4: Create the Helm service template**

Read the actual current `deploy/helm/timadorus-platform/templates/query-api-service.yaml` first
to confirm this matches. Create `deploy/helm/timadorus-platform/templates/realtime-service.yaml`,
mirroring it:

```yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-realtime
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
    app.kubernetes.io/component: realtime
spec:
  type: ClusterIP
  selector:
    {{- include "timadorus-platform.selectorLabels" . | nindent 4 }}
    app.kubernetes.io/component: realtime
  ports:
    - name: http
      port: {{ .Values.realtime.containerPort }}
      targetPort: http
```

- [ ] **Step 5: Add the `realtime:` block to `values.yaml`**

Read the actual current `deploy/helm/timadorus-platform/values.yaml` first to confirm this
matches its established shape exactly (every existing service block — `commandApi`, `queryApi`,
`projector`, `timadorusEngine` — uses identical `resources` values regardless of role; don't
invent different numbers for this one). Add a new top-level block, mirroring `projector`'s shape
(no `route.hostname` — like `projector`, this binary's SSE port isn't exposed through the existing
web-facing Gateway route in this plan; add one only if/when the frontend plan actually needs
external access wired up):

```yaml
realtime:
  image:
    repository: timadorus/realtime
    tag: ""
    pullPolicy: IfNotPresent
  replicas: 1
  containerPort: 8085
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 256Mi
```

- [ ] **Step 6: Validate the Helm templates render**

Run: `helm template deploy/helm/timadorus-platform | grep -A5 "component: realtime"`
Expected: both the Deployment and Service render without error, with `containerPort: 8085` and
the `DATABASE_URL`/`NATS_URL`/JWT env vars present (confirm by inspecting the output directly).

- [ ] **Step 7: Build and vet the whole repo one more time**

Run: `go build ./... && go vet ./...`
Expected: clean — confirms `Dockerfile.realtime`'s own build step (`go build ... ./cmd/realtime`)
will succeed too.

- [ ] **Step 8: Commit**

```bash
git add Dockerfile.realtime api/realtime/openapi.yaml deploy/helm/timadorus-platform/templates/realtime-deployment.yaml deploy/helm/timadorus-platform/templates/realtime-service.yaml deploy/helm/timadorus-platform/values.yaml
git commit -m "feat(realtime): add Dockerfile, OpenAPI doc, and Helm deployment for cmd/realtime

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
```
