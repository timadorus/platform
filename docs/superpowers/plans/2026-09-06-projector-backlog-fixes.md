# Projector Backlog Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Resolve all three items in `docs/BACKLOG.md`'s "projector" section: give `cmd/projector` an
explicit, configurable connection budget (mirroring `cmd/timadorus-engine`'s already-reviewed
pattern); build a standalone tool that safely performs a full read-model rebuild (deleting the
right JetStream durable consumers, not just resetting Postgres checkpoints, in the required
base-then-change-feed order); and correct the third item, which turns out to already be fixed.

**Architecture:** A new `internal/projection/registry` package becomes the single source of truth
for "all 12 projectors, split into Base and ChangeFeed" — both `cmd/projector/main.go` and the new
`cmd/rebuild-read-models` binary use it, so there is never a second hardcoded projector list to
drift out of sync. A new `internal/rebuildreadmodels` package holds the actual rebuild mechanics
(Postgres checkpoint reset/wait, NATS JetStream consumer deletion), independently unit-tested
against real Postgres and NATS testcontainers; `cmd/rebuild-read-models/main.go` is a thin CLI
wrapper around it, matching this codebase's established `main`-package-plus-testable-library-package
split (see `test/e2e/cmd/devcluster` + `test/e2e/internal`).

**Tech Stack:** Go, `pgx`, `nats.go` (JetStream admin API), `testcontainers-go` (Postgres + the new
`modules/nats`).

## Global Constraints

- `cmd/timadorusctl` is not touched by this plan — the new rebuild tool is a separate binary
  (`cmd/rebuild-read-models`), per this plan's own design spec's Decision 2. A BACKLOG note already
  exists flagging the resulting naming inconsistency for a future pass; not resolved here.
- `internal/projection` itself (the generic Router/Projector/checkpoint framework) is not modified
  — the new `registry` package sits alongside it, importing it, never the reverse, preserving this
  codebase's existing "adding a projection never changes the framework" principle.
- Resetting a Postgres checkpoint alone is known to be insufficient for a real rebuild (the
  JetStream durable consumer's own delivery cursor is independent of it) — every rebuild path in
  this plan does both: delete the consumer, then reset the checkpoint.
- `go build ./... && go vet ./... && go test -count=1 ./...` must stay clean after every task.

---

### Task 1: `cmd/projector` connection budget

**Files:**
- Modify: `internal/config/config.go`
- Modify: `cmd/projector/main.go`

**Interfaces:**
- Produces: `LoadProjector() (Projector, error)` — signature change (was `Projector` with no
  error), mirroring `LoadTimadorusEngine`'s existing shape. One call site to update
  (`cmd/projector/main.go`), confirmed via repo-wide grep.

- [ ] **Step 1: Add `PoolMaxConns` to `config.Projector` and thread it through `LoadProjector`**

In `internal/config/config.go`, replace the `Projector` struct and `LoadProjector` function:

```go
type Projector struct {
	// HTTPAddr serves /healthz, /readyz, /metrics only — the projector has no public API,
	// so unlike command-api/query-api this port carries no OpenAPI-defined routes and needs
	// no auth middleware.
	HTTPAddr    string
	DatabaseURL string
	NATSURL     string
	// PoolMaxConns caps the shared Postgres connection pool across all 12 projectors sharing
	// this binary (see cmd/projector/main.go's "Connection budget" comment for the reasoning
	// behind the default of 16). Configurable via PROJECTOR_POOL_MAX_CONNS so a growing
	// projector count can be given headroom without a code change or redeploy of a new binary.
	// Leaving the variable unset defaults to 16; explicitly setting it to something invalid
	// (unparseable, zero, or negative) fails LoadProjector with a named error instead of
	// silently substituting the default — see parsePoolMaxConns.
	PoolMaxConns int32
}

// LoadProjector returns an error only when PROJECTOR_POOL_MAX_CONNS is explicitly set to
// something invalid (see parsePoolMaxConns) — every other field is best-effort, matching this
// package's other Load* functions. Leaving the variable unset is not an error.
func LoadProjector() (Projector, error) {
	poolMaxConns, err := parsePoolMaxConns("PROJECTOR_POOL_MAX_CONNS", 16)
	if err != nil {
		return Projector{}, err
	}
	return Projector{
		HTTPAddr:     getEnv("PROJECTOR_ADDR", ":8083"),
		DatabaseURL:  getEnv("DATABASE_URL", "postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable"),
		NATSURL:      getEnv("NATS_URL", "nats://localhost:4222"),
		PoolMaxConns: poolMaxConns,
	}, nil
}
```

(`parsePoolMaxConns` is already generic and shared — no changes needed there.)

Run: `go build ./internal/config/...` — expect a compile error at `cmd/projector/main.go`'s call
site (expected; fixed in Step 2).

- [ ] **Step 2: Update `cmd/projector/main.go`'s pool construction and doc comments**

Replace:

```go
func run(ctx context.Context, logger *slog.Logger) error {
	cfg := config.LoadProjector()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
```

with:

```go
func run(ctx context.Context, logger *slog.Logger) error {
	cfg, err := config.LoadProjector()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// Connection budget: each in-flight Router.Handle call holds exactly one pool connection —
	// the universechanges projectors resolve on the ambient tx rather than acquiring a second
	// (see postgres.Store.Load), same as every other projector here. With 12 projectors
	// registered below, a simultaneous cold-start replay of all of them could in principle need
	// up to 12 connections at once; 16 gives headroom above that plus /readyz's own Ping.
	// Configurable via PROJECTOR_POOL_MAX_CONNS (internal/config.LoadProjector) if the projector
	// count grows further, with no code change required.
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
```

Add `"fmt"` to the import block — confirmed not already imported in this file (its current import
block runs `"context"`, `"log/slog"`, `"net/http"`, `"os"`, `"os/signal"`, `"sync"`, `"syscall"`,
`"time"`, then the three-import group of external packages, then the `github.com/timadorus/...`
group — add `"fmt"` alongside the other stdlib imports).

Run: `go build ./... && go vet ./...` — expect clean.

- [ ] **Step 3: Run full regression check and commit**

Run: `go build ./... && go vet ./... && go test -count=1 ./...` — expect clean.

```bash
git add internal/config/config.go cmd/projector/main.go
git commit -m "projector: add an explicit, configurable connection pool budget"
```

---

### Task 2: Correct the stale "no CI check" BACKLOG item

**Files:**
- Modify: `docs/BACKLOG.md`

- [ ] **Step 1: Mark the item as already-fixed**

The "verify generated API clients are up to date" step in `.github/workflows/ci.yml`'s `web-build`
job (added in commit `05a84b7`) already does exactly what this item asks for, and predates the
item itself (added in commit `970e127`) — it was simply never reconciled. Replace:

```markdown
- [ ] **No CI check that `web/src/api/{query,command}.types.ts` stay in sync with the OpenAPI
  specs.** `web/package.json`'s `npm run generate` (via `web/scripts/generate-api-clients.mjs`)
  already regenerates both files from `api/query/openapi.yaml` and `api/command/openapi.yaml` —
  that command already exists and is current. What's still missing is a CI step that runs it and
  fails the build on a diff, so a spec change without a regenerate can land unnoticed (as it did
  under an earlier, already-merged branch).
```

with:

```markdown
- [x] **Already fixed** (`05a84b7`, predates this entry). `.github/workflows/ci.yml`'s `web-build`
  job already has a "verify generated API clients are up to date" step: `npm run generate` followed
  by `git diff --exit-code -- src/api/command.types.ts src/api/query.types.ts`. This entry was
  simply never reconciled against that existing check — no code change needed.
```

- [ ] **Step 2: Commit**

```bash
git add docs/BACKLOG.md
git commit -m "docs: correct the stale 'no CI check' BACKLOG item — it was already fixed"
```

---

### Task 3: Extract a shared projector registry and export `bus.DurableName`

**Files:**
- Create: `internal/projection/registry/registry.go`
- Modify: `cmd/projector/main.go`
- Modify: `internal/bus/nats.go`

**Interfaces:**
- Produces: `registry.Base() []projection.Projector`, `registry.ChangeFeed() []projection.Projector`,
  `registry.All() []projection.Projector` — used by `cmd/projector/main.go` (Step 1 of this task)
  and, in Task 5, by `cmd/rebuild-read-models`.
- Produces: `bus.DurableName(processorName, subject string) string` — used by `NewSubscriber`
  (refactored in this task) and, in Task 4, by `internal/rebuildreadmodels`.

Both changes in this task are pure refactors with zero external behavior change — no new tests
needed beyond confirming the existing suite stays green.

- [ ] **Step 1: Create `internal/projection/registry/registry.go`**

```go
// Package registry is the single source of truth for "every projector this platform registers,
// split into Base and ChangeFeed" — both cmd/projector/main.go and cmd/rebuild-read-models use it,
// so there is never a second hardcoded projector list that could drift out of sync with the real
// one. This package imports internal/projection and every concrete projector package; none of
// them import this package back, so this stays a leaf in the import graph, same as
// cmd/projector/main.go's own registration list was before this refactor — internal/projection
// itself (the generic framework) is untouched and still knows nothing about any concrete
// projector, preserving its own "adding a projection never changes the framework" principle.
package registry

import (
	"github.com/timadorus/platform/internal/projection"
	campaignprojection "github.com/timadorus/platform/internal/projection/campaign"
	characterprojection "github.com/timadorus/platform/internal/projection/character"
	entityprojection "github.com/timadorus/platform/internal/projection/entity"
	objectprojection "github.com/timadorus/platform/internal/projection/object"
	rulesetprojection "github.com/timadorus/platform/internal/projection/ruleset"
	universeprojection "github.com/timadorus/platform/internal/projection/universe"
	universechangesprojection "github.com/timadorus/platform/internal/projection/universechanges"
	userprojection "github.com/timadorus/platform/internal/projection/user"
)

// Base returns the 7 base read-model projectors, one per aggregate type. A fresh slice of fresh
// instances every call — these are cheap, stateless constructors, and callers (cmd/projector,
// run once per process; cmd/rebuild-read-models, run once per invocation) never need to share an
// instance across calls.
func Base() []projection.Projector {
	return []projection.Projector{
		universeprojection.NewProjector(),
		userprojection.NewProjector(),
		campaignprojection.NewProjector(),
		entityprojection.NewProjector(),
		characterprojection.NewProjector(),
		objectprojection.NewProjector(),
		rulesetprojection.NewProjector(),
	}
}

// ChangeFeed returns the 5 universe-change-feed projectors (internal/projection/universechanges).
// A full read-model rebuild must reset Base before ChangeFeed, never the reverse — see
// docs/BACKLOG.md's "projector" section for why. Registration order in cmd/projector/main.go
// itself has no runtime ordering effect (the Router processes each projector's own subjects
// independently), so this split matters only for a full rebuild, not for normal operation.
func ChangeFeed() []projection.Projector {
	return []projection.Projector{
		universechangesprojection.NewUniverseProjector(),
		universechangesprojection.NewCampaignProjector(),
		universechangesprojection.NewEntityProjector(),
		universechangesprojection.NewObjectProjector(),
		universechangesprojection.NewCharacterProjector(),
	}
}

// All returns every registered projector, Base then ChangeFeed.
func All() []projection.Projector {
	return append(Base(), ChangeFeed()...)
}
```

- [ ] **Step 2: Refactor `cmd/projector/main.go` to use the registry**

Replace:

```go
	// Adding a new projection is exactly one line here — internal/projection itself never
	// changes (plan §7's open/closed requirement). (This feature needed five — see
	// docs/superpowers/specs/2026-09-02-universe-change-feed-design.md.)
	projectors := []projection.Projector{
		universeprojection.NewProjector(),
		userprojection.NewProjector(),
		campaignprojection.NewProjector(),
		entityprojection.NewProjector(),
		characterprojection.NewProjector(),
		objectprojection.NewProjector(),
		rulesetprojection.NewProjector(),
		universechangesprojection.NewUniverseProjector(),
		universechangesprojection.NewCampaignProjector(),
		universechangesprojection.NewEntityProjector(),
		universechangesprojection.NewObjectProjector(),
		universechangesprojection.NewCharacterProjector(),
	}
```

with:

```go
	// Adding a new projection is exactly one line, in internal/projection/registry — that
	// package (not this file, not internal/projection itself) is now the single source of
	// truth for "every projector this platform registers" (plan §7's open/closed requirement
	// still holds; the shared list also lets cmd/rebuild-read-models reuse it exactly, see that
	// binary's own doc comment).
	projectors := registry.All()
```

Remove the now-unused per-projector imports (`campaignprojection`, `characterprojection`,
`entityprojection`, `objectprojection`, `rulesetprojection`, `universeprojection`,
`universechangesprojection`, `userprojection`) and add
`"github.com/timadorus/platform/internal/projection/registry"`. The `projection` import itself
stays — `projection.NewRouter` is still called below.

Run: `go build ./... && go vet ./...` — expect clean (this is a pure refactor; `cmd/projector`'s
runtime behavior is unchanged — same 12 projectors, same order).

- [ ] **Step 3: Export `bus.DurableName` and refactor `NewSubscriber` to use it**

In `internal/bus/nats.go`, add this new exported function (placed after `Subject`, before
`NewPublisher`):

```go
// DurableName computes the JetStream durable consumer name NewSubscriber assigns for a given
// processor name and subject — exported so tooling that needs to address the exact same durable
// consumer directly (cmd/rebuild-read-models, which deletes consumers to force a full replay —
// see internal/rebuildreadmodels) never has to duplicate or guess this naming convention.
func DurableName(processorName, subject string) string {
	return processorName + "_" + subject
}
```

Replace `NewSubscriber`'s `DurableCalculator`:

```go
			DurableCalculator: func(prefix, topic string) string {
				return prefix + "_" + topic
			},
```

with:

```go
			DurableCalculator: DurableName,
```

(`DurableName`'s signature `(processorName, subject string) string` matches
`DurableCalculator`'s own `func(prefix, topic string) string` shape exactly, so it can be passed
directly — no wrapper closure needed.)

Run: `go build ./... && go vet ./...` — expect clean (pure refactor; `NewSubscriber`'s external
behavior and the actual durable names it produces are unchanged).

- [ ] **Step 4: Run full regression check and commit**

Run: `go build ./... && go vet ./... && go test -count=1 ./...` — expect clean, including every
existing `internal/projection/...` and `internal/engine/timadorus/...` test (none of these touch
projector registration or NATS naming directly, so none should be affected).

```bash
git add internal/projection/registry/registry.go cmd/projector/main.go internal/bus/nats.go
git commit -m "projection, bus: extract a shared projector registry; export bus.DurableName"
```

---

### Task 4: `internal/rebuildreadmodels` — the rebuild mechanics, independently tested

**Files:**
- Create: `internal/rebuildreadmodels/rebuildreadmodels.go`
- Create: `internal/rebuildreadmodels/rebuildreadmodels_test.go`
- Create: `internal/rebuildreadmodels/consumer_test.go`
- Modify: `go.mod`, `go.sum`

**Depends on:** Task 3 (`bus.DurableName`).

**Interfaces:**
- Produces: `SetCheckpoint`, `ResetCheckpoint`, `CurrentCheckpoint`, `MaxGlobalSeq`,
  `WaitForCatchUp`, `DeleteConsumer` — all consumed by `cmd/rebuild-read-models` in Task 5.

- [ ] **Step 1: Add the new test dependency**

```bash
go get github.com/testcontainers/testcontainers-go/modules/nats@v0.43.0
go mod tidy
```

Expected: `go.mod` gains a new direct entry for
`github.com/testcontainers/testcontainers-go/modules/nats v0.43.0`, and
`github.com/nats-io/nats.go`'s existing entry loses its `// indirect` marker (this package will
import it directly in Step 2). `go.sum` updates accordingly. Run `go build ./...` — expect clean
(no source changes yet, just dependency resolution).

- [ ] **Step 2: Create `internal/rebuildreadmodels/rebuildreadmodels.go`**

```go
// Package rebuildreadmodels holds the mechanics of a full read-model rebuild — deleting a
// projector's JetStream durable consumer (so a fresh one replays the whole retained stream from
// the start) and resetting its Postgres checkpoint to 0 — independently of cmd/rebuild-read-models'
// own CLI orchestration (confirmation prompts, phase sequencing), so this logic can be unit-tested
// directly against real Postgres/NATS testcontainers. See docs/BACKLOG.md's "projector" section
// and docs/superpowers/specs/2026-09-06-projector-backlog-fixes-design.md for why a checkpoint
// reset alone is not enough to force a real replay.
package rebuildreadmodels

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/projection/checkpoint"
)

// SetCheckpoint sets projectionName's checkpoint to value inside its own transaction, matching
// the same Get/Set contract every projector's own Router.handle already uses. Exported (not just
// ResetCheckpoint) so tests can simulate a projector catching up to an arbitrary value.
func SetCheckpoint(ctx context.Context, pool *pgxpool.Pool, projectionName string, value int64) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("rebuildreadmodels: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op once committed
	if err := checkpoint.Set(ctx, tx, projectionName, value); err != nil {
		return fmt.Errorf("rebuildreadmodels: set checkpoint %q to %d: %w", projectionName, value, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("rebuildreadmodels: commit checkpoint set for %q: %w", projectionName, err)
	}
	return nil
}

// ResetCheckpoint is SetCheckpoint's common case: the value a rebuild always resets to.
func ResetCheckpoint(ctx context.Context, pool *pgxpool.Pool, projectionName string) error {
	return SetCheckpoint(ctx, pool, projectionName, 0)
}

// CurrentCheckpoint reads projectionName's current checkpoint value without mutating it.
func CurrentCheckpoint(ctx context.Context, pool *pgxpool.Pool, projectionName string) (int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("rebuildreadmodels: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	seq, err := checkpoint.Get(ctx, tx, projectionName)
	if err != nil {
		return 0, fmt.Errorf("rebuildreadmodels: get checkpoint %q: %w", projectionName, err)
	}
	return seq, nil
}

// MaxGlobalSeq returns the current maximum global_seq across the whole event store — the target
// a rebuild's reset projectors must reach before the operation is considered caught up. Returns 0
// (not an error) on a completely empty events table.
func MaxGlobalSeq(ctx context.Context, pool *pgxpool.Pool) (int64, error) {
	var seq int64
	if err := pool.QueryRow(ctx, `SELECT COALESCE(MAX(global_seq), 0) FROM events`).Scan(&seq); err != nil {
		return 0, fmt.Errorf("rebuildreadmodels: query max global_seq: %w", err)
	}
	return seq, nil
}

// WaitForCatchUp polls names' checkpoints every interval until all of them are >= target, calling
// onProgress (nil-safe) after each check so a caller can print live status. Checks once
// immediately before ever waiting on interval, so a call where every name is already caught up
// returns right away rather than waiting a full interval first. Returns ctx.Err() if ctx is
// cancelled before that happens.
func WaitForCatchUp(ctx context.Context, pool *pgxpool.Pool, names []string, target int64, interval time.Duration, onProgress func(name string, current int64)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		allCaughtUp := true
		for _, name := range names {
			current, err := CurrentCheckpoint(ctx, pool, name)
			if err != nil {
				return err
			}
			if onProgress != nil {
				onProgress(name, current)
			}
			if current < target {
				allCaughtUp = false
			}
		}
		if allCaughtUp {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// DeleteConsumer deletes the JetStream durable consumer backing projectionName's subscription to
// subject (see bus.DurableName for how the consumer name is derived) — this is the step a
// checkpoint reset alone cannot substitute for; see this package's own doc comment. The stream
// name is the subject itself: internal/bus.NewSubscriber's own AutoProvision creates one JetStream
// stream per subject, named after the subject (watermill-nats's topicInterpreter.ensureStream
// calls AddStream with Name: topic). Deleting a nonexistent consumer or stream (e.g. a first-ever
// rebuild before cmd/projector has ever run) is not an error — nats.ErrConsumerNotFound and
// nats.ErrStreamNotFound are swallowed, matching this codebase's "start fresh rather than error"
// philosophy for idempotent operational tooling.
func DeleteConsumer(js nats.JetStreamManager, subject, projectionName string) error {
	durable := bus.DurableName(projectionName, subject)
	if err := js.DeleteConsumer(subject, durable); err != nil &&
		!errors.Is(err, nats.ErrConsumerNotFound) && !errors.Is(err, nats.ErrStreamNotFound) {
		return fmt.Errorf("rebuildreadmodels: delete consumer %q on stream %q: %w", durable, subject, err)
	}
	return nil
}
```

- [ ] **Step 3: Create `internal/rebuildreadmodels/rebuildreadmodels_test.go`**

```go
package rebuildreadmodels_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/rebuildreadmodels"
)

// newTestPool starts a fresh Postgres testcontainer with the events and projection_checkpoints
// migrations this package's tests need — mirrors every other package in this codebase's own
// per-package newTestPool convention (see e.g. internal/engine/timadorus/testutil_test.go).
func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../eventstore/postgres/migrations/0001_events.up.sql",
			"../projection/checkpoint/migrations/0001_projection_checkpoints.up.sql",
		),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(ctx) })

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

func TestResetCheckpoint_SetsToZero(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	if err := rebuildreadmodels.SetCheckpoint(ctx, pool, "test-projector", 42); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
	if err := rebuildreadmodels.ResetCheckpoint(ctx, pool, "test-projector"); err != nil {
		t.Fatalf("ResetCheckpoint: %v", err)
	}
	got, err := rebuildreadmodels.CurrentCheckpoint(ctx, pool, "test-projector")
	if err != nil {
		t.Fatalf("CurrentCheckpoint: %v", err)
	}
	if got != 0 {
		t.Fatalf("got %d, want 0", got)
	}
}

func TestMaxGlobalSeq_EmptyEventsTable_ReturnsZero(t *testing.T) {
	pool := newTestPool(t)
	got, err := rebuildreadmodels.MaxGlobalSeq(context.Background(), pool)
	if err != nil {
		t.Fatalf("MaxGlobalSeq: %v", err)
	}
	if got != 0 {
		t.Fatalf("got %d, want 0 on an empty events table", got)
	}
}

func TestMaxGlobalSeq_ReturnsCurrentMax(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := pool.Exec(ctx,
			`INSERT INTO events (aggregate_id, aggregate_type, version, event_type, payload)
			 VALUES (gen_random_uuid(), 'test', $1, 'test.event.v1', '{}')`, i+1,
		); err != nil {
			t.Fatalf("insert event %d: %v", i, err)
		}
	}

	got, err := rebuildreadmodels.MaxGlobalSeq(ctx, pool)
	if err != nil {
		t.Fatalf("MaxGlobalSeq: %v", err)
	}
	if got != 3 {
		t.Fatalf("got %d, want 3 (one row per insert, BIGSERIAL starts at 1)", got)
	}
}

func TestWaitForCatchUp_AlreadyCaughtUp_ReturnsImmediately(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	for _, name := range []string{"a", "b"} {
		if err := rebuildreadmodels.SetCheckpoint(ctx, pool, name, 10); err != nil {
			t.Fatalf("seed %q: %v", name, err)
		}
	}

	start := time.Now()
	// A long interval — if WaitForCatchUp checked-then-waited instead of the reverse, this test
	// would take at least that long; it must not, since both names are already at target.
	err := rebuildreadmodels.WaitForCatchUp(ctx, pool, []string{"a", "b"}, 10, time.Minute, nil)
	if err != nil {
		t.Fatalf("WaitForCatchUp: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("took %s, want near-instant (already caught up, must check before waiting)", elapsed)
	}
}

func TestWaitForCatchUp_PollsUntilExternallyBumped(t *testing.T) {
	pool := newTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := rebuildreadmodels.SetCheckpoint(ctx, pool, "slow", 0); err != nil {
		t.Fatalf("seed: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- rebuildreadmodels.WaitForCatchUp(ctx, pool, []string{"slow"}, 5, 50*time.Millisecond, nil)
	}()

	time.Sleep(200 * time.Millisecond)
	if err := rebuildreadmodels.SetCheckpoint(ctx, pool, "slow", 5); err != nil {
		t.Fatalf("bump: %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WaitForCatchUp: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("WaitForCatchUp did not return within 5s of being bumped to target")
	}
}
```

Run: `go test ./internal/rebuildreadmodels/... -run "TestResetCheckpoint|TestMaxGlobalSeq|TestWaitForCatchUp" -v`
— expect all 5 PASS.

- [ ] **Step 4: Create `internal/rebuildreadmodels/consumer_test.go`**

```go
package rebuildreadmodels_test

import (
	"context"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	tcnats "github.com/testcontainers/testcontainers-go/modules/nats"

	"github.com/timadorus/platform/internal/rebuildreadmodels"
)

// newTestJetStream starts a fresh NATS testcontainer (JetStream is enabled by default in this
// module's Run) and returns a connected JetStreamContext.
func newTestJetStream(t *testing.T) nats.JetStreamContext {
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
	nc, err := nats.Connect(connStr)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(nc.Close)

	js, err := nc.JetStream()
	if err != nil {
		t.Fatalf("jetstream: %v", err)
	}
	return js
}

// TestDeleteConsumer_ForcesFullReplay is the load-bearing proof for this whole package's reason
// to exist: a checkpoint reset alone does not cause NATS to redeliver an already-acked message,
// but deleting the durable consumer (this function) does.
func TestDeleteConsumer_ForcesFullReplay(t *testing.T) {
	js := newTestJetStream(t)
	const subject = "test-subject"
	const projectorName = "test-projector"
	durable := "test-projector_test-subject" // matches bus.DurableName(projectorName, subject)

	if _, err := js.AddStream(&nats.StreamConfig{Name: subject, Subjects: []string{subject}}); err != nil {
		t.Fatalf("add stream: %v", err)
	}

	sub, err := js.PullSubscribe(subject, durable)
	if err != nil {
		t.Fatalf("pull subscribe: %v", err)
	}
	if _, err := js.Publish(subject, []byte("hello")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	msgs, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1", len(msgs))
	}
	if err := msgs[0].Ack(); err != nil {
		t.Fatalf("ack: %v", err)
	}
	if err := sub.Unsubscribe(); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}

	// Baseline: without a reset, the acked message is NOT redelivered — proving this test would
	// actually fail if DeleteConsumer below did nothing.
	sub2, err := js.PullSubscribe(subject, durable)
	if err != nil {
		t.Fatalf("re-subscribe: %v", err)
	}
	if _, err := sub2.Fetch(1, nats.MaxWait(time.Second)); err == nil {
		t.Fatal("expected no messages before DeleteConsumer, got one")
	}
	if err := sub2.Unsubscribe(); err != nil {
		t.Fatalf("unsubscribe: %v", err)
	}

	if err := rebuildreadmodels.DeleteConsumer(js, subject, projectorName); err != nil {
		t.Fatalf("DeleteConsumer: %v", err)
	}

	sub3, err := js.PullSubscribe(subject, durable)
	if err != nil {
		t.Fatalf("re-subscribe after delete: %v", err)
	}
	defer sub3.Unsubscribe()
	msgs2, err := sub3.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil {
		t.Fatalf("fetch after DeleteConsumer: %v", err)
	}
	if len(msgs2) != 1 || string(msgs2[0].Data) != "hello" {
		t.Fatalf("got %v, want the original message redelivered after DeleteConsumer", msgs2)
	}
}

func TestDeleteConsumer_NonexistentConsumer_NoError(t *testing.T) {
	js := newTestJetStream(t)
	const subject = "test-subject-2"
	if _, err := js.AddStream(&nats.StreamConfig{Name: subject, Subjects: []string{subject}}); err != nil {
		t.Fatalf("add stream: %v", err)
	}
	if err := rebuildreadmodels.DeleteConsumer(js, subject, "never-existed"); err != nil {
		t.Fatalf("got error %v, want nil (deleting a nonexistent consumer must be a no-op)", err)
	}
}
```

Run: `go test ./internal/rebuildreadmodels/... -run TestDeleteConsumer -v` — expect both PASS.

- [ ] **Step 5: Run full regression check and commit**

Run: `go build ./... && go vet ./... && go test -count=1 ./internal/rebuildreadmodels/...` —
expect clean, all 7 tests passing.

```bash
git add internal/rebuildreadmodels/ go.mod go.sum
git commit -m "rebuildreadmodels: add the checkpoint-reset and NATS-consumer-deletion mechanics, tested"
```

---

### Task 5: `cmd/rebuild-read-models` CLI and final BACKLOG updates

**Files:**
- Create: `cmd/rebuild-read-models/main.go`
- Modify: `docs/BACKLOG.md`

**Depends on:** Task 3 (`registry`), Task 4 (`rebuildreadmodels`).

- [ ] **Step 1: Create `cmd/rebuild-read-models/main.go`**

```go
// Command rebuild-read-models is a standalone maintenance tool for safely rebuilding every
// projection's read model from scratch. Resetting a Postgres checkpoint alone does not cause NATS
// to redeliver anything a durable consumer has already acked — this tool deletes each affected
// projector's JetStream durable consumer too (see internal/rebuildreadmodels's own doc comment for
// why), so a fresh one replays the whole retained stream. See docs/BACKLOG.md's "projector"
// section and docs/superpowers/specs/2026-09-06-projector-backlog-fixes-design.md for the full
// reasoning.
//
// cmd/projector MUST be stopped before running either phase, and restarted after each — deleting a
// durable consumer a live subscription is bound to has undefined behavior otherwise. This tool
// does not attempt to detect whether cmd/projector is running; it asks for an explicit interactive
// confirmation instead.
//
// Usage:
//
//	rebuild-read-models --confirm
//	  Deletes the 7 base projectors' consumers, resets their checkpoints to 0, then polls until
//	  they've all caught back up. Prints the target global_seq and the exact phase-2 command to
//	  run next.
//
//	rebuild-read-models --confirm --phase=change-feed --target-seq=<value printed by phase 1>
//	  Confirms the 7 base projectors have reached target-seq, then does the same delete+reset for
//	  the 5 universe-changes-* projectors.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"

	"github.com/timadorus/platform/internal/config"
	"github.com/timadorus/platform/internal/projection"
	"github.com/timadorus/platform/internal/projection/registry"
	"github.com/timadorus/platform/internal/rebuildreadmodels"
)

const pollInterval = 5 * time.Second

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	confirm := flag.Bool("confirm", false, "required: acknowledges this tool is destructive and that cmd/projector must already be stopped")
	phase := flag.String("phase", "base", `"base" (default) or "change-feed"`)
	targetSeq := flag.Int64("target-seq", 0, "required for --phase=change-feed: the value printed by the base-phase run")
	flag.Parse()

	if err := run(ctx, logger, *confirm, *phase, *targetSeq); err != nil {
		logger.Error("rebuild-read-models: fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger, confirm bool, phase string, targetSeq int64) error {
	if !confirm {
		return fmt.Errorf("refusing to run without --confirm — this tool deletes JetStream durable consumers and resets Postgres checkpoints; see this binary's own package doc comment before using it")
	}
	if phase != "base" && phase != "change-feed" {
		return fmt.Errorf("--phase must be %q or %q, got %q", "base", "change-feed", phase)
	}
	if phase == "change-feed" && targetSeq <= 0 {
		return fmt.Errorf("--phase=change-feed requires --target-seq (the value printed by the base-phase run)")
	}

	fmt.Fprintln(os.Stderr, "WARNING: cmd/projector MUST already be stopped. Deleting a durable")
	fmt.Fprintln(os.Stderr, "consumer a live subscription is bound to has undefined behavior.")
	if !confirmInteractive("Is cmd/projector stopped? Type 'y' to continue") {
		return fmt.Errorf("aborted: cmd/projector was not confirmed stopped")
	}

	cfg, err := config.LoadProjector()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("connect to postgres: %w", err)
	}
	defer pool.Close()

	nc, err := nats.Connect(cfg.NATSURL)
	if err != nil {
		return fmt.Errorf("connect to nats: %w", err)
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		return fmt.Errorf("get jetstream context: %w", err)
	}

	if phase == "base" {
		return runBasePhase(ctx, logger, pool, js)
	}
	return runChangeFeedPhase(ctx, logger, pool, js, targetSeq)
}

func resetAll(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, js nats.JetStreamManager, projectors []projection.Projector) error {
	for _, p := range projectors {
		name := p.Name()
		for _, subject := range p.Subjects() {
			if err := rebuildreadmodels.DeleteConsumer(js, subject, name); err != nil {
				return err
			}
		}
		if err := rebuildreadmodels.ResetCheckpoint(ctx, pool, name); err != nil {
			return err
		}
		logger.Info("rebuild-read-models: reset", "projector", name)
	}
	return nil
}

func runBasePhase(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, js nats.JetStreamManager) error {
	target, err := rebuildreadmodels.MaxGlobalSeq(ctx, pool)
	if err != nil {
		return err
	}

	base := registry.Base()
	if err := resetAll(ctx, logger, pool, js, base); err != nil {
		return err
	}

	names := make([]string, len(base))
	for i, p := range base {
		names[i] = p.Name()
	}

	fmt.Fprintf(os.Stderr, "Base projectors reset. Start cmd/projector now, then wait for it to catch up to global_seq=%d.\n", target)
	fmt.Fprintln(os.Stderr, "Polling for catch-up (Ctrl-C to stop watching once you're satisfied — the reset has already happened)...")
	err = rebuildreadmodels.WaitForCatchUp(ctx, pool, names, target, pollInterval, func(name string, current int64) {
		fmt.Fprintf(os.Stderr, "  %s: %d/%d\n", name, current, target)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			fmt.Fprintf(os.Stderr, "\nStopped watching (the reset itself already completed). Once all base projectors reach global_seq=%d, run:\n\n  rebuild-read-models --confirm --phase=change-feed --target-seq=%d\n", target, target)
			return nil
		}
		return err
	}
	fmt.Fprintf(os.Stderr, "\nAll base projectors caught up. Run:\n\n  rebuild-read-models --confirm --phase=change-feed --target-seq=%d\n", target)
	return nil
}

func runChangeFeedPhase(ctx context.Context, logger *slog.Logger, pool *pgxpool.Pool, js nats.JetStreamManager, targetSeq int64) error {
	for _, p := range registry.Base() {
		current, err := rebuildreadmodels.CurrentCheckpoint(ctx, pool, p.Name())
		if err != nil {
			return err
		}
		if current < targetSeq {
			return fmt.Errorf("base projector %q is at %d, not yet caught up to --target-seq=%d — run the base phase first and wait for it to finish", p.Name(), current, targetSeq)
		}
	}

	if err := resetAll(ctx, logger, pool, js, registry.ChangeFeed()); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "Change-feed projectors reset. Restart cmd/projector now.")
	return nil
}

func confirmInteractive(prompt string) bool {
	fmt.Fprintf(os.Stderr, "%s: ", prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	return strings.TrimSpace(strings.ToLower(line)) == "y"
}
```

Run: `go build ./... && go vet ./...` — expect clean.

- [ ] **Step 2: Update `docs/BACKLOG.md` to mark the remaining two projector items fixed**

Replace:

```markdown
- [ ] **`cmd/projector` now runs 12 projectors on a default-sized connection pool with no budget
  note.** `cmd/timadorus-engine/main.go` carries an explicit "Connection budget" comment for its 2
  processors; `cmd/projector/main.go`'s `pgxpool.New` has no equivalent, and the
  `universe-change-feed` branch took it from 7 to 12 projectors (a 71% increase in concurrent
  connection demand) with no explicit `pool_max_conns` (defaults to `max(4, NumCPU)`). Not a
  correctness bug today — each `Router.handle` holds exactly one connection and the
  `universechanges` projectors resolve on the ambient tx rather than acquiring a second pool
  connection, so there's no deadlock risk — but on a small node a simultaneous cold-start replay
  of all 12 could in principle queue long enough to trip Watermill's 30s `AckWaitTimeout` and
  cause redelivery churn. Add a "Connection budget" comment near `cmd/projector/main.go`'s
  `pgxpool.New` mirroring the engine's, and consider `pool_max_conns` if this is ever measured to
  matter in practice.

- [ ] **A full read-model rebuild (all checkpoints reset) is not safe with the change-feed
  projectors in the mix.** Replay load at this platform's current scale is fine (cheap indexed
  lookup + insert per event, small backlogs). But if every checkpoint were ever reset to rebuild
  read models from scratch, the `universe-changes-*` projectors would race the base projectors
  with no ordering guarantee between independent durables, and the 5-attempt Nack budget (no
  `NakDelay`) would burn in milliseconds — non-`Created` events would dead-letter en masse and
  their change rows would be lost. Rebuild base read models first, then reset the
  `universe-changes-*` checkpoints, if a full rebuild is ever needed.
```

with:

```markdown
- [x] **Fixed.** `cmd/projector` now builds its pool via `pgxpool.ParseConfig` +
  `pgxpool.NewWithConfig`, with `MaxConns` from the new `Projector.PoolMaxConns` config field
  (default 16, overridable via `PROJECTOR_POOL_MAX_CONNS`), mirroring
  `cmd/timadorus-engine`'s already-established pattern exactly.

- [x] **Fixed.** `cmd/rebuild-read-models` (new standalone binary) automates a safe full
  read-model rebuild: it deletes each affected projector's JetStream durable consumer (a
  checkpoint reset alone does not cause NATS to redeliver an already-acked message — see
  `internal/rebuildreadmodels`'s own doc comment) and resets its Postgres checkpoint, in two
  required phases (`--phase=base` then `--phase=change-feed`), refusing to run the second phase
  until the base projectors have actually caught up to the first phase's captured target. Requires
  `cmd/projector` to already be stopped (an explicit interactive confirmation, not detected
  automatically). The shared list of "every registered projector" now lives in
  `internal/projection/registry`, used by both `cmd/projector/main.go` and this tool, so they can
  never drift out of sync.
```

- [ ] **Step 3: Run full regression check and commit**

Run: `go build ./... && go vet ./... && go test -count=1 ./...` — expect clean.

```bash
git add cmd/rebuild-read-models/main.go docs/BACKLOG.md
git commit -m "cmd/rebuild-read-models: add the CLI, mark both projector BACKLOG items fixed"
```

---

## Final Verification

- `go build ./... && go vet ./... && go test -count=1 ./...` clean.
- `docs/BACKLOG.md`'s "projector" section shows all three items as `[x]`.
- Manual smoke test (not automated, matching this plan's design spec's Testing Strategy): run
  `rebuild-read-models` with no flags and confirm it refuses with a clear message; run it with
  `--confirm` against a local `make dev-up` cluster (with `cmd/projector`'s own pod scaled to 0
  first) and confirm it prints a sensible target and progress lines.
