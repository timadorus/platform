# Universe Change Feed Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A polling-based, per-Universe change feed: a new generic read model recording every
event on Universe/Campaign/Entity/Object/Character, two new query-api endpoints to poll it, and a
new SPA composable/provide pair that lets any component react to a change made by another tab or
user — additively, alongside the existing session-local `sidebarRefreshSignal`/
`bumpSidebarRefresh`/`pendingEntityId` mechanisms, which stay untouched.

**Architecture:** Five independent, stateless, single-subject `projection.Projector`s (not one
projector on five subjects — see the design spec's own correction for why) each insert one row
per event they see into a shared `universe_changes_read_model` table, resolving the owning
Universe id via a direct payload/aggregate-id read for `Universe*` and every `*Created` event, or
a plain read-model query otherwise (mirroring `RulesetCache.resolve`'s own established pattern,
including its same accepted race window). `internal/query/universechanges` serves it read-only;
two new query-api endpoints expose it; a new SPA composable polls it on a 5-second interval and
exposes the latest change as one `Ref`, `provide`d by `WorkspaceView.vue` the same way
`sidebarRefreshSignal`/`pendingEntityId` already are, so every consumer uses the same familiar
`watch()`-and-filter idiom.

**Tech Stack:** Go, `jackc/pgx/v5`, Watermill/NATS (via `internal/bus`/`internal/projection`,
unchanged), `oapi-codegen` (already wired), testcontainers-go, Vue 3 `<script setup>`,
`@playwright/test`.

## Global Constraints

- Five separate projectors (`UniverseProjector`, `CampaignProjector`, `EntityProjector`,
  `ObjectProjector`, `CharacterProjector`), each subscribed to exactly one subject, each with its
  own `Name()`/checkpoint — never one projector on multiple subjects (see the design spec's
  Decisions section for the router-checkpoint hazard this avoids).
- No in-memory cache, no mutex, no shared state between the five projectors — universe-id
  resolution is either a direct payload/aggregate-id read (free) or a plain, stateless read-model
  query (`campaigns_read_model`/`entities_read_model`/`objects_read_model`/`characters_read_model`
  — never the write-side event store, never `internal/eventsourcing`).
- A zero-rows resolution result is a plain wrapped error (triggers the router's normal
  Nack/retry/dead-letter path) — never a special sentinel, never silently skipped.
- Every event on a covered aggregate type produces exactly one change-log row, unconditionally —
  no per-event-type inclusion/exclusion list.
- `global_seq` is the read model's primary key and the client's polling cursor — no separate
  sequence.
- `User`/`Ruleset` events are not covered — both are unparented, no "owning Universe" to resolve.
- The new SPA mechanism is additive: `sidebarRefreshSignal`/`bumpSidebarRefresh`/`pendingEntityId`
  and every existing `waitForX` helper are untouched.
- Any new migration must be wired into **both** `scripts/migrate-up.sh`'s `schema_owners` array
  **and** `Dockerfile.migrate`'s matching `COPY` line — the exact pairing a recent final review
  found missing for `ruleset_tables_read_model`. Both must be updated together this time.

---

### Task 1: `universe_changes_read_model` migration

**Files:**
- Create: `internal/projection/universechanges/migrations/0001_universe_changes_read_model.up.sql`
- Create: `internal/projection/universechanges/migrations/0001_universe_changes_read_model.down.sql`
- Modify: `scripts/migrate-up.sh`
- Modify: `Dockerfile.migrate`

**Interfaces:**
- Produces: the `universe_changes_read_model` table — Tasks 2 and 3 both depend on this schema
  existing (in their own testcontainers pools) and on the real migration being wired into both
  `scripts/migrate-up.sh` and `Dockerfile.migrate` for a real deploy.

- [ ] **Step 1: Add the migration**

Create `internal/projection/universechanges/migrations/0001_universe_changes_read_model.up.sql`:

```sql
-- One row per event on Universe/Campaign/Entity/Object/Character, scoped to the Universe it
-- belongs to — powers a per-Universe, polling-based change feed the SPA (or any client) can use
-- to detect a change made by another tab/user/the CLI, without already knowing the specific
-- aggregate to re-check. global_seq is the event store's own already-globally-ordered sequence,
-- reused directly as this table's primary key and as the client's polling cursor — no separate
-- sequence needed. Written by five independent, stateless projectors
-- (internal/projection/universechanges), never by cmd/timadorus-engine. User/Ruleset are
-- unparented and never appear here (see the design spec's own Explicitly Out of Scope).
CREATE TABLE universe_changes_read_model (
    global_seq     BIGINT PRIMARY KEY,
    universe_id    UUID NOT NULL,
    aggregate_type TEXT NOT NULL,
    aggregate_id   UUID NOT NULL,
    event_type     TEXT NOT NULL,
    occurred_at    TIMESTAMPTZ NOT NULL
);
CREATE INDEX ON universe_changes_read_model (universe_id, global_seq);
```

Create `internal/projection/universechanges/migrations/0001_universe_changes_read_model.down.sql`:

```sql
DROP TABLE universe_changes_read_model;
```

- [ ] **Step 2: Wire it into `scripts/migrate-up.sh`**

Add this line to the `schema_owners` array, immediately after the existing
`"command_ruleset:internal/command/ruleset/migrations"` entry (the last one in the array today):

```bash
  "projection_universe_changes:internal/projection/universechanges/migrations"
```

- [ ] **Step 3: Wire it into `Dockerfile.migrate`**

Add this line immediately after the existing
`COPY internal/command/ruleset/migrations /migrations/internal/command/ruleset/migrations` line
(the last such `COPY` line today, immediately before `COPY scripts/migrate-up.sh ...`):

```dockerfile
COPY internal/projection/universechanges/migrations /migrations/internal/projection/universechanges/migrations
```

- [ ] **Step 4: Verify the migration image actually builds and runs, empirically**

This is the exact class of gap a recent final review found (a migration added to
`scripts/migrate-up.sh` but not `Dockerfile.migrate`, breaking every real deploy silently past
every Go test). Don't just eyeball the two files — build the real image and run it:

```bash
docker build -f Dockerfile.migrate -t timadorus/migrate:plantest .
docker run --rm --entrypoint find timadorus/migrate:plantest /migrations/internal/projection/universechanges/migrations -type f
docker run --rm -d --name migrate-plantest-pg -e POSTGRES_PASSWORD=test -e POSTGRES_DB=test -p 15433:5432 postgres:16-alpine
sleep 3
docker run --rm --network host -e DATABASE_URL="postgres://postgres:test@localhost:15433/test?sslmode=disable" timadorus/migrate:plantest
docker stop migrate-plantest-pg
```

Expected: the `find` shows both `.sql` files; the migration run completes with exit 0, including
a `==> migrate up: projection_universe_changes` line. Clean up the container/image afterward.

- [ ] **Step 5: Commit**

```bash
git add internal/projection/universechanges/migrations/0001_universe_changes_read_model.up.sql internal/projection/universechanges/migrations/0001_universe_changes_read_model.down.sql scripts/migrate-up.sh Dockerfile.migrate
git commit -m "projection: add the universe_changes_read_model schema for the per-Universe change feed"
```

---

### Task 2: Five change-feed projectors

**Files:**
- Create: `internal/projection/universechanges/insert.go`
- Create: `internal/projection/universechanges/universe_projector.go`
- Create: `internal/projection/universechanges/campaign_projector.go`
- Create: `internal/projection/universechanges/entity_projector.go`
- Create: `internal/projection/universechanges/object_projector.go`
- Create: `internal/projection/universechanges/character_projector.go`
- Create: `internal/projection/universechanges/testutil_test.go`
- Create: `internal/projection/universechanges/projector_test.go`
- Modify: `cmd/projector/main.go`

**Interfaces:**
- Consumes: Task 1's `universe_changes_read_model` schema; `internal/bus.Envelope`; each source
  aggregate's own `events` package (`internal/domain/{universe,campaign,entity,object,character}/events`)
  — read-only, event-type constants and payload structs only, exactly like every other projector.
- Produces: `universechanges.NewUniverseProjector()`, `NewCampaignProjector()`,
  `NewEntityProjector()`, `NewObjectProjector()`, `NewCharacterProjector()`, each returning a
  `projection.Projector` — Task 2's own wiring into `cmd/projector/main.go` is the only consumer
  needed for this plan (no later task depends on these constructors directly).

- [ ] **Step 1: Add the shared insert helper**

Create `internal/projection/universechanges/insert.go`:

```go
// Package universechanges projects Universe/Campaign/Entity/Object/Character events into
// universe_changes_read_model — a generic, per-Universe change feed a client can poll to detect
// a change made elsewhere (another tab, another user, the CLI) without already knowing the
// specific aggregate to re-check. Deliberately five independent, single-subject projectors
// (Universe/Campaign/Entity/Object/Character) rather than one projector on five subjects:
// internal/projection/router.go runs one goroutine per subject but keys its checkpoint by
// Name() alone, so a single multi-subject projector could have a higher-global_seq event from
// one subject commit the checkpoint before a lower-global_seq event from a different subject is
// processed, silently skipping it. Five single-subject projectors sidesteps this entirely, each
// getting its own checkpoint like every other projector in this codebase already does.
package universechanges

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
)

// insertChange is the one piece of logic all five projectors in this package share — pure SQL,
// no business logic, no per-event-type filtering (every event on a covered aggregate type
// produces exactly one row, unconditionally — see the design spec for why). Not shared via a
// generic container; a plain function is the right amount of sharing here.
func insertChange(ctx context.Context, tx pgx.Tx, universeID uuid.UUID, env bus.Envelope) error {
	if _, err := tx.Exec(ctx,
		`INSERT INTO universe_changes_read_model (global_seq, universe_id, aggregate_type, aggregate_id, event_type, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (global_seq) DO NOTHING`,
		env.GlobalSeq, universeID, env.AggregateType, env.AggregateID, env.EventType, env.CreatedAt,
	); err != nil {
		return fmt.Errorf("universechanges: insert change for %s %s: %w", env.AggregateType, env.AggregateID, err)
	}
	return nil
}
```

- [ ] **Step 2: Add `UniverseProjector`**

Create `internal/projection/universechanges/universe_projector.go`:

```go
package universechanges

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/universe/events"
)

const UniverseProjectorName = "universe-changes-universe"

// UniverseProjector is the simplest of the five: a Universe event's own aggregate id already is
// the Universe id, so no resolution step is needed at all.
type UniverseProjector struct{}

func NewUniverseProjector() *UniverseProjector { return &UniverseProjector{} }

func (p *UniverseProjector) Name() string { return UniverseProjectorName }

func (p *UniverseProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *UniverseProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	return insertChange(ctx, tx, env.AggregateID, env)
}
```

- [ ] **Step 3: Add `CampaignProjector`**

Create `internal/projection/universechanges/campaign_projector.go`:

```go
package universechanges

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/campaign/events"
)

const CampaignProjectorName = "universe-changes-campaign"

type CampaignProjector struct{}

func NewCampaignProjector() *CampaignProjector { return &CampaignProjector{} }

func (p *CampaignProjector) Name() string { return CampaignProjectorName }

func (p *CampaignProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *CampaignProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := p.resolveUniverseID(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}

// resolveUniverseID reads UniverseID straight from CampaignCreated's own payload (free — every
// Campaign carries this immutably from creation). Every other Campaign event carries only its
// own aggregate id, so it's resolved via a plain read-model query — the same
// cross-projection-read pattern RulesetCache.resolve already established, including the same
// narrow "immediately after creation" race window: a zero-rows result here is a plain wrapped
// error, which the router's own Nack/retry path already handles exactly like every other
// cross-projection lookup in this codebase.
func (p *CampaignProjector) resolveUniverseID(ctx context.Context, tx pgx.Tx, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == events.TypeCampaignCreated {
		var e events.CampaignCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("universechanges: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}

	var universeID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT universe_id FROM campaigns_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("universechanges: resolve universe for campaign %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}
```

- [ ] **Step 4: Add `EntityProjector`**

Create `internal/projection/universechanges/entity_projector.go`:

```go
package universechanges

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/entity/events"
)

const EntityProjectorName = "universe-changes-entity"

type EntityProjector struct{}

func NewEntityProjector() *EntityProjector { return &EntityProjector{} }

func (p *EntityProjector) Name() string { return EntityProjectorName }

func (p *EntityProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *EntityProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := p.resolveUniverseID(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}

// resolveUniverseID mirrors CampaignProjector.resolveUniverseID's exact shape — see that
// method's doc comment for the full reasoning.
func (p *EntityProjector) resolveUniverseID(ctx context.Context, tx pgx.Tx, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == events.TypeEntityCreated {
		var e events.EntityCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("universechanges: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}

	var universeID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT universe_id FROM entities_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("universechanges: resolve universe for entity %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}
```

- [ ] **Step 5: Add `ObjectProjector`**

Create `internal/projection/universechanges/object_projector.go`:

```go
package universechanges

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/object/events"
)

const ObjectProjectorName = "universe-changes-object"

type ObjectProjector struct{}

func NewObjectProjector() *ObjectProjector { return &ObjectProjector{} }

func (p *ObjectProjector) Name() string { return ObjectProjectorName }

func (p *ObjectProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *ObjectProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := p.resolveUniverseID(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}

// resolveUniverseID mirrors CampaignProjector.resolveUniverseID's exact shape — see that
// method's doc comment for the full reasoning.
func (p *ObjectProjector) resolveUniverseID(ctx context.Context, tx pgx.Tx, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == events.TypeObjectCreated {
		var e events.ObjectCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("universechanges: unmarshal %s: %w", env.EventType, err)
		}
		return e.UniverseID, nil
	}

	var universeID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT universe_id FROM objects_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("universechanges: resolve universe for object %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}
```

- [ ] **Step 6: Add `CharacterProjector`**

Create `internal/projection/universechanges/character_projector.go`:

```go
package universechanges

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/character/events"
)

const CharacterProjectorName = "universe-changes-character"

type CharacterProjector struct{}

func NewCharacterProjector() *CharacterProjector { return &CharacterProjector{} }

func (p *CharacterProjector) Name() string { return CharacterProjectorName }

func (p *CharacterProjector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *CharacterProjector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	universeID, err := p.resolveUniverseID(ctx, tx, env)
	if err != nil {
		return err
	}
	return insertChange(ctx, tx, universeID, env)
}

// resolveUniverseID needs one more hop than its siblings: CharacterCreated carries CampaignID,
// not UniverseID directly, so even the create-time path needs a read-model query (against
// campaigns_read_model, keyed by the payload's own CampaignID) — Character is the only one of
// the five aggregate types where even Created isn't fully free. Every other Character event
// resolves via a single joined query against both characters_read_model and
// campaigns_read_model, rather than two separate round trips.
func (p *CharacterProjector) resolveUniverseID(ctx context.Context, tx pgx.Tx, env bus.Envelope) (uuid.UUID, error) {
	if env.EventType == events.TypeCharacterCreated {
		var e events.CharacterCreated
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return uuid.Nil, fmt.Errorf("universechanges: unmarshal %s: %w", env.EventType, err)
		}
		var universeID uuid.UUID
		if err := tx.QueryRow(ctx,
			`SELECT universe_id FROM campaigns_read_model WHERE id = $1`, e.CampaignID,
		).Scan(&universeID); err != nil {
			return uuid.Nil, fmt.Errorf("universechanges: resolve universe for campaign %s (character %s create): %w", e.CampaignID, env.AggregateID, err)
		}
		return universeID, nil
	}

	var universeID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT c.universe_id FROM characters_read_model ch
		 JOIN campaigns_read_model c ON c.id = ch.campaign_id
		 WHERE ch.id = $1`, env.AggregateID,
	).Scan(&universeID); err != nil {
		return uuid.Nil, fmt.Errorf("universechanges: resolve universe for character %s: %w", env.AggregateID, err)
	}
	return universeID, nil
}
```

- [ ] **Step 7: Add the shared test pool helper**

Create `internal/projection/universechanges/testutil_test.go`:

```go
package universechanges_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

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
			"../checkpoint/migrations/0001_projection_checkpoints.up.sql",
			"../universe/migrations/0001_universe_read_model.up.sql",
			"../campaign/migrations/0001_campaign_read_model.up.sql",
			"../campaign/migrations/0002_campaign_ruleset_id.up.sql",
			"../campaign/migrations/0003_campaign_configuration.up.sql",
			"../entity/migrations/0001_entity_read_model.up.sql",
			"../object/migrations/0001_object_read_model.up.sql",
			"../character/migrations/0001_character_read_model.up.sql",
			"../character/migrations/0002_character_info.up.sql",
			"migrations/0001_universe_changes_read_model.up.sql",
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

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
```

Before using this file, verify each listed migration path actually exists at that relative
location and check whether any campaign/character migration file is named differently than
guessed above (`0002_campaign_ruleset_id.up.sql`, `0003_campaign_configuration.up.sql`,
`0002_character_info.up.sql`) — run `ls ../campaign/migrations ../character/migrations` from
`internal/projection/universechanges/` and correct the list to match reality before writing the
rest of this file if any name differs.

- [ ] **Step 8: Add the projector tests**

Create `internal/projection/universechanges/projector_test.go`. This drives each projector
through the real `projection.Router` (matching `internal/projection/universe/projector_test.go`'s
own established pattern — the one existing precedent for testing a plain read-model projector at
this level, not a direct `Handle` call) using an in-memory `gochannel` pub/sub:

```go
package universechanges_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/bus"
	campaignevents "github.com/timadorus/platform/internal/domain/campaign/events"
	characterevents "github.com/timadorus/platform/internal/domain/character/events"
	entityevents "github.com/timadorus/platform/internal/domain/entity/events"
	objectevents "github.com/timadorus/platform/internal/domain/object/events"
	universeevents "github.com/timadorus/platform/internal/domain/universe/events"
	"github.com/timadorus/platform/internal/projection"
	"github.com/timadorus/platform/internal/projection/universechanges"
)

func mustMarshal(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

// runProjector wires one Projector to its own in-memory subscriber, running until wait() is
// called — mirrors internal/engine/timadorus's runCampaignEngine/runCampaignEngine-shaped
// helpers, adapted for a plain projection.Projector instead of an engine processor.
func runProjector(t *testing.T, pool *pgxpool.Pool, p projection.Projector, subject string) (publish func(env bus.Envelope), wait func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })

	router := projection.NewRouter(pool, func(string) (message.Subscriber, error) {
		return inMemory, nil
	}, discardLogger())

	done := make(chan error, 1)
	go func() { done <- router.Run(ctx, []projection.Projector{p}) }()

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

func waitForChangeRow(t *testing.T, pool *pgxpool.Pool, globalSeq int64) (universeID uuid.UUID, found bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		err := pool.QueryRow(context.Background(),
			`SELECT universe_id FROM universe_changes_read_model WHERE global_seq = $1`, globalSeq,
		).Scan(&universeID)
		if err == nil {
			return universeID, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return uuid.Nil, false
}

func TestUniverseProjector_InsertsRow(t *testing.T) {
	pool := newTestPool(t)
	p := universechanges.NewUniverseProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(universeevents.AggregateType))

	universeID := uuid.New()
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: universeID, AggregateType: universeevents.AggregateType, Version: 1,
		EventType: universeevents.TypeUniverseCreated,
		Payload:   mustMarshal(t, universeevents.UniverseCreated{ID: universeID, Name: "Test", CreatorUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})

	got, found := waitForChangeRow(t, pool, 1)
	if !found {
		t.Fatal("timed out waiting for the change row")
	}
	if got != universeID {
		t.Fatalf("got universe_id %s, want %s", got, universeID)
	}
	wait()
}

func TestCampaignProjector_Created_UsesPayloadUniverseID(t *testing.T) {
	pool := newTestPool(t)
	p := universechanges.NewCampaignProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(campaignevents.AggregateType))

	campaignID, universeID := uuid.New(), uuid.New()
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: campaignevents.AggregateType, Version: 1,
		EventType: campaignevents.TypeCampaignCreated,
		Payload: mustMarshal(t, campaignevents.CampaignCreated{
			ID: campaignID, Name: "Test Campaign", UniverseID: universeID, RulesetID: uuid.New(),
			GamemasterUserIDs: []uuid.UUID{uuid.New()}, OccurredAt: time.Now().UTC(),
		}),
		CreatedAt: time.Now().UTC(),
	})

	got, found := waitForChangeRow(t, pool, 1)
	if !found {
		t.Fatal("timed out waiting for the change row")
	}
	if got != universeID {
		t.Fatalf("got universe_id %s, want %s (from the event payload, no read-model lookup needed)", got, universeID)
	}
	wait()
}

func TestCampaignProjector_Renamed_ResolvesViaReadModel(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID := uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, 'Test Campaign', $2, $3, '', false, now())`,
		campaignID, universeID, uuid.New(),
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}

	p := universechanges.NewCampaignProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(campaignevents.AggregateType))

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: campaignID, AggregateType: campaignevents.AggregateType, Version: 2,
		EventType: campaignevents.TypeCampaignRenamed,
		Payload:   mustMarshal(t, campaignevents.CampaignRenamed{Name: "Renamed", OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})

	got, found := waitForChangeRow(t, pool, 1)
	if !found {
		t.Fatal("timed out waiting for the change row")
	}
	if got != universeID {
		t.Fatalf("got universe_id %s, want %s (resolved via campaigns_read_model)", got, universeID)
	}
	wait()
}

func TestEntityProjector_CreatedAndRenamed(t *testing.T) {
	pool := newTestPool(t)
	p := universechanges.NewEntityProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(entityevents.AggregateType))

	entityID, universeID := uuid.New(), uuid.New()
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: entityID, AggregateType: entityevents.AggregateType, Version: 1,
		EventType: entityevents.TypeEntityCreated,
		Payload:   mustMarshal(t, entityevents.EntityCreated{ID: entityID, Name: "Test Entity", UniverseID: universeID, OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})
	if got, found := waitForChangeRow(t, pool, 1); !found || got != universeID {
		t.Fatalf("created: got %s, found=%v, want %s", got, found, universeID)
	}

	if _, err := pool.Exec(context.Background(),
		`INSERT INTO entities_read_model (id, name, universe_id, is_archived, updated_at) VALUES ($1, 'Test Entity', $2, false, now())`,
		entityID, universeID,
	); err != nil {
		t.Fatalf("seed entities_read_model: %v", err)
	}
	publish(bus.Envelope{
		GlobalSeq: 2, AggregateID: entityID, AggregateType: entityevents.AggregateType, Version: 2,
		EventType: entityevents.TypeEntityRenamed,
		Payload:   mustMarshal(t, entityevents.EntityRenamed{Name: "Renamed Entity", OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})
	if got, found := waitForChangeRow(t, pool, 2); !found || got != universeID {
		t.Fatalf("renamed: got %s, found=%v, want %s", got, found, universeID)
	}
	wait()
}

func TestObjectProjector_CreatedAndRenamed(t *testing.T) {
	pool := newTestPool(t)
	p := universechanges.NewObjectProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(objectevents.AggregateType))

	objectID, universeID := uuid.New(), uuid.New()
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: objectID, AggregateType: objectevents.AggregateType, Version: 1,
		EventType: objectevents.TypeObjectCreated,
		Payload:   mustMarshal(t, objectevents.ObjectCreated{ID: objectID, Name: "Test Object", UniverseID: universeID, OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})
	if got, found := waitForChangeRow(t, pool, 1); !found || got != universeID {
		t.Fatalf("created: got %s, found=%v, want %s", got, found, universeID)
	}

	if _, err := pool.Exec(context.Background(),
		`INSERT INTO objects_read_model (id, name, universe_id, is_archived, updated_at) VALUES ($1, 'Test Object', $2, false, now())`,
		objectID, universeID,
	); err != nil {
		t.Fatalf("seed objects_read_model: %v", err)
	}
	publish(bus.Envelope{
		GlobalSeq: 2, AggregateID: objectID, AggregateType: objectevents.AggregateType, Version: 2,
		EventType: objectevents.TypeObjectRenamed,
		Payload:   mustMarshal(t, objectevents.ObjectRenamed{Name: "Renamed Object", OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})
	if got, found := waitForChangeRow(t, pool, 2); !found || got != universeID {
		t.Fatalf("renamed: got %s, found=%v, want %s", got, found, universeID)
	}
	wait()
}

func TestCharacterProjector_Created_ResolvesViaCampaign(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID := uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, 'Test Campaign', $2, $3, '', false, now())`,
		campaignID, universeID, uuid.New(),
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}

	p := universechanges.NewCharacterProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(characterevents.AggregateType))

	characterID, entityID, playerID := uuid.New(), uuid.New(), uuid.New()
	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: characterevents.AggregateType, Version: 1,
		EventType: characterevents.TypeCharacterCreated,
		Payload: mustMarshal(t, characterevents.CharacterCreated{
			ID: characterID, Name: "Aragorn", CampaignID: campaignID, EntityID: entityID,
			PlayerUserID: playerID, Info: "", OccurredAt: time.Now().UTC(),
		}),
		CreatedAt: time.Now().UTC(),
	})

	got, found := waitForChangeRow(t, pool, 1)
	if !found {
		t.Fatal("timed out waiting for the change row")
	}
	if got != universeID {
		t.Fatalf("got universe_id %s, want %s (resolved via the payload's CampaignID, then campaigns_read_model)", got, universeID)
	}
	wait()
}

func TestCharacterProjector_Renamed_ResolvesViaJoin(t *testing.T) {
	pool := newTestPool(t)
	campaignID, universeID, characterID, entityID, playerID := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, configuration, is_archived, updated_at)
		 VALUES ($1, 'Test Campaign', $2, $3, '', false, now())`,
		campaignID, universeID, uuid.New(),
	); err != nil {
		t.Fatalf("seed campaigns_read_model: %v", err)
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO characters_read_model (id, name, campaign_id, entity_id, player_user_id, info, is_archived, updated_at)
		 VALUES ($1, 'Aragorn', $2, $3, $4, '', false, now())`,
		characterID, campaignID, entityID, playerID,
	); err != nil {
		t.Fatalf("seed characters_read_model: %v", err)
	}

	p := universechanges.NewCharacterProjector()
	publish, wait := runProjector(t, pool, p, bus.Subject(characterevents.AggregateType))

	publish(bus.Envelope{
		GlobalSeq: 1, AggregateID: characterID, AggregateType: characterevents.AggregateType, Version: 2,
		EventType: characterevents.TypeCharacterRenamed,
		Payload:   mustMarshal(t, characterevents.CharacterRenamed{Name: "Strider", OccurredAt: time.Now().UTC()}),
		CreatedAt: time.Now().UTC(),
	})

	got, found := waitForChangeRow(t, pool, 1)
	if !found {
		t.Fatal("timed out waiting for the change row")
	}
	if got != universeID {
		t.Fatalf("got universe_id %s, want %s (resolved via the characters_read_model/campaigns_read_model join)", got, universeID)
	}
	wait()
}
```

The `characters_read_model` column list used above
(`id, name, campaign_id, entity_id, player_user_id, info, is_archived, updated_at`) has already
been confirmed against `internal/projection/character/migrations/0001_character_read_model.up.sql`
and `0002_character_info.up.sql` (the `info` column comes from the second migration, already
included in `testutil_test.go`'s init-script list above) — no further check needed here.

- [ ] **Step 9: Run this package's tests**

Run: `go test ./internal/projection/universechanges/... -v`
Expected: all 7 tests pass.

- [ ] **Step 10: Wire the five projectors into `cmd/projector/main.go`**

Add this import:

```go
universechangesprojection "github.com/timadorus/platform/internal/projection/universechanges"
```

Add five lines to the `projectors` slice literal, alongside the existing seven:

```go
		universechangesprojection.NewUniverseProjector(),
		universechangesprojection.NewCampaignProjector(),
		universechangesprojection.NewEntityProjector(),
		universechangesprojection.NewObjectProjector(),
		universechangesprojection.NewCharacterProjector(),
```

- [ ] **Step 11: Build, vet, test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: entirely clean.

- [ ] **Step 12: Commit**

```bash
git add internal/projection/universechanges/ cmd/projector/main.go
git commit -m "projection: add five change-feed projectors (Universe/Campaign/Entity/Object/Character)"
```

---

### Task 3: `internal/query/universechanges` repository

**Files:**
- Create: `internal/query/universechanges/repository.go`
- Create: `internal/query/universechanges/repository_test.go`

**Interfaces:**
- Consumes: Task 1's `universe_changes_read_model` schema.
- Produces: `universechanges.Repository`, `universechanges.Change{GlobalSeq, AggregateType,
  AggregateID, EventType, OccurredAt}`, `.Cursor(ctx, universeID) (int64, error)`,
  `.List(ctx, universeID, since int64) ([]Change, error)` — Task 4 depends on all of these.

- [ ] **Step 1: Add the repository**

Create `internal/query/universechanges/repository.go`:

```go
// Package universechanges reads the universe_changes_read_model table written by
// internal/projection/universechanges.
package universechanges

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Change struct {
	GlobalSeq     int64
	AggregateType string
	AggregateID   uuid.UUID
	EventType     string
	OccurredAt    time.Time
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// Cursor returns the current max global_seq recorded for universeID, or 0 if there are none
// yet — a client calls this once, on first load, so its first real poll (since=<this value>)
// never returns the Universe's entire history.
func (r *Repository) Cursor(ctx context.Context, universeID uuid.UUID) (int64, error) {
	var cursor int64
	if err := r.pool.QueryRow(ctx,
		`SELECT COALESCE(MAX(global_seq), 0) FROM universe_changes_read_model WHERE universe_id = $1`,
		universeID,
	).Scan(&cursor); err != nil {
		return 0, fmt.Errorf("query/universechanges: cursor for universe %s: %w", universeID, err)
	}
	return cursor, nil
}

// List returns changes to universeID strictly after since, ordered by global_seq ascending,
// capped at 20 rows per call (matching the existing Entity-search convention) so a client that
// falls behind for a while doesn't get flooded in one response.
func (r *Repository) List(ctx context.Context, universeID uuid.UUID, since int64) ([]Change, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT global_seq, aggregate_type, aggregate_id, event_type, occurred_at
		 FROM universe_changes_read_model
		 WHERE universe_id = $1 AND global_seq > $2
		 ORDER BY global_seq ASC
		 LIMIT 20`,
		universeID, since,
	)
	if err != nil {
		return nil, fmt.Errorf("query/universechanges: list for universe %s since %d: %w", universeID, since, err)
	}
	defer rows.Close()

	var out []Change
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.GlobalSeq, &c.AggregateType, &c.AggregateID, &c.EventType, &c.OccurredAt); err != nil {
			return nil, fmt.Errorf("query/universechanges: scan row: %w", err)
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/universechanges: iterate rows: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 2: Add tests**

Create `internal/query/universechanges/repository_test.go`:

```go
package universechanges_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/query/universechanges"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../../projection/universechanges/migrations/0001_universe_changes_read_model.up.sql",
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

func seedChange(t *testing.T, pool *pgxpool.Pool, globalSeq int64, universeID, aggregateID uuid.UUID, aggregateType, eventType string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO universe_changes_read_model (global_seq, universe_id, aggregate_type, aggregate_id, event_type, occurred_at)
		 VALUES ($1, $2, $3, $4, $5, now())`,
		globalSeq, universeID, aggregateType, aggregateID, eventType,
	); err != nil {
		t.Fatalf("seed change: %v", err)
	}
}

func TestRepository_Cursor_NoRows_ReturnsZero(t *testing.T) {
	pool := newTestPool(t)
	repo := universechanges.NewRepository(pool)

	cursor, err := repo.Cursor(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cursor != 0 {
		t.Fatalf("got cursor %d, want 0", cursor)
	}
}

func TestRepository_Cursor_ReturnsMaxGlobalSeq(t *testing.T) {
	pool := newTestPool(t)
	repo := universechanges.NewRepository(pool)
	universeID := uuid.New()
	seedChange(t, pool, 1, universeID, uuid.New(), "campaign", "campaign.created.v1")
	seedChange(t, pool, 5, universeID, uuid.New(), "entity", "entity.created.v1")
	seedChange(t, pool, 3, universeID, uuid.New(), "object", "object.created.v1")

	cursor, err := repo.Cursor(context.Background(), universeID)
	if err != nil {
		t.Fatalf("cursor: %v", err)
	}
	if cursor != 5 {
		t.Fatalf("got cursor %d, want 5", cursor)
	}
}

func TestRepository_List_OrderedAndScoped(t *testing.T) {
	pool := newTestPool(t)
	repo := universechanges.NewRepository(pool)
	universeA, universeB := uuid.New(), uuid.New()
	seedChange(t, pool, 1, universeA, uuid.New(), "campaign", "campaign.created.v1")
	seedChange(t, pool, 3, universeA, uuid.New(), "entity", "entity.created.v1")
	seedChange(t, pool, 2, universeA, uuid.New(), "object", "object.created.v1")
	seedChange(t, pool, 4, universeB, uuid.New(), "campaign", "campaign.created.v1")

	changes, err := repo.List(context.Background(), universeA, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(changes) != 3 {
		t.Fatalf("got %d changes, want 3 (must not include universe B's row)", len(changes))
	}
	if changes[0].GlobalSeq != 1 || changes[1].GlobalSeq != 2 || changes[2].GlobalSeq != 3 {
		t.Fatalf("got global_seq order %d,%d,%d, want 1,2,3", changes[0].GlobalSeq, changes[1].GlobalSeq, changes[2].GlobalSeq)
	}
}

func TestRepository_List_SinceExcludesUpToAndIncludingCursor(t *testing.T) {
	pool := newTestPool(t)
	repo := universechanges.NewRepository(pool)
	universeID := uuid.New()
	seedChange(t, pool, 1, universeID, uuid.New(), "campaign", "campaign.created.v1")
	seedChange(t, pool, 2, universeID, uuid.New(), "entity", "entity.created.v1")

	changes, err := repo.List(context.Background(), universeID, 1)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(changes) != 1 || changes[0].GlobalSeq != 2 {
		t.Fatalf("got %v, want exactly global_seq 2 (since=1 is exclusive)", changes)
	}
}

func TestRepository_List_CapsAt20(t *testing.T) {
	pool := newTestPool(t)
	repo := universechanges.NewRepository(pool)
	universeID := uuid.New()
	for i := int64(1); i <= 25; i++ {
		seedChange(t, pool, i, universeID, uuid.New(), "campaign", "campaign.created.v1")
	}

	changes, err := repo.List(context.Background(), universeID, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(changes) != 20 {
		t.Fatalf("got %d changes, want 20 (capped)", len(changes))
	}
	if changes[0].GlobalSeq != 1 || changes[19].GlobalSeq != 20 {
		t.Fatalf("got first/last global_seq %d/%d, want 1/20", changes[0].GlobalSeq, changes[19].GlobalSeq)
	}
}
```

- [ ] **Step 3: Run this package's tests**

Run: `go test ./internal/query/universechanges/... -v`
Expected: all 5 tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/query/universechanges/
git commit -m "query: add internal/query/universechanges, reading universe_changes_read_model"
```

---

### Task 4: OpenAPI endpoints, codegen, and `internal/httpapi/query` wiring

**Files:**
- Modify: `api/query/openapi.yaml`
- Regenerate: `api/query/gen/server.gen.go`
- Modify: `internal/httpapi/query/server.go`
- Modify: `cmd/query-api/main.go`

**Interfaces:**
- Consumes: Task 3's `universechanges.Repository`/`Change`.
- Produces: `GET /universes/{universeId}/changes/cursor`, `GET /universes/{universeId}/changes` —
  no later Go task depends on this; it's the API surface Tasks 5-7 (SPA + e2e) consume via HTTP.

- [ ] **Step 1: Add the OpenAPI schema and paths**

In `api/query/openapi.yaml`, add this schema to the `schemas:` section, anywhere alongside the
others (e.g. immediately after `RulesetTableRow`):

```yaml
    UniverseChangeCursor:
      type: object
      required: [globalSeq]
      properties:
        globalSeq:
          type: integer
          format: int64

    UniverseChange:
      type: object
      required: [globalSeq, aggregateType, aggregateId, eventType, occurredAt]
      properties:
        globalSeq:
          type: integer
          format: int64
        aggregateType:
          type: string
        aggregateId:
          type: string
          format: uuid
        eventType:
          type: string
        occurredAt:
          type: string
          format: date-time
```

Add these two paths, immediately after the existing `/universes/{universeId}/objects:` path
block:

```yaml
  /universes/{universeId}/changes/cursor:
    get:
      operationId: getUniverseChangesCursor
      summary: Get the current change cursor for a Universe.
      parameters:
        - $ref: "#/components/parameters/UniverseId"
      responses:
        "200":
          description: The current cursor.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/UniverseChangeCursor"

  /universes/{universeId}/changes:
    get:
      operationId: listUniverseChanges
      summary: List changes to this Universe and everything inside it, after a given cursor.
      parameters:
        - $ref: "#/components/parameters/UniverseId"
        - name: since
          in: query
          required: true
          schema:
            type: integer
            format: int64
      responses:
        "200":
          description: Changes, ordered by globalSeq ascending, capped at 20 per call.
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/UniverseChange"
```

- [ ] **Step 2: Regenerate the query API's generated server code**

Run: `go generate ./api/query/...`
Expected: `api/query/gen/server.gen.go` changes (new types for
`GetUniverseChangesCursor`/`ListUniverseChanges`). Confirm with
`git diff --stat api/query/gen/server.gen.go` that it changed; do not hand-edit this file.

- [ ] **Step 3: Wire the new repository into `Server`**

In `internal/httpapi/query/server.go`, add this import:

```go
universechangesquery "github.com/timadorus/platform/internal/query/universechanges"
```

Add a field to the `Server` struct and a parameter to `NewServer` (following this file's existing
`rulesetTables` field/parameter, added by an earlier branch, as the pattern to copy):

```go
type Server struct {
	universe      *universequery.Repository
	user          *userquery.Repository
	campaign      *campaignquery.Repository
	entity        *entityquery.Repository
	character     *characterquery.Repository
	object        *objectquery.Repository
	ruleset       *rulesetquery.Repository
	rulesetTables *rulesettablesquery.Repository
	changes       *universechangesquery.Repository
}

func NewServer(
	universeRepo *universequery.Repository,
	userRepo *userquery.Repository,
	campaignRepo *campaignquery.Repository,
	entityRepo *entityquery.Repository,
	characterRepo *characterquery.Repository,
	objectRepo *objectquery.Repository,
	rulesetRepo *rulesetquery.Repository,
	rulesetTablesRepo *rulesettablesquery.Repository,
	changesRepo *universechangesquery.Repository,
) *Server {
	return &Server{
		universe:      universeRepo,
		user:          userRepo,
		campaign:      campaignRepo,
		entity:        entityRepo,
		character:     characterRepo,
		object:        objectRepo,
		ruleset:       rulesetRepo,
		rulesetTables: rulesetTablesRepo,
		changes:       changesRepo,
	}
}
```

Add these two handlers, anywhere alongside the existing ones (e.g. after
`GetRulesetTableRow`):

```go
func (s *Server) GetUniverseChangesCursor(ctx context.Context, request gen.GetUniverseChangesCursorRequestObject) (gen.GetUniverseChangesCursorResponseObject, error) {
	cursor, err := s.changes.Cursor(ctx, request.UniverseId)
	if err != nil {
		return nil, err
	}
	return gen.GetUniverseChangesCursor200JSONResponse{GlobalSeq: cursor}, nil
}

func (s *Server) ListUniverseChanges(ctx context.Context, request gen.ListUniverseChangesRequestObject) (gen.ListUniverseChangesResponseObject, error) {
	changes, err := s.changes.List(ctx, request.UniverseId, request.Params.Since)
	if err != nil {
		return nil, err
	}
	out := make([]gen.UniverseChange, len(changes))
	for i, c := range changes {
		out[i] = gen.UniverseChange{
			GlobalSeq:     c.GlobalSeq,
			AggregateType: c.AggregateType,
			AggregateId:   c.AggregateID,
			EventType:     c.EventType,
			OccurredAt:    c.OccurredAt,
		}
	}
	return gen.ListUniverseChanges200JSONResponse(out), nil
}
```

If the generated request/response type names or field names (`request.Params.Since`,
`GetUniverseChangesCursor200JSONResponse{GlobalSeq: ...}`, etc.) don't match exactly what Step 2
produced, read the real generated names from `api/query/gen/server.gen.go` and use those instead
— oapi-codegen capitalizes `operationId`/query-parameter names mechanically, matching every
existing handler's own naming, so this should already be right, but confirm against the actual
file rather than assuming.

- [ ] **Step 4: Wire the new repository into `cmd/query-api/main.go`**

Add this import:

```go
universechangesquery "github.com/timadorus/platform/internal/query/universechanges"
```

Add, alongside the existing repository constructions:

```go
	changesRepo := universechangesquery.NewRepository(pool)
```

Update the `httpquery.NewServer(...)` call to pass it as the new final argument:

```go
	server := httpquery.NewServer(universeRepo, userRepo, campaignRepo, entityRepo, characterRepo, objectRepo, rulesetRepo, rulesetTablesRepo, changesRepo)
```

- [ ] **Step 5: Build and typecheck**

Run: `go build ./... && go vet ./...`
Expected: entirely clean.

- [ ] **Step 6: Commit**

```bash
git add api/query/openapi.yaml api/query/gen/server.gen.go internal/httpapi/query/server.go cmd/query-api/main.go
git commit -m "query-api: expose the per-Universe change feed via GET /universes/{id}/changes[/cursor]"
```

---

### Task 5: `useChangeFeed` composable and `WorkspaceView.vue` wiring

**Files:**
- Create: `web/src/composables/useChangeFeed.ts`
- Modify: `web/src/views/WorkspaceView.vue`

**Interfaces:**
- Consumes: Task 4's `GET /universes/{id}/changes/cursor` and `GET /universes/{id}/changes`.
- Produces: `useChangeFeed()` returning `{ lastChange: Ref<AggregateChange | null>, start(universeId: string): Promise<void>, stop(): void }`;
  `WorkspaceView.vue` `provide('lastAggregateChange', lastChange)` — Task 6 depends on this
  provide key and the `AggregateChange` shape.

- [ ] **Step 1: Add the composable**

Create `web/src/composables/useChangeFeed.ts`:

```ts
import { ref } from 'vue'
import { getQueryClient } from '@/api/client'

export interface AggregateChange {
  globalSeq: number
  aggregateType: string
  aggregateId: string
  eventType: string
  occurredAt: string
}

// POLL_INTERVAL_MS is deliberately much slower than the existing 750ms eventual-consistency
// polls (waitForUser/waitForCharacter/etc.) — this is a low-urgency background check for changes
// made elsewhere, not "wait for my own just-submitted action".
const POLL_INTERVAL_MS = 5000

// useChangeFeed polls GET /universes/{id}/changes on an interval and exposes the single most
// recent change as one Ref, following the same provide/watch(ref) idiom this codebase already
// uses for sidebarRefreshSignal/pendingEntityId — deliberately not a new pub-sub/event-emitter
// abstraction. Additive to those mechanisms, not a replacement: this only ever reports changes,
// it never itself refreshes anything — each consumer decides what "this aggregate changed"
// means for its own data.
export function useChangeFeed() {
  const lastChange = ref<AggregateChange | null>(null)
  let cursor = 0
  let currentUniverseId = ''
  let timer: ReturnType<typeof setInterval> | null = null
  let inFlight = false

  async function poll() {
    if (inFlight || !currentUniverseId) return
    inFlight = true
    try {
      const { data, error } = await getQueryClient().GET('/universes/{universeId}/changes', {
        params: { path: { universeId: currentUniverseId }, query: { since: cursor } },
      })
      if (!error && data) {
        for (const change of data as AggregateChange[]) {
          cursor = change.globalSeq
          lastChange.value = change
        }
      }
    } finally {
      inFlight = false
    }
  }

  async function start(universeId: string) {
    stop()
    currentUniverseId = universeId
    cursor = 0
    const { data } = await getQueryClient().GET('/universes/{universeId}/changes/cursor', {
      params: { path: { universeId } },
    })
    cursor = data?.globalSeq ?? 0
    timer = setInterval(poll, POLL_INTERVAL_MS)
  }

  function stop() {
    if (timer) clearInterval(timer)
    timer = null
    currentUniverseId = ''
  }

  return { lastChange, start, stop }
}
```

- [ ] **Step 2: Wire it into `WorkspaceView.vue`**

Add this import:

```ts
import { useChangeFeed } from '@/composables/useChangeFeed'
```

Add `onUnmounted` to the existing `vue` import line (currently `computed, onMounted, provide,
ref, watch` — add `onUnmounted`).

Add, alongside the existing `sidebarRefreshSignal`/`pendingEntityId` provides:

```ts
const { lastChange: lastAggregateChange, start: startChangeFeed, stop: stopChangeFeed } = useChangeFeed()
provide('lastAggregateChange', lastAggregateChange)
```

Add, alongside the existing `onMounted(load)`/`watch([universeId, campaignId], load)`:

```ts
onMounted(() => startChangeFeed(universeId.value))
watch(universeId, startChangeFeed)
onUnmounted(stopChangeFeed)
```

(`startChangeFeed`'s own `stop()`-then-restart at the top of `start()` makes a second
`onMounted`/`watch` firing in quick succession safe — no separate guard needed here.)

- [ ] **Step 3: Typecheck and build**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean. `lastAggregateChange` is provided but not yet consumed by anything — that's
Task 6.

- [ ] **Step 4: Run the existing e2e suite**

Run (from `web/`): `npm run test:e2e`
Expected: the existing tests still pass unchanged — this task adds a poller nothing currently
reacts to or asserts on, and the mock backend hasn't been given the two new routes yet (Task 7),
so confirm the poll's own failure mode is silent (the composable already treats an `error`
response as "skip this tick, try again next interval" — verify this is actually how the existing
tests' unmocked-GET-returns-`200 []` fallback behaves against these two new paths, i.e. `/cursor`
returns `[]` rather than the expected `{globalSeq}` object; if this makes `poll()`/`start()` throw
instead of harmlessly no-op, note it in your report — it would mean Task 7's mock routes need to
land before this task can be considered fully inert against the existing suite, which would be
worth flagging rather than silently reordering).

- [ ] **Step 5: Commit**

```bash
git add web/src/composables/useChangeFeed.ts web/src/views/WorkspaceView.vue
git commit -m "web: add useChangeFeed and provide lastAggregateChange from WorkspaceView (not yet consumed)"
```

---

### Task 6: Wire list panels and detail views to `lastAggregateChange`

**Files:**
- Modify: `web/src/components/layout/CharactersPanel.vue`
- Modify: `web/src/components/layout/EntitiesPanel.vue`
- Modify: `web/src/components/layout/ObjectsPanel.vue`
- Modify: `web/src/views/CharacterDetailView.vue`
- Modify: `web/src/views/EntityDetailView.vue`
- Modify: `web/src/views/ObjectDetailView.vue`
- Modify: `web/src/views/CampaignOverviewPanel.vue`
- Modify: `web/src/views/UniverseOverviewPanel.vue`

**Interfaces:**
- Consumes: Task 5's `'lastAggregateChange'` provide key and `AggregateChange` shape.

- [ ] **Step 1: Wire `CharactersPanel.vue`** (list panel — react to any `character` change)

Add `AggregateChange` to this file's existing `useChangeFeed` import if needed — actually this
file only needs the injected `Ref`, not the composable itself. Add, alongside the existing
`sidebarRefreshSignal`/`pendingEntityId` injects:

```ts
import type { AggregateChange } from '@/composables/useChangeFeed'
```

```ts
const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'character') refresh()
  })
}
```

- [ ] **Step 2: Wire `EntitiesPanel.vue`** (list panel)

Same shape, reacting to `aggregateType === 'entity'` by re-running its existing search:

```ts
import type { AggregateChange } from '@/composables/useChangeFeed'
```

```ts
const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'entity') search(props.universeId, currentQuery.value)
  })
}
```

- [ ] **Step 3: Wire `ObjectsPanel.vue`** (list panel)

Same shape, `aggregateType === 'object'`:

```ts
import type { AggregateChange } from '@/composables/useChangeFeed'
```

```ts
const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'object') search(props.universeId, currentQuery.value)
  })
}
```

- [ ] **Step 4: Wire `CharacterDetailView.vue`** (detail view — react only to its own exact id)

Add, alongside the existing `bumpSidebarRefresh` inject:

```ts
import type { AggregateChange } from '@/composables/useChangeFeed'
```

```ts
const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'character' && change.aggregateId === characterId.value) load()
  })
}
```

- [ ] **Step 5: Wire `EntityDetailView.vue`**

Same shape:

```ts
import type { AggregateChange } from '@/composables/useChangeFeed'
```

```ts
const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'entity' && change.aggregateId === entityId.value) load()
  })
}
```

- [ ] **Step 6: Wire `ObjectDetailView.vue`**

Same shape — this file's own `load`/`objectId` names have already been confirmed to match
`EntityDetailView.vue`'s exactly:

```ts
import type { AggregateChange } from '@/composables/useChangeFeed'
```

```ts
const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'object' && change.aggregateId === objectId.value) load()
  })
}
```

- [ ] **Step 7: Wire `CampaignOverviewPanel.vue`**

This view's own load function is `load`, and its own id computed ref is `campaignId` (both
already confirmed against the current file). Add:

```ts
import type { AggregateChange } from '@/composables/useChangeFeed'
```

```ts
const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'campaign' && change.aggregateId === campaignId.value) load()
  })
}
```

- [ ] **Step 8: Wire `UniverseOverviewPanel.vue`**

This view's own load function is `load`, id computed ref is `universeId` (both already confirmed
against the current file). Add:

```ts
import type { AggregateChange } from '@/composables/useChangeFeed'
```

```ts
const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'universe' && change.aggregateId === universeId.value) load()
  })
}
```

- [ ] **Step 9: Typecheck, build, and run the existing e2e suite**

Run (from `web/`): `npm run typecheck && npm run build && npm run test:e2e`
Expected: all clean, all existing tests still pass unchanged — nothing in this task changes
behavior unless `lastAggregateChange` actually emits a matching change, which nothing in the
existing mocked tests triggers yet (Task 7 adds that coverage).

- [ ] **Step 10: Commit**

```bash
git add web/src/components/layout/CharactersPanel.vue web/src/components/layout/EntitiesPanel.vue web/src/components/layout/ObjectsPanel.vue web/src/views/CharacterDetailView.vue web/src/views/EntityDetailView.vue web/src/views/ObjectDetailView.vue web/src/views/CampaignOverviewPanel.vue web/src/views/UniverseOverviewPanel.vue
git commit -m "web: react to lastAggregateChange in every list panel and detail view"
```

---

### Task 7: Mock backend support and an e2e test for an externally-made change

**Files:**
- Modify: `web/e2e/support/mockBackend.ts`
- Create: `web/e2e/universe-change-feed.spec.ts`

**Interfaces:**
- Consumes: Tasks 5-6's SPA wiring.
- Produces: mock routes for `GET /api/query/universes/:universeId/changes/cursor` and
  `GET /api/query/universes/:universeId/changes`, plus a test helper to inject a change the mock
  will report on the next poll.

- [ ] **Step 1: Add `MockState` support for injected changes**

In `web/e2e/support/mockBackend.ts`, add to `MockState`:

```ts
  // changes simulates externally-made changes (another tab/user) for universe-change-feed.spec.ts
  // — tests push onto this array directly; the mock routes below serve from it exactly like a
  // real universe_changes_read_model would.
  changes: { globalSeq: number; universeId: string; aggregateType: string; aggregateId: string; eventType: string; occurredAt: string }[]
```

and to `createMockState`'s defaults:

```ts
    changes: [],
```

- [ ] **Step 2: Add the two mock routes**

Add these GET routes to the `// ---- query API ----` section, immediately after the existing
`/api/query/universes/:universeId/campaigns` route:

```ts
    if (method === 'GET' && (m = matchPath('/api/query/universes/:universeId/changes/cursor', p))) {
      const relevant = state.changes.filter((c) => c.universeId === m!.params.universeId)
      const cursor = relevant.length ? Math.max(...relevant.map((c) => c.globalSeq)) : 0
      return json(route, { globalSeq: cursor })
    }
    if (method === 'GET' && (m = matchPath('/api/query/universes/:universeId/changes', p))) {
      const since = Number(query.get('since') ?? '0')
      const matches = state.changes
        .filter((c) => c.universeId === m!.params.universeId && c.globalSeq > since)
        .sort((a, b) => a.globalSeq - b.globalSeq)
        .slice(0, 20)
      return json(route, matches)
    }
```

- [ ] **Step 3: Write the test**

Create `web/e2e/universe-change-feed.spec.ts`:

```ts
import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(overrides: Partial<MockState> = {}): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [
      { id: 'c1', universeId: 'u1', name: 'Test Campaign', rulesetId: 'r1', isArchived: false },
    ],
    rulesets: [{ id: 'r1', name: 'Test Ruleset' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    gamemasterIds: ['user-1'],
    characters: [
      { id: 'ch1', name: 'Aragorn', campaignId: 'c1', entityId: 'e1', playerUserId: 'user-1', isArchived: false },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('an externally-made Entity change is picked up by the Entities sidebar without any local action', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')
  await expect(page.getByText('Aragorn')).toBeVisible()

  // Simulate another tab/user creating a new Entity in this Universe: add it to state directly
  // (bypassing any command route — this test is about the change feed noticing it, not about
  // creation itself) and record a matching change-log row the poller will pick up.
  state.entities.push({ id: 'e2', name: 'Gandalf', universeId: 'u1', isArchived: false })
  state.changes.push({
    globalSeq: 1,
    universeId: 'u1',
    aggregateType: 'entity',
    aggregateId: 'e2',
    eventType: 'entity.created.v1',
    occurredAt: new Date().toISOString(),
  })

  // useChangeFeed polls every 5s — wait comfortably past that instead of asserting immediately.
  await expect(page.getByText('Gandalf')).toBeVisible({ timeout: 10000 })
})
```

Note on timing: this test takes at least one real 5-second poll interval to pass — this is
inherent to the feature (a background, low-urgency poller), not a flaw in the test. If this
proves too slow for the suite's overall runtime, that's a signal worth reporting rather than
silently working around (e.g. by lowering `POLL_INTERVAL_MS`) — flag it in your report rather
than changing the interval unilaterally, since 5000ms was a deliberate design decision.

- [ ] **Step 4: Run the full suite**

Run (from `web/`): `npm run test:e2e`
Expected: all existing tests plus this new one pass. Run it twice to confirm no flakes (this test
depends on real wall-clock timing, so flakiness here is worth taking seriously, not dismissing).

- [ ] **Step 5: Typecheck and build**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add web/e2e/support/mockBackend.ts web/e2e/universe-change-feed.spec.ts
git commit -m "web/e2e: test that an externally-made change is picked up via the change feed"
```

---

## Final Verification

- `go build ./... && go vet ./... && go test ./...` clean across the whole repo.
- `go generate ./...` regenerates `api/query/gen` with no further diff.
- `npm run typecheck && npm run build && npm run test:e2e` (from `web/`) all clean, no flakes
  across at least two full runs.
- Grep the whole repo for `lastAggregateChange` and confirm it's provided exactly once
  (`WorkspaceView.vue`) and injected in exactly the eight files Task 6 touched.
- Confirm `docker build -f Dockerfile.migrate` still succeeds and the migration image contains
  `internal/projection/universechanges/migrations` (re-run Task 1 Step 4's verification once more
  against the final state of the branch, not just right after Task 1).
