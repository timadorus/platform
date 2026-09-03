# timadorus-engine Backlog Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix five specific items from `docs/BACKLOG.md`'s `timadorus-engine` section — a
concurrency test that doesn't reliably exercise its own race, an unconfigurable connection pool,
a Ruleset rename that silently corrupts the name-uniqueness reservation, table-content edits that
never reach an already-synced cluster, and missing e2e coverage for the ruleset-tables endpoints.

**Architecture:** No new subsystems. Each task is a targeted, independent fix inside existing
packages (`internal/engine/timadorus`, `internal/config`, `cmd/timadorus-engine`,
`internal/command/ruleset`, `internal/engine/timadorus/tables`, `internal/query/rulesettables`,
`test/e2e`). Tasks have no dependencies on each other and may be done in any order, but are
numbered to match the user's own request.

**Tech Stack:** Go, `pgx`/`pgxpool`, `golang-migrate`-style SQL migrations, `testcontainers-go`,
Ginkgo/Gomega (e2e only).

## Global Constraints

- Every fixed BACKLOG item's bullet must be replaced with a short "fixed" summary (commit hash(es)
  included), matching the exact style already used at the top of `docs/BACKLOG.md`'s "Web SPA"
  section ("All three items previously listed here are fixed (`e7ad69c`, `e89a5f3`): ..."). Do not
  just check a box — remove the bullet's problem-statement prose and replace it with the fixed
  summary, per this file's own stated convention ("Pull an item out of here... when picked up").
- Any doc comment elsewhere in the tree that describes a since-fixed gap as still-open must be
  updated in the same task that fixes the underlying code — stale doc comments describing fixed
  bugs as live are worse than no comment (this session's own established standard).
- Every new/changed migration must go in the existing schema-owner's `migrations/` directory
  (never a new directory) — `scripts/migrate-up.sh` and `Dockerfile.migrate` reference whole
  directories for this schema owner already, so no change to either file is needed, but Task 4
  must verify this empirically (real Docker build+run), not assume it.
- No task may weaken an existing test's assertion to make a change pass. If an existing test's
  literal expectation is genuinely wrong given the fix, say so in the task report; don't silently
  loosen it.
- `go build ./... && go vet ./... && go test ./...` must stay clean after every task.

---

### Task 1: `TestRulesetCache_ConcurrentGetSet_Race` — a race test that can't miss

**Files:**
- Modify: `internal/engine/timadorus/cache_test.go`

**Interfaces:**
- Consumes: `RulesetCache.get(campaignID uuid.UUID) (string, bool)`, `RulesetCache.set(campaignID uuid.UUID, name string)`, `NewRulesetCache() *RulesetCache` — all already exist, unexported, in `internal/engine/timadorus/cache.go`. This test file is in `package timadorus` (white-box), so it can call them directly.
- Produces: nothing consumed by later tasks.

The existing `TestSharedRulesetCache_ConcurrentAccess` (in `shared_cache_test.go`) drives two full
processors through a real router and rarely gets genuine concurrent access to the cache — see
`docs/BACKLOG.md`'s entry for why. This task adds a second, lower-level test that removes every
layer between the goroutines and the cache itself.

- [ ] **Step 1: Add the new test**

Add to `internal/engine/timadorus/cache_test.go` (add `"sync"` to the existing import block):

```go
package timadorus

import (
	"sync"
	"testing"

	"github.com/google/uuid"
)

func TestRulesetCache(t *testing.T) {
	// ... existing test, unchanged ...
}

// TestRulesetCache_ConcurrentGetSet_Race drives RulesetCache.get/set directly from many
// goroutines with no DB round-trip and no processor in the way, unlike
// TestSharedRulesetCache_ConcurrentAccess (shared_cache_test.go), whose extra
// characters_read_model hop before its Character path reaches the cache reliably lets the
// Campaign path win first — so under `go test -race` that test alone almost never actually
// catches a RulesetCache locking regression (verified: 28 runs against a version of cache.go
// with RulesetCache.get/set's mutex calls deleted caught zero races). This test's only job is
// giving -race a target it can't fail to see: many goroutines genuinely racing on a handful of
// shared keys, no I/O in the way to serialize them by accident.
func TestRulesetCache_ConcurrentGetSet_Race(t *testing.T) {
	c := NewRulesetCache()
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := ids[i%len(ids)]
			c.set(id, "timadorus")
			_, _ = c.get(id)
		}(i)
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run it clean first**

Run: `go test -race ./internal/engine/timadorus/... -run TestRulesetCache_ConcurrentGetSet_Race -count=5 -v`
Expected: PASS, 5/5, no `-race` warnings — the existing mutex in `cache.go`'s `get`/`set` already
protects this correctly.

- [ ] **Step 3: Prove the test actually catches a regression**

Temporarily comment out `c.mu.RLock()`/`c.mu.RUnlock()` in `get` and `c.mu.Lock()`/`c.mu.Unlock()`
in `set` (`internal/engine/timadorus/cache.go`). Run:
`go test -race ./internal/engine/timadorus/... -run TestRulesetCache_ConcurrentGetSet_Race -count=5 -v`
Expected: FAIL — `go test -race` reports a `DATA RACE` on `RulesetCache.names`. This is the proof
the test is load-bearing, not just green-by-luck. **Revert the temporary edit to `cache.go`
immediately after confirming this** — `cache.go` itself does not change in this task, only the
test file does.

- [ ] **Step 4: Confirm clean again after reverting**

Run: `go test -race ./internal/engine/timadorus/...` (the package's full suite, not just the new
test). Expected: PASS, no races, no regressions in `TestRulesetCache` or either
`shared_cache_test.go` test.

- [ ] **Step 5: Update BACKLOG.md**

In `docs/BACKLOG.md`'s `timadorus-engine` section, replace the
`TestSharedRulesetCache_ConcurrentAccess has a real but unreliable race window` bullet with:

```markdown
- [x] **Fixed.** `TestRulesetCache_ConcurrentGetSet_Race` (`internal/engine/timadorus/cache_test.go`)
  drives `RulesetCache.get`/`set` directly from 50 goroutines against 3 shared keys, no DB
  round-trip or processor in the way — confirmed to actually catch a regression by temporarily
  deleting the cache's mutex calls and observing `go test -race` report a real data race, then
  reverting. `TestSharedRulesetCache_ConcurrentAccess` stays as-is; it still proves the two
  processors correctly share one cache instance end to end, just not reliably under `-race`.
```

- [ ] **Step 6: Commit**

```bash
git add internal/engine/timadorus/cache_test.go docs/BACKLOG.md
git commit -m "engine: add a race test that reliably catches a RulesetCache locking regression"
```

---

### Task 2: Configurable connection pool for `cmd/timadorus-engine`

**Files:**
- Modify: `internal/config/config.go`
- Create: `internal/config/config_test.go`
- Modify: `cmd/timadorus-engine/main.go`

**Interfaces:**
- Produces: `config.TimadorusEngine.PoolMaxConns int32`, read by `cmd/timadorus-engine/main.go`'s
  `run()`. No other package touches `TimadorusEngine`.

- [ ] **Step 1: Add the config field and env-parsing helper**

In `internal/config/config.go`, add `"strconv"` to the import block, then change:

```go
type TimadorusEngine struct {
	// HTTPAddr serves /healthz, /readyz, /metrics only — same shape as Projector's, see that
	// type's doc comment.
	HTTPAddr    string
	DatabaseURL string
	NATSURL     string
	// PoolMaxConns caps the shared Postgres connection pool used by both engine processors (see
	// cmd/timadorus-engine/main.go's "Connection budget" comment for the reasoning behind the
	// default of 8). Configurable via TIMADORUS_ENGINE_POOL_MAX_CONNS so a 3rd processor sharing
	// this binary, or any other change to the per-Handle connection cost, can be given headroom
	// without a code change or redeploy of a new binary.
	PoolMaxConns int32
}

func LoadTimadorusEngine() TimadorusEngine {
	return TimadorusEngine{
		HTTPAddr:     getEnv("TIMADORUS_ENGINE_ADDR", ":8084"),
		DatabaseURL:  getEnv("DATABASE_URL", "postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable"),
		NATSURL:      getEnv("NATS_URL", "nats://localhost:4222"),
		PoolMaxConns: getEnvInt32("TIMADORUS_ENGINE_POOL_MAX_CONNS", 8),
	}
}
```

Add this helper next to `getEnv` at the bottom of the file:

```go
// getEnvInt32 parses key as a base-10 int32, falling back to def on an unset or unparseable
// value — deliberately silent on a bad value (this package has no logger to report through)
// rather than failing binary startup over a malformed tuning knob.
func getEnvInt32(key string, def int32) int32 {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil {
		return def
	}
	return int32(n)
}
```

- [ ] **Step 2: Write the failing tests**

Create `internal/config/config_test.go`:

```go
package config_test

import (
	"testing"

	"github.com/timadorus/platform/internal/config"
)

func TestLoadTimadorusEngine_PoolMaxConnsDefault(t *testing.T) {
	cfg := config.LoadTimadorusEngine()
	if cfg.PoolMaxConns != 8 {
		t.Fatalf("got PoolMaxConns %d, want default 8", cfg.PoolMaxConns)
	}
}

func TestLoadTimadorusEngine_PoolMaxConnsOverride(t *testing.T) {
	t.Setenv("TIMADORUS_ENGINE_POOL_MAX_CONNS", "16")
	cfg := config.LoadTimadorusEngine()
	if cfg.PoolMaxConns != 16 {
		t.Fatalf("got PoolMaxConns %d, want overridden 16", cfg.PoolMaxConns)
	}
}

func TestLoadTimadorusEngine_PoolMaxConnsInvalid_FallsBackToDefault(t *testing.T) {
	t.Setenv("TIMADORUS_ENGINE_POOL_MAX_CONNS", "not-a-number")
	cfg := config.LoadTimadorusEngine()
	if cfg.PoolMaxConns != 8 {
		t.Fatalf("got PoolMaxConns %d, want default 8 on invalid input", cfg.PoolMaxConns)
	}
}
```

- [ ] **Step 3: Run — should already pass**

Since Step 1's implementation lands in the same task, run:
`go test ./internal/config/... -run TestLoadTimadorusEngine -v`
Expected: PASS, all 3.

- [ ] **Step 4: Wire it into `cmd/timadorus-engine/main.go`**

Replace this block in `run()`:

```go
	// Connection budget: each in-flight Router.Handle call can hold up to 2 pool connections
	// at once — one for the Router's own transaction, and one for the aggregate's Load, which
	// reads via the pool rather than the ambient tx (see postgres.Store.Load). So N processors
	// sharing this one pool can peak at 2N connections, on top of /readyz's own Ping. With the
	// 2 processors registered below that's already at pgxpool's default floor (max(4,
	// NumCPU)) in the worst case. A third processor sharing this binary would need an explicit
	// pool_max_conns bump — a deliberate follow-up, not done here.
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
```

with:

```go
	// Connection budget: each in-flight Router.Handle call can hold up to 2 pool connections
	// at once — one for the Router's own transaction, and one for the aggregate's Load, which
	// reads via the pool rather than the ambient tx (see postgres.Store.Load). So N processors
	// sharing this one pool can peak at 2N connections, on top of /readyz's own Ping. With the
	// 2 processors registered below that's 4, plus Ping — comfortably under the default of 8.
	// Configurable via TIMADORUS_ENGINE_POOL_MAX_CONNS (internal/config.LoadTimadorusEngine) if
	// a 3rd processor or heavier load ever needs more headroom, with no code change required.
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

- [ ] **Step 5: Verify the binary still builds and starts**

Run: `go build ./cmd/timadorus-engine/... && go vet ./cmd/timadorus-engine/...`
Expected: clean. (Full integration — actually connecting with a real pool — is already covered by
this binary's existing test suite and `make dev-up`; no new integration test is needed for a
`pgxpool.Config.MaxConns` field assignment.)

- [ ] **Step 6: Update BACKLOG.md**

Replace the `Connection pool headroom` bullet with:

```markdown
- [x] **Fixed.** `cmd/timadorus-engine/main.go` now builds its pool via `pgxpool.ParseConfig` +
  `pgxpool.NewWithConfig`, setting `MaxConns` from the new `TimadorusEngine.PoolMaxConns` config
  field (`internal/config/config.go`, default 8, overridable via
  `TIMADORUS_ENGINE_POOL_MAX_CONNS`). A 3rd processor sharing this binary can now get headroom via
  config alone, no code change.
```

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go cmd/timadorus-engine/main.go docs/BACKLOG.md
git commit -m "config,timadorus-engine: make the engine's connection pool size configurable"
```

---

### Task 3: `ruleset.Service.Rename` reserves/releases names atomically

**Files:**
- Modify: `internal/command/ruleset/service.go`
- Modify: `internal/engine/timadorus/register.go` (stale doc comment only)
- Modify: `internal/command/ruleset/service_test.go`

**Interfaces:**
- Consumes: `postgres.NewUnitOfWork(ctx, pool) (*UnitOfWork, context.Context, error)`,
  `postgres.TxFromContext(ctx) (pgx.Tx, bool)`, `postgres.IsUniqueViolation(err) bool` — all
  already used identically by `Service.Create` in the same file.
- Produces: no signature change to `Service.Rename(ctx, id, name) error` — same signature,
  corrected behavior only.

`ruleset.Ruleset.Rename` (`internal/domain/ruleset/ruleset.go:49-60`) is a no-op (no error, no
event) if `name == r.name` already — the fix below must not touch `ruleset_names` in that case, or
a repeated rename to the same name would try to reserve a name already reserved by itself and
wrongly report `ErrNameAlreadyExists`.

- [ ] **Step 1: Write the failing tests**

Add to `internal/command/ruleset/service_test.go`:

```go
func TestService_Rename_ReleasesOldNameAndReservesNew(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	id, err := service.Create(ctx, "OldName", "", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := service.Rename(ctx, id, "NewName"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// The old name must be released: a later Create must succeed, not collide.
	if _, err := service.Create(ctx, "OldName", "", nil); err != nil {
		t.Fatalf("create with the released old name: %v", err)
	}

	// The new name must be reserved: FindIDByName must resolve it to the renamed aggregate.
	found, err := service.FindIDByName(ctx, "NewName")
	if err != nil {
		t.Fatalf("find id by new name: %v", err)
	}
	if found != id {
		t.Fatalf("got id %s for \"NewName\", want %s", found, id)
	}

	var oldNameCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ruleset_names WHERE name = $1 AND id = $2`, "OldName", id).Scan(&oldNameCount); err != nil {
		t.Fatalf("count old reservation still pointing at the renamed aggregate: %v", err)
	}
	if oldNameCount != 0 {
		t.Fatalf("got %d ruleset_names rows for (\"OldName\", %s), want 0 (must not still point at the renamed aggregate)", oldNameCount, id)
	}
}

func TestService_Rename_ToAnAlreadyTakenName_FailsAndKeepsOldReservation(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	idA, err := service.Create(ctx, "RulesetA", "", nil)
	if err != nil {
		t.Fatalf("create A: %v", err)
	}
	if _, err := service.Create(ctx, "RulesetB", "", nil); err != nil {
		t.Fatalf("create B: %v", err)
	}

	err = service.Rename(ctx, idA, "RulesetB")
	if !errors.Is(err, ruleset.ErrNameAlreadyExists) {
		t.Fatalf("got %v, want ErrNameAlreadyExists", err)
	}

	// A's old reservation must survive the failed rename attempt.
	found, err := service.FindIDByName(ctx, "RulesetA")
	if err != nil {
		t.Fatalf("find id by RulesetA after failed rename: %v", err)
	}
	if found != idA {
		t.Fatalf("got id %s for \"RulesetA\" after failed rename, want %s (old reservation must survive)", found, idA)
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE aggregate_id = $1 AND event_type = $2`,
		idA, rulesetevents.TypeRulesetRenamed,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count RulesetRenamed events: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("got %d RulesetRenamed events for A, want 0 (a failed reservation must not still raise the event)", eventCount)
	}
}

func TestService_Rename_ToTheSameName_IsANoOpAndDoesNotTouchReservations(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	id, err := service.Create(ctx, "SameName", "", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := service.Rename(ctx, id, "SameName"); err != nil {
		t.Fatalf("rename to the same name: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ruleset_names WHERE name = $1`, "SameName").Scan(&count); err != nil {
		t.Fatalf("count ruleset_names: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d ruleset_names rows for \"SameName\", want 1 (renaming to the current name must be a pure no-op)", count)
	}
}
```

- [ ] **Step 2: Run to verify they fail**

Run: `go test ./internal/command/ruleset/... -run TestService_Rename -v`
Expected: `TestService_Rename_ReleasesOldNameAndReservesNew` FAILs on the "create with the released
old name" step (old behavior leaves it reserved, so this second `Create` gets
`ErrNameAlreadyExists` instead of succeeding). The other two may already incidentally pass or fail
depending on order — that's fine, the important one is confirmed broken first.

- [ ] **Step 3: Fix `Service.Rename`**

In `internal/command/ruleset/service.go`, replace:

```go
func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) error {
	r, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := r.Rename(name); err != nil {
		return err
	}
	return s.repo.Save(ctx, r)
}
```

with:

```go
// Rename reserves the new name and releases the old one in the same transaction as the
// RulesetRenamed save — the same shape Create already uses for its own reservation (see Create's
// doc comment). A no-op rename (name already equals the current name — see Ruleset.Rename) skips
// the reservation dance entirely: there is nothing to release or reserve, and attempting to
// insert a name already reserved by this same aggregate would wrongly report
// ErrNameAlreadyExists.
func (s *Service) Rename(ctx context.Context, id uuid.UUID, name string) error {
	r, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	oldName := r.Name()
	if err := r.Rename(name); err != nil {
		return err
	}
	if name == oldName {
		return s.repo.Save(ctx, r)
	}

	uow, txCtx, err := postgres.NewUnitOfWork(ctx, s.pool)
	if err != nil {
		return err
	}
	tx, _ := postgres.TxFromContext(txCtx) // always ok: txCtx was just built by NewUnitOfWork

	if _, err := tx.Exec(ctx, `INSERT INTO ruleset_names (name, id) VALUES ($1, $2)`, name, id); err != nil {
		_ = uow.Rollback(ctx)
		if postgres.IsUniqueViolation(err) {
			return ruleset.ErrNameAlreadyExists
		}
		return fmt.Errorf("ruleset: reserve renamed name %q: %w", name, err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM ruleset_names WHERE name = $1`, oldName); err != nil {
		_ = uow.Rollback(ctx)
		return fmt.Errorf("ruleset: release old name %q: %w", oldName, err)
	}
	if err := s.repo.Save(txCtx, r); err != nil {
		_ = uow.Rollback(ctx)
		return err
	}
	return uow.Commit(ctx)
}
```

- [ ] **Step 4: Run to verify they pass**

Run: `go test ./internal/command/ruleset/... -v`
Expected: PASS, all tests in the package (existing `Create`/`FindIDByName` tests included — this
change touches only `Rename`).

- [ ] **Step 5: Fix the now-stale doc comments**

`internal/command/ruleset/service.go`'s `Create` doc comment currently ends with:

```
// invariant does not extend to Rename: Rename never touches ruleset_names, so a name freed by
// renaming a Ruleset away stays reserved (orphaned), and the new name it takes is left
// unreserved — a later Create can then produce a duplicate of that new name. See docs/BACKLOG.md
// for the known gap and the fix sketch (reserving the new name in Rename's own transaction).
func (s *Service) Create(ctx context.Context, name, description string, references []string) (uuid.UUID, error) {
```

Change the last four lines to:

```
// invariant now also extends to Rename (see Rename's own doc comment): renaming reserves the
// new name and releases the old one in the same transaction as the RulesetRenamed save, so the
// two never disagree.
func (s *Service) Create(ctx context.Context, name, description string, references []string) (uuid.UUID, error) {
```

`internal/engine/timadorus/register.go`'s `RegisterRuleset` doc comment currently has this
paragraph (lines 39-46):

```
// The "already exists" check only ever looks at the ruleset_names reservation, not at whether a
// Ruleset named TargetRulesetName actually exists right now: ruleset.Service.Rename never
// releases or re-reserves names (see Service.Create's doc comment), so if the "Timadorus" Ruleset
// is ever renamed away, this reservation survives and every later startup still treats the
// platform as registered — even though no Ruleset is actually named TargetRulesetName any more.
// FindIDByName still resolves the ORIGINAL Ruleset's id correctly in that case (the reservation's
// id column is set once at Create and never changes), which is arguably the more useful behavior
// anyway — that Ruleset still exists, just under a different name.
```

Replace it with:

```
// The "already exists" check only ever looks at the ruleset_names reservation, which now stays
// in sync with reality: ruleset.Service.Rename releases the old name and reserves the new one in
// the same transaction as the RulesetRenamed save (see that method's own doc comment). So if the
// "Timadorus" Ruleset is ever renamed away, the "Timadorus" reservation is released along with
// it, and the very next startup's Create call above succeeds in making a brand-new Ruleset
// genuinely named TargetRulesetName — rather than resolving the old, now-differently-named one,
// which is the correct behavior given this engine's whole premise is "act on the Ruleset
// literally named Timadorus."
```

- [ ] **Step 6: Run the full package suite plus a build/vet check**

Run: `go build ./... && go vet ./... && go test ./internal/command/ruleset/... ./internal/engine/timadorus/... -v`
Expected: all clean, no regressions in either package.

- [ ] **Step 7: Update BACKLOG.md**

Replace the `Rename bypasses the ruleset_names reservation entirely...` bullet with:

```markdown
- [x] **Fixed.** `ruleset.Service.Rename` (`internal/command/ruleset/service.go`) now reserves the
  new name and releases the old one in the same transaction as the `RulesetRenamed` save, mirroring
  `Create`'s own reservation shape exactly. A rename to an already-taken name now correctly fails
  with `ErrNameAlreadyExists` and leaves the old reservation untouched; a rename to the current
  name is a pure no-op. `RegisterRuleset`'s and `Create`'s doc comments updated to match.
```

- [ ] **Step 8: Commit**

```bash
git add internal/command/ruleset/service.go internal/command/ruleset/service_test.go internal/engine/timadorus/register.go docs/BACKLOG.md
git commit -m "ruleset: Rename now reserves/releases ruleset_names atomically, matching Create"
```

---

### Task 4: Table content-hash versioning — edits reach already-synced clusters

**Files:**
- Create: `internal/projection/rulesettables/migrations/0002_content_hash_versioning.up.sql`
- Create: `internal/projection/rulesettables/migrations/0002_content_hash_versioning.down.sql`
- Modify: `internal/engine/timadorus/tables/sync.go`
- Modify: `internal/engine/timadorus/tables/sync_test.go`
- Modify: `internal/engine/timadorus/tables/testutil_test.go`
- Modify: `internal/query/rulesettables/repository.go`
- Modify: `internal/query/rulesettables/repository_test.go`

**Interfaces:**
- Consumes: nothing new.
- Produces: no signature changes to `RegisterTables`, `Repository.List`, or `Repository.Get` —
  same signatures, corrected behavior and schema only. `Row{Key, Data}` unchanged.

Widens `ruleset_tables_read_model`'s primary key from `(ruleset_id, table_name, row_key)` to
`(ruleset_id, table_name, row_key, content_hash)`. A YAML edit's new content gets a new hash, so it
inserts as an *additional* row (preserving the old one as history) instead of being silently
discarded by the existing `ON CONFLICT ... DO NOTHING`. The query side always resolves the newest
row per key by `updated_at`, so callers still see exactly one row per key.

- [ ] **Step 1: Write the migration**

Create `internal/projection/rulesettables/migrations/0002_content_hash_versioning.up.sql`:

```sql
-- Table content is otherwise immutable per Ruleset (see 0001's own comment), but real edits to a
-- table's source YAML do happen (e.g. fixing a typo) and previously had no way to reach a cluster
-- that had already synced the old content once — RegisterTables's own
-- ON CONFLICT (ruleset_id, table_name, row_key) DO NOTHING silently discarded the edit forever.
-- Widening the primary key to include a content hash lets an edited row's new content insert as
-- an additional, newer row instead of being dropped, while the old row(s) stay in place as
-- history. The query side (internal/query/rulesettables) always selects the newest row per
-- (ruleset_id, table_name, row_key) by updated_at, so callers still see exactly one row per key —
-- just the latest one.
--
-- One-time, self-healing cost on the first startup after this migration on any cluster that
-- already had rows: every pre-existing row gets content_hash = '' (the DEFAULT below, dropped
-- immediately after backfilling existing rows), which never matches a freshly computed real
-- hash — so RegisterTables's very next run inserts one new, correctly hashed row per existing
-- key, permanently alongside its old content_hash='' placeholder predecessor. Harmless: the
-- query side already picks the newest row by updated_at, so this is invisible to any caller,
-- just a one-time doubling of row count for pre-existing rows.
ALTER TABLE ruleset_tables_read_model ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';
ALTER TABLE ruleset_tables_read_model ALTER COLUMN content_hash DROP DEFAULT;
ALTER TABLE ruleset_tables_read_model DROP CONSTRAINT ruleset_tables_read_model_pkey;
ALTER TABLE ruleset_tables_read_model ADD PRIMARY KEY (ruleset_id, table_name, row_key, content_hash);
CREATE INDEX ruleset_tables_read_model_latest_idx
    ON ruleset_tables_read_model (ruleset_id, table_name, row_key, updated_at DESC);
```

Create `internal/projection/rulesettables/migrations/0002_content_hash_versioning.down.sql`:

```sql
-- Not safely reversible once more than one content_hash version of a row exists (would violate
-- the original (ruleset_id, table_name, row_key) primary key) — provided for symmetry/CI
-- dev-reset use only. Keeps only the newest row per key (by updated_at, then content_hash to
-- break an exact tie deterministically) before reverting the schema.
DELETE FROM ruleset_tables_read_model a
USING ruleset_tables_read_model b
WHERE a.ruleset_id = b.ruleset_id
  AND a.table_name = b.table_name
  AND a.row_key = b.row_key
  AND (a.updated_at, a.content_hash) < (b.updated_at, b.content_hash);
DROP INDEX IF EXISTS ruleset_tables_read_model_latest_idx;
ALTER TABLE ruleset_tables_read_model DROP CONSTRAINT ruleset_tables_read_model_pkey;
ALTER TABLE ruleset_tables_read_model ADD PRIMARY KEY (ruleset_id, table_name, row_key);
ALTER TABLE ruleset_tables_read_model DROP COLUMN content_hash;
```

- [ ] **Step 2: Verify the migration pairing needs no other file changes**

`scripts/migrate-up.sh:24` and `Dockerfile.migrate:20` both already reference the whole
`internal/projection/rulesettables/migrations` directory (not individual filenames) — confirm this
by inspecting both files, then prove it empirically:

```bash
docker build -f Dockerfile.migrate -t timadorus/migrate:backlogfixes .
docker run --rm --entrypoint find timadorus/migrate:backlogfixes \
    /migrations/internal/projection/rulesettables/migrations -type f
```

Expected: both `0001_ruleset_tables_read_model.{up,down}.sql` and
`0002_content_hash_versioning.{up,down}.sql` are listed. Then run a real migration against a
throwaway Postgres container to confirm `0002` actually applies cleanly on top of `0001` and every
other schema owner's migrations still apply (full `up` run, not just this one directory) — follow
the exact pattern used by this session's own prior branches' final reviews (`docker run --network
... -e DATABASE_URL=... timadorus/migrate:backlogfixes up`), then inspect the resulting schema:

```bash
psql "$DATABASE_URL" -c '\d ruleset_tables_read_model'
```

Expected: `content_hash` column present, NOT NULL, no default; primary key is
`(ruleset_id, table_name, row_key, content_hash)`; the new index is present.

- [ ] **Step 3: Update `testutil_test.go`'s and `repository_test.go`'s init scripts**

Both files currently pass only `.../0001_ruleset_tables_read_model.up.sql` to
`tcpostgres.WithOrderedInitScripts`. In `internal/engine/timadorus/tables/testutil_test.go`,
change:

```go
		tcpostgres.WithOrderedInitScripts(
			"../../../projection/rulesettables/migrations/0001_ruleset_tables_read_model.up.sql",
		),
```

to:

```go
		tcpostgres.WithOrderedInitScripts(
			"../../../projection/rulesettables/migrations/0001_ruleset_tables_read_model.up.sql",
			"../../../projection/rulesettables/migrations/0002_content_hash_versioning.up.sql",
		),
```

In `internal/query/rulesettables/repository_test.go`, make the equivalent change (same two
filenames, relative path `../../projection/rulesettables/migrations/...` — one fewer `../` than
`tables/testutil_test.go` since this file is one directory shallower).

- [ ] **Step 4: Update `sync.go`**

Replace the whole of `internal/engine/timadorus/tables/sync.go`'s `RegisterTables` function and
add the two new imports:

```go
import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)
```

```go
// RegisterTables upserts every row of every given table into ruleset_tables_read_model, scoped
// to rulesetID. Idempotent via ON CONFLICT DO NOTHING rather than a separate existence check —
// safe to call on every timadorus-engine startup and safe under concurrent replicas: a row whose
// content is unchanged since the last sync always computes the same content_hash, so a DO NOTHING
// no-op is the correct outcome. A row whose YAML content HAS changed computes a different hash,
// so it inserts as an additional, newer row instead of silently vanishing into the old
// ON CONFLICT target — see this table's own migration comment
// (0002_content_hash_versioning.up.sql) for why the primary key includes content_hash at all.
func RegisterTables(ctx context.Context, pool *pgxpool.Pool, rulesetID uuid.UUID, syncables ...Syncable) error {
	for _, table := range syncables {
		for rowKey, data := range table.RowData() {
			payload, err := json.Marshal(data)
			if err != nil {
				return fmt.Errorf("tables: marshal %s row %q: %w", table.TableName(), rowKey, err)
			}
			sum := sha256.Sum256(payload)
			contentHash := hex.EncodeToString(sum[:])
			if _, err := pool.Exec(ctx,
				`INSERT INTO ruleset_tables_read_model (ruleset_id, table_name, row_key, content_hash, data, updated_at)
				 VALUES ($1, $2, $3, $4, $5, now())
				 ON CONFLICT (ruleset_id, table_name, row_key, content_hash) DO NOTHING`,
				rulesetID, table.TableName(), rowKey, contentHash, payload,
			); err != nil {
				return fmt.Errorf("tables: upsert %s row %q: %w", table.TableName(), rowKey, err)
			}
		}
	}
	return nil
}
```

- [ ] **Step 5: Add the new sync-level test**

Add to `internal/engine/timadorus/tables/sync_test.go`:

```go
func TestRegisterTables_ContentChange_InsertsNewVersionInsteadOfDiscarding(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	rulesetID := uuid.New()

	v1 := &fakeTable{name: "fake", rows: map[string]fakeRow{"a": {Name: "Alpha"}}}
	if err := tables.RegisterTables(ctx, pool, rulesetID, v1); err != nil {
		t.Fatalf("first register: %v", err)
	}

	v2 := &fakeTable{name: "fake", rows: map[string]fakeRow{"a": {Name: "Alpha Fixed Typo"}}}
	if err := tables.RegisterTables(ctx, pool, rulesetID, v2); err != nil {
		t.Fatalf("second register (content changed): %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ruleset_tables_read_model WHERE ruleset_id = $1 AND table_name = $2 AND row_key = $3`,
		rulesetID, "fake", "a",
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("got %d rows for the edited key, want 2 (old content preserved as history, new content added as a new version)", count)
	}
}
```

- [ ] **Step 6: Run — confirm all `tables` package tests pass**

Run: `go test ./internal/engine/timadorus/tables/... -v`
Expected: PASS, including the pre-existing `TestRegisterTables_InsertsEveryRow`,
`TestRegisterTables_IdempotentOnSecondCall`,
`TestRegisterTables_SameTableNameDifferentRulesets_NoCollision` (all unaffected — none of them
change a row's content between calls), and the new test from Step 5.

- [ ] **Step 7: Update `repository.go`'s `List` and `Get`**

Replace `List`:

```go
// List returns every row of rulesetID's tableName table, ordered by row key — the newest version
// of each row if RegisterTables has synced more than one content_hash for the same key (see
// 0002_content_hash_versioning.up.sql).
func (r *Repository) List(ctx context.Context, rulesetID uuid.UUID, tableName string) ([]Row, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT DISTINCT ON (row_key) row_key, data FROM ruleset_tables_read_model
		 WHERE ruleset_id = $1 AND table_name = $2
		 ORDER BY row_key, updated_at DESC`,
		rulesetID, tableName,
	)
	if err != nil {
		return nil, fmt.Errorf("query/rulesettables: list %s for ruleset %s: %w", tableName, rulesetID, err)
	}
	defer rows.Close()

	var out []Row
	for rows.Next() {
		var row Row
		if err := rows.Scan(&row.Key, &row.Data); err != nil {
			return nil, fmt.Errorf("query/rulesettables: scan row: %w", err)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/rulesettables: iterate rows: %w", err)
	}
	return out, nil
}
```

Replace `Get`:

```go
// Get returns one row's data, or ErrNotFound — the newest version if more than one content_hash
// has been synced for this key (see List's doc comment).
func (r *Repository) Get(ctx context.Context, rulesetID uuid.UUID, tableName, rowKey string) (json.RawMessage, error) {
	var data json.RawMessage
	err := r.pool.QueryRow(ctx,
		`SELECT data FROM ruleset_tables_read_model
		 WHERE ruleset_id = $1 AND table_name = $2 AND row_key = $3
		 ORDER BY updated_at DESC
		 LIMIT 1`,
		rulesetID, tableName, rowKey,
	).Scan(&data)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("query/rulesettables: get %s/%s for ruleset %s: %w", tableName, rowKey, rulesetID, err)
	}
	return data, nil
}
```

- [ ] **Step 8: Update `repository_test.go`'s `seedRow` helper and add the new tests**

The existing `seedRow` helper omits `content_hash`, which is now `NOT NULL`. Replace it with two
helpers — one for the common case, one for explicit-timestamp tests — and add `"time"` to the
import block:

```go
func seedRow(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID, tableName, rowKey, contentHash, data string) {
	t.Helper()
	seedRowAt(t, pool, rulesetID, tableName, rowKey, contentHash, data, time.Now())
}

func seedRowAt(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID, tableName, rowKey, contentHash, data string, updatedAt time.Time) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO ruleset_tables_read_model (ruleset_id, table_name, row_key, content_hash, data, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6)`,
		rulesetID, tableName, rowKey, contentHash, data, updatedAt,
	); err != nil {
		t.Fatalf("seed row: %v", err)
	}
}
```

Update every existing `seedRow(...)` call site in this file to pass a `contentHash` argument
before `data` — a fixed placeholder like `"h1"` is fine for every existing test (none of them seed
two versions of the same key), e.g.:
`seedRow(t, pool, rulesetID, "traits", "strong", "h1", `{"displayName":"Strong"}`)`.

Add two new tests proving the "latest wins" behavior:

```go
func TestRepository_Get_ReturnsNewestVersion_WhenRowHasMultipleContentHashes(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)
	rulesetID := uuid.New()
	older := time.Now().Add(-time.Hour)
	newer := time.Now()

	seedRowAt(t, pool, rulesetID, "traits", "strong", "hash-v1", `{"displayName":"Strong"}`, older)
	seedRowAt(t, pool, rulesetID, "traits", "strong", "hash-v2", `{"displayName":"Really Strong"}`, newer)

	data, err := repo.Get(context.Background(), rulesetID, "traits", "strong")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	var got struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.DisplayName != "Really Strong" {
		t.Fatalf("got %q, want the newer version's displayName %q", got.DisplayName, "Really Strong")
	}
}

func TestRepository_List_ReturnsNewestVersionPerKey(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)
	rulesetID := uuid.New()
	older := time.Now().Add(-time.Hour)
	newer := time.Now()

	seedRowAt(t, pool, rulesetID, "traits", "strong", "hash-v1", `{"displayName":"Strong"}`, older)
	seedRowAt(t, pool, rulesetID, "traits", "strong", "hash-v2", `{"displayName":"Really Strong"}`, newer)
	seedRowAt(t, pool, rulesetID, "traits", "agile", "hash-a1", `{"displayName":"Agile"}`, older)

	rows, err := repo.List(context.Background(), rulesetID, "traits")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (one per row_key, not one per content_hash version)", len(rows))
	}
	if rows[0].Key != "agile" || rows[1].Key != "strong" {
		t.Fatalf("got keys [%s %s], want [agile strong]", rows[0].Key, rows[1].Key)
	}
	var strongData struct {
		DisplayName string `json:"displayName"`
	}
	if err := json.Unmarshal(rows[1].Data, &strongData); err != nil {
		t.Fatalf("unmarshal strong: %v", err)
	}
	if strongData.DisplayName != "Really Strong" {
		t.Fatalf("got %q for strong, want the newer version", strongData.DisplayName)
	}
}
```

- [ ] **Step 9: Run — confirm the full `rulesettables` query package passes**

Run: `go test ./internal/query/rulesettables/... -v`
Expected: PASS, all existing tests (updated `seedRow` call sites included) plus the two new ones.

- [ ] **Step 10: Full regression pass**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: entirely clean — this task touches a schema used by `internal/httpapi/query/server.go`'s
`ListRulesetTableRows`/`GetRulesetTableRow` handlers, which call straight into `Repository.List`/
`Get` with no other logic, so no handler-level change is needed, but confirm no handler test
breaks.

- [ ] **Step 11: Update BACKLOG.md**

Replace the `Edits to a table YAML file never propagate to already-synced clusters...` bullet
with:

```markdown
- [x] **Fixed.** `ruleset_tables_read_model`'s primary key now includes a `content_hash` column
  (`0002_content_hash_versioning.up.sql`). `RegisterTables` computes a sha256 of each row's
  marshaled content, so an edited row's new content inserts as an additional, newer row instead of
  being silently discarded by the old `ON CONFLICT (ruleset_id, table_name, row_key) DO NOTHING`.
  `internal/query/rulesettables.Repository.List`/`Get` always resolve the newest row per key by
  `updated_at`, so callers still see exactly one row per key — the current one.
```

- [ ] **Step 12: Commit**

```bash
git add internal/projection/rulesettables/migrations/0002_content_hash_versioning.up.sql \
        internal/projection/rulesettables/migrations/0002_content_hash_versioning.down.sql \
        internal/engine/timadorus/tables/sync.go \
        internal/engine/timadorus/tables/sync_test.go \
        internal/engine/timadorus/tables/testutil_test.go \
        internal/query/rulesettables/repository.go \
        internal/query/rulesettables/repository_test.go \
        docs/BACKLOG.md
git commit -m "rulesettables: content-hash versioning so table YAML edits reach synced clusters"
```

---

### Task 5: e2e coverage for the ruleset-tables query endpoints

**Files:**
- Modify: `test/e2e/e2e_test.go`

**Interfaces:**
- Consumes: `querygen.Ruleset{Id, Name, ...}`, `querygen.RulesetTableRow{Key string, Data
  map[string]interface{}}` (already generated in `api/query/gen`), the package-level `env
  *e2eutil.Environment` and `doJSON` helper already defined in this file.
- Produces: nothing consumed by other tasks.

This test relies on `cmd/timadorus-engine` having already registered the "Timadorus" Ruleset and
synced `traits.yaml` at cluster startup — it does not create its own Ruleset for this (the
existing giant `It` block creates an unrelated `"e2e-ruleset"`, which is never a Ruleset named
`"Timadorus"` and never gets tables synced to it).

- [ ] **Step 1: Add the new `It` block**

Add a new, independent `It` inside the existing `Describe("Timadorus platform aggregates", ...)`
block in `test/e2e/e2e_test.go`, after the closing `})` of the existing
`"creates one of each aggregate and reads them back correctly"` block:

```go
	It("the Timadorus ruleset's data tables are synced and queryable through the query API", func() {
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

		var traitRows []querygen.RulesetTableRow
		resp, err = doJSON(http.MethodGet, fmt.Sprintf("%s/rulesets/%s/tables/traits", env.QueryAPIBaseURL, timadorusRuleset.Id), env.BearerToken, nil, &traitRows)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		// strong, agile, quick — internal/engine/timadorus/tables/traits.yaml's three rows.
		Expect(traitRows).To(HaveLen(3))
		Expect(traitRows).To(ContainElement(HaveField("Key", "strong")))
		Expect(traitRows).To(ContainElement(HaveField("Key", "agile")))
		Expect(traitRows).To(ContainElement(HaveField("Key", "quick")))

		var strongRow map[string]any
		resp, err = doJSON(http.MethodGet, fmt.Sprintf("%s/rulesets/%s/tables/traits/strong", env.QueryAPIBaseURL, timadorusRuleset.Id), env.BearerToken, nil, &strongRow)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(strongRow["displayName"]).To(Equal("Strong"))

		resp, err = doJSON(http.MethodGet, fmt.Sprintf("%s/rulesets/%s/tables/traits/nonexistent-row", env.QueryAPIBaseURL, timadorusRuleset.Id), env.BearerToken, nil, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusNotFound))
	})
```

- [ ] **Step 2: Run against a real cluster**

This is an e2e test (`//go:build e2e`) — it needs a real running cluster with `cmd/timadorus-engine`
having completed its startup sync. Follow this repo's existing e2e-run convention (check
`test/e2e/README.md` or however the existing suite is normally invoked, e.g. via `make dev-up`
against a devcluster, then `go test -tags e2e ./test/e2e/...`). Expected: PASS, alongside every
pre-existing `It` in this file.

- [ ] **Step 3: Update BACKLOG.md**

Replace the `No end-to-end coverage for the two new query-api endpoints...` bullet with:

```markdown
- [x] **Fixed.** `test/e2e/e2e_test.go` now has a dedicated `It` asserting
  `GET /rulesets/{timadorusRulesetId}/tables/traits` returns all 3 seeded rows and
  `GET .../tables/traits/strong` returns the expected row content, resolving the "Timadorus"
  Ruleset by name from `GET /rulesets` rather than assuming a fixed id — covering the startup
  sync, both endpoints, and the migration image all in one test.
```

- [ ] **Step 4: Commit**

```bash
git add test/e2e/e2e_test.go docs/BACKLOG.md
git commit -m "test/e2e: cover the ruleset-tables query endpoints against the Timadorus ruleset"
```

---

## Final Verification

- `go build ./... && go vet ./...` clean.
- `go test ./...` clean (full suite, `-count=1` at least once to bypass cache).
- `go test -race ./internal/engine/timadorus/...` clean.
- `docker build -f Dockerfile.migrate ...` + a real `up` run against a throwaway Postgres
  confirms `0002_content_hash_versioning` applies and the resulting schema matches Task 4 Step 2's
  expectations.
- Full e2e suite passes (`-tags e2e`), including Task 5's new `It`.
- `docs/BACKLOG.md`'s `timadorus-engine` section has all five fixed bullets replaced with `[x]`
  summaries; the remaining five items (architecture note, two backfill items, embedding-size
  threshold) are untouched.
