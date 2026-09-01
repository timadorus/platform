# Ruleset Data Tables Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Load `internal/engine/timadorus/tables/traits.yaml` (and future sibling table files) into
concrete, hand-written Go structures with per-row hook registration, and sync their data into a
new, generic read-model table exposed through the query API — idempotently, at
`cmd/timadorus-engine` startup.

**Architecture:** Each table file gets its own Go file in `internal/engine/timadorus/tables`
defining a concrete row struct (`Key` field first, YAML-tagged data fields, `Hooks` field last)
and a small wrapper type implementing a shared `Syncable` interface. One generic Postgres table
(`ruleset_tables_read_model`, keyed by `ruleset_id, table_name, row_key`) backs every table file,
read through a new `internal/query/rulesettables` package and two new query-api endpoints.
Resolving the real Ruleset id `RegisterTables` needs (without depending on the independently-racing
read-model projector) requires widening the existing `ruleset_names` reservation table with an `id`
column and changing `RegisterRuleset`'s signature to return it.

**Tech Stack:** Go, `gopkg.in/yaml.v3` (already an indirect dependency — this plan promotes it to
direct), `//go:embed`, `jackc/pgx/v5`, `oapi-codegen` (already wired), testcontainers-go.

## Global Constraints

- Table data is immutable for a given Ruleset — no command surface, no event type for a row
  changing. Table rows are seeded reference data, not event-sourced aggregates.
- One concrete Go struct per table file (`TraitsRow`, and whichever structs follow it) — no
  generic `Table[T]`/`Row[T]` container. Every such struct's first field is `Key` (set by the
  loader from the YAML file's outer map key, not itself a YAML field) and its last field is
  `Hooks` (`[]Hook`, never populated by YAML, never serialized to JSON).
- `Hook`'s signature is `func(ctx context.Context, tx pgx.Tx, env bus.Envelope) error` — identical
  to `projection.Projector.Handle`'s. Multiple hooks per row run in registration order via
  `RunHooks`, stopping at the first error.
- The read-model upsert logic is shared once via a `Syncable` interface (`TableName() string`,
  `RowData() map[string]any`) — not a generic type parameter.
- One generic Postgres table across all table files: `ruleset_tables_read_model(ruleset_id,
  table_name, row_key, data, updated_at)`, primary key `(ruleset_id, table_name, row_key)`.
  Table names are unique within a Ruleset, not globally.
- `RegisterTables` upserts via `ON CONFLICT (ruleset_id, table_name, row_key) DO NOTHING` — no
  separate existence check, safe under concurrent replicas and repeated restarts.
- No actual `Dispatch`/hook-invocation call site in this plan — no existing event references a
  table row by key yet. This plan delivers the mechanism only.
- No change to `internal/domain/*` beyond none — `ruleset_names` (a command-side reservation
  table, not a domain package) is the only existing schema this plan widens.

---

### Task 1: `ruleset_names.id` column, `Service.FindIDByName`, `RegisterRuleset`'s new signature

**Files:**
- Create: `internal/command/ruleset/migrations/0002_ruleset_names_id.up.sql`
- Create: `internal/command/ruleset/migrations/0002_ruleset_names_id.down.sql`
- Modify: `internal/command/ruleset/service.go`
- Modify: `internal/command/ruleset/service_test.go`
- Modify: `internal/engine/timadorus/register.go`
- Modify: `internal/engine/timadorus/register_test.go`
- Modify: `internal/engine/timadorus/testutil_test.go`

**Interfaces:**
- Consumes: `postgres.NewUnitOfWork`, `postgres.TxFromContext`, `postgres.IsUniqueViolation`
  (unchanged, already used by `Service.Create`).
- Produces: `Service.FindIDByName(ctx, name string) (uuid.UUID, error)` (new);
  `RegisterRuleset(ctx, pool) (uuid.UUID, error)` (signature changed from `(ctx, pool) error`) —
  Task 6 depends on this new return value.

- [ ] **Step 1: Add the migration**

Create `internal/command/ruleset/migrations/0002_ruleset_names_id.up.sql`:

```sql
-- Adds an id column to the name-reservation table so a caller with only the name (e.g.
-- RegisterRuleset's "already exists" branch) can resolve the aggregate's real id without
-- depending on rulesets_read_model, which is written by a different, independently-racing
-- projector consuming the same RulesetCreated event this reservation is paired with (see the
-- campaign-creation-default-traits branch's final review for the identical class of race in a
-- sibling read path, fixed there by resolving from the event store instead — this is the same
-- fix, applied one layer up: the reservation table already lives in the same durable
-- transaction as Create's aggregate save, so widening it costs no new race).
--
-- Nullable, not NOT NULL: any row reserved before this migration ships has no id to backfill
-- from, the same accepted, documented gap 0001_ruleset_names.up.sql's own comment already makes
-- for a different column. This platform's only Ruleset today, "Timadorus", is registered
-- exclusively via the code this migration ships alongside, so a real cluster only hits this in
-- transition on an in-place upgrade of a pre-existing dev cluster — reset it instead, same as
-- 0001's own advice.
ALTER TABLE ruleset_names ADD COLUMN id UUID;
```

Create `internal/command/ruleset/migrations/0002_ruleset_names_id.down.sql`:

```sql
ALTER TABLE ruleset_names DROP COLUMN id;
```

- [ ] **Step 2: Update `Service.Create` to populate the new column**

In `internal/command/ruleset/service.go`, change:

```go
	if _, err := tx.Exec(ctx, `INSERT INTO ruleset_names (name) VALUES ($1)`, name); err != nil {
```

to:

```go
	if _, err := tx.Exec(ctx, `INSERT INTO ruleset_names (name, id) VALUES ($1, $2)`, name, r.AggregateID()); err != nil {
```

No other line in `Create` changes.

- [ ] **Step 3: Add `Service.FindIDByName`**

In `internal/command/ruleset/service.go`, add this method after `Create`:

```go
// FindIDByName resolves an existing Ruleset's id from its name-reservation row — race-free by
// construction, since ruleset_names.id is populated in the exact same transaction Create already
// uses to reserve the name (see Create's own comment, and migrations/0002_ruleset_names_id.up.sql).
// Returns an error (not a sentinel "not found") both when the name was never reserved at all and
// when it was reserved by a row created before the id column existed — both are genuine failures
// a caller should surface loudly, not silently paper over.
func (s *Service) FindIDByName(ctx context.Context, name string) (uuid.UUID, error) {
	var id *uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT id FROM ruleset_names WHERE name = $1`, name).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("ruleset: find id for name %q: %w", name, err)
	}
	if id == nil {
		return uuid.Nil, fmt.Errorf("ruleset: name %q was reserved before the id column existed (pre-migration row)", name)
	}
	return *id, nil
}
```

No new imports needed — `uuid`, `fmt`, `context` are already imported in this file.

- [ ] **Step 4: Add test coverage for `FindIDByName`**

In `internal/command/ruleset/service_test.go`, add `"migrations/0002_ruleset_names_id.up.sql"` to
`newTestPool`'s `WithOrderedInitScripts` list, immediately after the existing
`"migrations/0001_ruleset_names.up.sql"` entry.

Add these two tests after `TestService_Create_EmptyName_FailsBeforeTouchingDB`:

```go
func TestService_FindIDByName_ReturnsTheIDCreateReserved(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	id, err := service.Create(ctx, "GURPS", "", nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	found, err := service.FindIDByName(ctx, "GURPS")
	if err != nil {
		t.Fatalf("find id by name: %v", err)
	}
	if found != id {
		t.Fatalf("got id %s, want %s", found, id)
	}
}

func TestService_FindIDByName_UnknownName_ReturnsError(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	if _, err := service.FindIDByName(ctx, "NeverCreated"); err == nil {
		t.Fatal("got nil error for an unreserved name, want a real error")
	}
}
```

Run: `go test ./internal/command/ruleset/... -v`
Expected: all tests pass, including the two new ones and the two pre-existing ones (which must
keep passing unmodified — confirm `TestService_Create_DuplicateName_ReturnsErrNameAlreadyExists`
still passes now that the INSERT includes the new `id` column).

- [ ] **Step 5: Update `RegisterRuleset`'s signature and body**

In `internal/engine/timadorus/register.go`, replace the whole function (keep the doc comment's
first two paragraphs, extend the rest):

```go
// RegisterRuleset ensures a Ruleset named TargetRulesetName exists, creating it on this
// platform's first startup, and returns its id either way — callers (the table-data sync in
// internal/engine/timadorus/tables) need the real id to scope rows to this Ruleset. Builds its
// own scoped registry/store/repo/service, mirroring exactly how
// NewCharacterProcessor/NewCampaignProcessor already build their own scoped repos — this
// function is only ever called once, at cmd/timadorus-engine startup, so there's no shared state
// to inject from outside.
//
// errors.Is(err, ruleset.ErrNameAlreadyExists) is handled by resolving the existing id via
// Service.FindIDByName — the only outcome besides a clean create treated as success (idempotent
// across restarts). Any other error is returned as-is: the caller (cmd/timadorus-engine/main.go's
// run()) terminates the process on it, since a Campaign referencing this Ruleset by name has no
// other way to discover it if registration silently failed.
//
// The "already exists" check only ever looks at the ruleset_names reservation, not at whether a
// Ruleset named TargetRulesetName actually exists right now: ruleset.Service.Rename never
// releases or re-reserves names (see Service.Create's doc comment), so if the "Timadorus" Ruleset
// is ever renamed away, this reservation survives and every later startup still treats the
// platform as registered — even though no Ruleset is actually named TargetRulesetName any more.
// FindIDByName still resolves the ORIGINAL Ruleset's id correctly in that case (the reservation's
// id column is set once at Create and never changes), which is arguably the more useful behavior
// anyway — that Ruleset still exists, just under a different name.
func RegisterRuleset(ctx context.Context, pool *pgxpool.Pool) (uuid.UUID, error) {
	registry := eventsourcing.NewRegistry()
	rulesetevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, ruleset.AggregateType, func() *ruleset.Ruleset {
		return &ruleset.Ruleset{}
	})
	service := rulesetcmd.NewService(repo, pool)

	id, err := service.Create(ctx, TargetRulesetName, "", nil)
	if errors.Is(err, ruleset.ErrNameAlreadyExists) {
		id, err := service.FindIDByName(ctx, TargetRulesetName)
		if err != nil {
			return uuid.Nil, fmt.Errorf(errPrefix+"resolve existing ruleset %q: %w", TargetRulesetName, err)
		}
		return id, nil
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf(errPrefix+"register ruleset %q: %w", TargetRulesetName, err)
	}
	return id, nil
}
```

Add `"github.com/google/uuid"` to the import block, grouped with the other third-party imports.

- [ ] **Step 6: Update `register_test.go` for the new signature**

In `internal/engine/timadorus/register_test.go`, update every call site:

```go
func TestRegisterRuleset_CreatesOnFirstCall(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	id, err := timadorus.RegisterRuleset(ctx, pool)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("got nil id")
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ruleset_names WHERE name = $1`, timadorus.TargetRulesetName).Scan(&count); err != nil {
		t.Fatalf("count ruleset_names: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d ruleset_names rows for %q, want 1", count, timadorus.TargetRulesetName)
	}
}

func TestRegisterRuleset_NoOpOnSecondCall(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	firstID, err := timadorus.RegisterRuleset(ctx, pool)
	if err != nil {
		t.Fatalf("first register: %v", err)
	}
	secondID, err := timadorus.RegisterRuleset(ctx, pool)
	if err != nil {
		t.Fatalf("second register: %v", err)
	}
	if secondID != firstID {
		t.Fatalf("got second-call id %s, want it to match the first call's id %s", secondID, firstID)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE event_type = $1`, "ruleset.created.v1").Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d ruleset.created.v1 events after two registrations, want 1", count)
	}
}

func TestRegisterRuleset_PropagatesRealError(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	pool.Close() // guarantees RegisterRuleset's own pool.Begin fails: a real error, not ErrNameAlreadyExists

	_, err := timadorus.RegisterRuleset(ctx, pool)
	if err == nil {
		t.Fatal("got nil error from a closed pool, want a real error")
	}
	if errors.Is(err, ruleset.ErrNameAlreadyExists) {
		t.Fatalf("got ErrNameAlreadyExists from a closed pool, want a genuine failure: %v", err)
	}
}
```

(`TestTargetRulesetName_MatchesProcessorTarget` is unaffected — leave it exactly as it is.) Add
`"github.com/google/uuid"` to the import block.

- [ ] **Step 7: Add the new migration to the shared test pool's init scripts**

In `internal/engine/timadorus/testutil_test.go`, add
`"../../command/ruleset/migrations/0002_ruleset_names_id.up.sql"` to `newTestPool`'s
`WithOrderedInitScripts` list, immediately after the existing
`"../../command/ruleset/migrations/0001_ruleset_names.up.sql"` entry.

- [ ] **Step 8: Run this package's tests**

Run: `go test ./internal/command/ruleset/... ./internal/engine/timadorus/... -v`
Expected: every test passes, including all of `internal/engine/timadorus`'s pre-existing tests
(unaffected by this task other than the two files touched above) and `internal/command/ruleset`'s
full suite.

- [ ] **Step 9: Repo-wide verification**

Run: `go build ./... && go vet ./...`
Expected: clean. `cmd/timadorus-engine/main.go` will currently FAIL to build after this step,
because it still calls `RegisterRuleset` expecting a single `error` return — this is expected and
fixed in Task 6; do not modify `cmd/timadorus-engine/main.go` in this task. Confirm the build
failure is exactly this one call site and nothing else (`go build ./cmd/timadorus-engine/...`
should be the only failing package).

- [ ] **Step 10: Commit**

```bash
git add internal/command/ruleset/migrations/0002_ruleset_names_id.up.sql internal/command/ruleset/migrations/0002_ruleset_names_id.down.sql internal/command/ruleset/service.go internal/command/ruleset/service_test.go internal/engine/timadorus/register.go internal/engine/timadorus/register_test.go internal/engine/timadorus/testutil_test.go
git commit -m "ruleset: widen ruleset_names with an id column so RegisterRuleset can return the Ruleset's id race-free

Adds Service.FindIDByName, resolving an existing Ruleset's id from the same durable transaction
Create already reserves its name in — avoiding a dependency on rulesets_read_model, written by a
different, independently-racing projector. RegisterRuleset now returns (uuid.UUID, error); its
one caller, cmd/timadorus-engine/main.go, is updated in a later task."
```

---

### Task 2: `ruleset_tables_read_model` migration

**Files:**
- Create: `internal/projection/rulesettables/migrations/0001_ruleset_tables_read_model.up.sql`
- Create: `internal/projection/rulesettables/migrations/0001_ruleset_tables_read_model.down.sql`
- Modify: `scripts/migrate-up.sh`

**Interfaces:**
- Produces: the `ruleset_tables_read_model` table — Tasks 3 and 4 both depend on this schema
  existing (in their own testcontainers pools) and on the real migration being wired into
  `scripts/migrate-up.sh` for `make migrate-up`/the Helm migration Job.

- [ ] **Step 1: Add the migration**

Create `internal/projection/rulesettables/migrations/0001_ruleset_tables_read_model.up.sql`:

```sql
-- One generic table across every ruleset-engine data table file (traits.yaml today, more to
-- follow) rather than one bespoke table per file — table shapes vary per file, so `data` is an
-- opaque JSON payload, the same pattern already used for Campaign.configuration and
-- Character.info elsewhere in this platform. Table names are unique WITHIN a Ruleset (the
-- primary key), not globally — a different Ruleset can reuse a table name like "traits" for its
-- own, unrelated rows without collision.
--
-- Not written by a projection.Projector reacting to bus events — timadorus-engine writes it
-- directly at startup from embedded YAML files (see internal/engine/timadorus/tables), since
-- table rows have no event-sourced lifecycle (plan §Context: rule changes are always modeled as
-- new Rulesets, never as edits to an existing one's tables). It lives under internal/projection
-- purely by the same "migrations grouped with the read model they define" convention every other
-- read-model schema already follows, for discoverability by both the writer
-- (cmd/timadorus-engine) and the reader (cmd/query-api).
CREATE TABLE ruleset_tables_read_model (
    ruleset_id UUID NOT NULL,
    table_name TEXT NOT NULL,
    row_key    TEXT NOT NULL,
    data       JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (ruleset_id, table_name, row_key)
);
```

Create `internal/projection/rulesettables/migrations/0001_ruleset_tables_read_model.down.sql`:

```sql
DROP TABLE ruleset_tables_read_model;
```

- [ ] **Step 2: Wire it into `scripts/migrate-up.sh`**

In `scripts/migrate-up.sh`, add this line to the `schema_owners` array, immediately after the
existing `"projection_ruleset:internal/projection/ruleset/migrations"` entry:

```bash
  "projection_ruleset_tables:internal/projection/rulesettables/migrations"
```

- [ ] **Step 3: Verify the migration applies cleanly**

This plan's later tasks exercise this schema via testcontainers-go (which applies raw `.sql` init
scripts directly, not through `golang-migrate`), so there's no dedicated runnable check for this
step in isolation beyond reading the SQL for syntax correctness. Confirm by eye that the file
matches every other migration's exact style in this repo (no trailing semicolon issues, same
`CREATE TABLE`/`DROP TABLE` shape as `internal/projection/ruleset/migrations/0001_ruleset_read_model.up.sql`).

- [ ] **Step 4: Commit**

```bash
git add internal/projection/rulesettables/migrations/0001_ruleset_tables_read_model.up.sql internal/projection/rulesettables/migrations/0001_ruleset_tables_read_model.down.sql scripts/migrate-up.sh
git commit -m "projection: add the generic ruleset_tables_read_model schema for ruleset data tables"
```

---

### Task 3: `internal/engine/timadorus/tables` package — `Hook`, `Syncable`, `RegisterTables`, and `traits.go`

**Files:**
- Create: `internal/engine/timadorus/tables/hook.go`
- Create: `internal/engine/timadorus/tables/sync.go`
- Create: `internal/engine/timadorus/tables/sync_test.go`
- Create: `internal/engine/timadorus/tables/embed.go`
- Create: `internal/engine/timadorus/tables/traits.go`
- Create: `internal/engine/timadorus/tables/traits_test.go`
- Create: `internal/engine/timadorus/tables/testutil_test.go`

**Interfaces:**
- Consumes: `internal/bus.Envelope` (unchanged), Task 2's `ruleset_tables_read_model` schema (for
  this task's own tests).
- Produces: `tables.Hook`, `tables.Syncable`, `tables.RegisterTables(ctx, pool, rulesetID,
  syncables ...Syncable) error`, `tables.LoadTraits() (*tables.TraitsTable, error)`,
  `(*TraitsTable).RegisterHook(key string, h Hook) error`, `(*TraitsTable).Dispatch(ctx, tx, key,
  env) error` — Task 6 depends on `tables.LoadTraits` and `tables.RegisterTables`.

- [ ] **Step 1: Promote `gopkg.in/yaml.v3` to a direct dependency**

Run: `go get gopkg.in/yaml.v3@v3.0.1` (from the repo root) — this is already in `go.sum` as an
indirect dependency at exactly this version, so this should only remove the `// indirect` comment
in `go.mod`, not change the resolved version. Confirm `go.mod`'s diff is exactly that one-line
change (no version bump) before proceeding.

- [ ] **Step 2: Add `Hook`**

Create `internal/engine/timadorus/tables/hook.go`:

```go
package tables

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
)

// Hook is invoked when some future event handler decides a table row is relevant to an incoming
// event — table rows have no event-sourced lifecycle of their own (see this package's own doc
// comment), so nothing in this codebase yet decides that; this type exists so that future
// decision can attach behavior to a specific row once it does. Its signature deliberately
// matches projection.Projector.Handle's exactly: a hook can do anything a full event processor
// can (load/save aggregates within the same transaction), just scoped to reacting on behalf of
// one row instead of a whole event type.
type Hook func(ctx context.Context, tx pgx.Tx, env bus.Envelope) error
```

- [ ] **Step 3: Add `Syncable` and `RegisterTables`**

Create `internal/engine/timadorus/tables/sync.go`:

```go
// Package tables loads internal/engine/timadorus/tables/*.yaml (traits.yaml today, more to
// follow) into concrete, hand-written Go structures — one per file, e.g. TraitsRow for
// traits.yaml — each with a Key field first, YAML-tagged data fields, and a Hooks field last for
// attaching function hooks to individual rows (see hook.go). Table data is immutable for a given
// Ruleset: rule changes are always modeled as new Rulesets, never as edits to an existing one's
// tables, so these rows have no event-sourced lifecycle, no command surface, and are synced into
// the read model as a one-shot, idempotent startup step (RegisterTables) rather than projected
// from events.
package tables

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Syncable is implemented by every table's wrapper type (TraitsTable, and whichever follow it) so
// RegisterTables's upsert loop lives in one place instead of being copy-adapted per table file —
// a small, targeted sharing of real logic, deliberately not a generic container type (see this
// package's own file layout: every row struct stays concrete and hand-written).
type Syncable interface {
	// TableName is this table's stable name, used as part of the read model's primary key —
	// matches the YAML file's base name (e.g. "traits" for traits.yaml).
	TableName() string
	// RowData returns each row keyed by RowKey(), as a plain value ready for json.Marshal — Key
	// and Hooks are already tagged `json:"-"` on every row struct, so this can just be the row
	// values themselves.
	RowData() map[string]any
}

// RegisterTables upserts every row of every given table into ruleset_tables_read_model, scoped
// to rulesetID. Idempotent via ON CONFLICT DO NOTHING rather than a separate existence check —
// safe to call on every timadorus-engine startup and safe under concurrent replicas, since table
// data never changes for a given Ruleset (see this package's own doc comment) and a DO NOTHING
// no-op is therefore always the correct outcome for a row that already exists, forever.
func RegisterTables(ctx context.Context, pool *pgxpool.Pool, rulesetID uuid.UUID, syncables ...Syncable) error {
	for _, table := range syncables {
		for rowKey, data := range table.RowData() {
			payload, err := json.Marshal(data)
			if err != nil {
				return fmt.Errorf("tables: marshal %s row %q: %w", table.TableName(), rowKey, err)
			}
			if _, err := pool.Exec(ctx,
				`INSERT INTO ruleset_tables_read_model (ruleset_id, table_name, row_key, data, updated_at)
				 VALUES ($1, $2, $3, $4, now())
				 ON CONFLICT (ruleset_id, table_name, row_key) DO NOTHING`,
				rulesetID, table.TableName(), rowKey, payload,
			); err != nil {
				return fmt.Errorf("tables: upsert %s row %q: %w", table.TableName(), rowKey, err)
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Add the embed directive**

Create `internal/engine/timadorus/tables/embed.go`:

```go
package tables

import "embed"

// dataFiles embeds every YAML table file shipped in this directory, so the engine binary stays
// self-contained (no runtime file access needed) — matches this codebase's existing
// embedded-OpenAPI-spec convention (api/query/doc.go, api/command/doc.go).
//
//go:embed *.yaml
var dataFiles embed.FS
```

- [ ] **Step 5: Add `TraitsRow`/`TraitsTable`/`LoadTraits`**

Create `internal/engine/timadorus/tables/traits.go`:

```go
package tables

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"gopkg.in/yaml.v3"

	"github.com/timadorus/platform/internal/bus"
)

// TraitsRow is traits.yaml's row shape: Key first (set by LoadTraits from the row's own map key,
// not itself a YAML field), the YAML-tagged data columns, Hooks last (never populated by YAML,
// never serialized — see Hook's own doc comment for why a row can have more than one).
type TraitsRow struct {
	Key         string `yaml:"-" json:"-"`
	DisplayName string `yaml:"displayName" json:"displayName"`
	Description string `yaml:"description" json:"description"`
	Hooks       []Hook `yaml:"-" json:"-"`
}

func (r *TraitsRow) RowKey() string { return r.Key }
func (r *TraitsRow) AddHook(h Hook) { r.Hooks = append(r.Hooks, h) }

// RunHooks invokes every hook registered on this row in registration order, stopping at the
// first error.
func (r *TraitsRow) RunHooks(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	for _, h := range r.Hooks {
		if err := h(ctx, tx, env); err != nil {
			return err
		}
	}
	return nil
}

// TraitsTable is traits.yaml's in-memory table, keyed by row key (e.g. "strong").
type TraitsTable struct {
	rows map[string]*TraitsRow
}

// LoadTraits parses the embedded traits.yaml. The file's single top-level key ("traits:") is
// treated permissively — LoadTraits unwraps whatever single top-level key is present rather than
// requiring it to match the filename, so a future table file's author isn't forced into an
// exact-name convention for a key that's otherwise redundant with the filename itself.
func LoadTraits() (*TraitsTable, error) {
	raw, err := dataFiles.ReadFile("traits.yaml")
	if err != nil {
		return nil, fmt.Errorf("tables: read traits.yaml: %w", err)
	}

	var wrapper map[string]map[string]TraitsRow
	if err := yaml.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("tables: parse traits.yaml: %w", err)
	}
	if len(wrapper) != 1 {
		return nil, fmt.Errorf("tables: traits.yaml: expected exactly one top-level key, got %d", len(wrapper))
	}

	var rowsMap map[string]TraitsRow
	for _, v := range wrapper {
		rowsMap = v
	}

	rows := make(map[string]*TraitsRow, len(rowsMap))
	for key, row := range rowsMap {
		row := row
		row.Key = key
		rows[key] = &row
	}
	return &TraitsTable{rows: rows}, nil
}

// Row returns the row for key, or false if it doesn't exist.
func (t *TraitsTable) Row(key string) (*TraitsRow, bool) {
	r, ok := t.rows[key]
	return r, ok
}

// RegisterHook attaches h to the row at key, returning an error if key doesn't exist rather than
// silently no-op'ing (a typo'd row key should fail loudly at registration time, not silently
// never fire).
func (t *TraitsTable) RegisterHook(key string, h Hook) error {
	row, ok := t.rows[key]
	if !ok {
		return fmt.Errorf("tables: traits: unknown row %q", key)
	}
	row.AddHook(h)
	return nil
}

// Dispatch runs every hook registered on the row at key.
func (t *TraitsTable) Dispatch(ctx context.Context, tx pgx.Tx, key string, env bus.Envelope) error {
	row, ok := t.rows[key]
	if !ok {
		return fmt.Errorf("tables: traits: unknown row %q", key)
	}
	return row.RunHooks(ctx, tx, env)
}

func (t *TraitsTable) TableName() string { return "traits" }

func (t *TraitsTable) RowData() map[string]any {
	out := make(map[string]any, len(t.rows))
	for k, v := range t.rows {
		out[k] = *v
	}
	return out
}

var _ Syncable = (*TraitsTable)(nil)
```

- [ ] **Step 6: Add the package's own testcontainers pool helper**

Create `internal/engine/timadorus/tables/testutil_test.go`:

```go
package tables_test

import (
	"context"
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
			"../../../projection/rulesettables/migrations/0001_ruleset_tables_read_model.up.sql",
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
```

- [ ] **Step 7: Add `sync_test.go`**

Create `internal/engine/timadorus/tables/sync_test.go`:

```go
package tables_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/engine/timadorus/tables"
)

// fakeRow/fakeTable are a minimal hand-rolled Syncable — RegisterTables's own tests don't need a
// real table file, just something implementing the interface.
type fakeRow struct {
	Name string `json:"name"`
}

type fakeTable struct {
	name string
	rows map[string]fakeRow
}

func (f *fakeTable) TableName() string { return f.name }
func (f *fakeTable) RowData() map[string]any {
	out := make(map[string]any, len(f.rows))
	for k, v := range f.rows {
		out[k] = v
	}
	return out
}

func TestRegisterTables_InsertsEveryRow(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	rulesetID := uuid.New()

	table := &fakeTable{name: "fake", rows: map[string]fakeRow{
		"a": {Name: "Alpha"},
		"b": {Name: "Bravo"},
	}}

	if err := tables.RegisterTables(ctx, pool, rulesetID, table); err != nil {
		t.Fatalf("register tables: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ruleset_tables_read_model WHERE ruleset_id = $1 AND table_name = $2`,
		rulesetID, "fake",
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("got %d rows, want 2", count)
	}

	var payload []byte
	if err := pool.QueryRow(ctx,
		`SELECT data FROM ruleset_tables_read_model WHERE ruleset_id = $1 AND table_name = $2 AND row_key = $3`,
		rulesetID, "fake", "a",
	).Scan(&payload); err != nil {
		t.Fatalf("select row a: %v", err)
	}
	var decoded fakeRow
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if decoded.Name != "Alpha" {
		t.Fatalf("got name %q, want %q", decoded.Name, "Alpha")
	}
}

func TestRegisterTables_IdempotentOnSecondCall(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	rulesetID := uuid.New()

	table := &fakeTable{name: "fake", rows: map[string]fakeRow{"a": {Name: "Alpha"}}}

	if err := tables.RegisterTables(ctx, pool, rulesetID, table); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := tables.RegisterTables(ctx, pool, rulesetID, table); err != nil {
		t.Fatalf("second register: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ruleset_tables_read_model WHERE ruleset_id = $1 AND table_name = $2 AND row_key = $3`,
		rulesetID, "fake", "a",
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d rows after two registrations, want 1", count)
	}
}

func TestRegisterTables_SameTableNameDifferentRulesets_NoCollision(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()
	rulesetA := uuid.New()
	rulesetB := uuid.New()

	table := &fakeTable{name: "fake", rows: map[string]fakeRow{"a": {Name: "Alpha"}}}

	if err := tables.RegisterTables(ctx, pool, rulesetA, table); err != nil {
		t.Fatalf("register for ruleset A: %v", err)
	}
	if err := tables.RegisterTables(ctx, pool, rulesetB, table); err != nil {
		t.Fatalf("register for ruleset B: %v", err)
	}

	var count int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM ruleset_tables_read_model WHERE table_name = $1`, "fake",
	).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("got %d rows across both rulesets, want 2 (table names are scoped per ruleset, not global)", count)
	}
}
```

- [ ] **Step 8: Add `traits_test.go`**

Create `internal/engine/timadorus/tables/traits_test.go`:

```go
package tables_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/engine/timadorus/tables"
)

func TestLoadTraits_ParsesEveryRow(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}

	strong, ok := traits.Row("strong")
	if !ok {
		t.Fatal("row \"strong\" not found")
	}
	if strong.Key != "strong" {
		t.Fatalf("got Key %q, want %q", strong.Key, "strong")
	}
	if strong.DisplayName != "Strong" {
		t.Fatalf("got DisplayName %q, want %q", strong.DisplayName, "Strong")
	}
	if strong.Description == "" {
		t.Fatal("got empty Description")
	}

	for _, key := range []string{"quick", "agile"} {
		if _, ok := traits.Row(key); !ok {
			t.Fatalf("row %q not found", key)
		}
	}

	if _, ok := traits.Row("nonexistent"); ok {
		t.Fatal("got a row for a key that doesn't exist in traits.yaml")
	}
}

func TestTraitsTable_TableName(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}
	if traits.TableName() != "traits" {
		t.Fatalf("got TableName() %q, want %q", traits.TableName(), "traits")
	}
}

func TestTraitsTable_RowData_ExcludesKeyAndHooks(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}

	data := traits.RowData()
	strong, ok := data["strong"].(tables.TraitsRow)
	if !ok {
		t.Fatalf("RowData()[\"strong\"] is not a tables.TraitsRow: %T", data["strong"])
	}
	if strong.DisplayName != "Strong" {
		t.Fatalf("got DisplayName %q, want %q", strong.DisplayName, "Strong")
	}
}

func TestTraitsTable_RegisterHook_UnknownRow_ReturnsError(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}
	noop := tables.Hook(func(context.Context, pgx.Tx, bus.Envelope) error { return nil })
	if err := traits.RegisterHook("nonexistent", noop); err == nil {
		t.Fatal("got nil error registering a hook on a nonexistent row, want an error")
	}
}

func TestTraitsTable_RegisterHook_MultipleHooksRunInOrder(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}

	var order []int
	hook1 := tables.Hook(func(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
		order = append(order, 1)
		return nil
	})
	hook2 := tables.Hook(func(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
		order = append(order, 2)
		return nil
	})
	if err := traits.RegisterHook("strong", hook1); err != nil {
		t.Fatalf("register hook1: %v", err)
	}
	if err := traits.RegisterHook("strong", hook2); err != nil {
		t.Fatalf("register hook2: %v", err)
	}

	// tx is nil here on purpose: pgx.Tx is an interface, so nil is a valid zero value, and
	// neither hook above touches it — Dispatch itself never dereferences tx either, it only
	// threads it through to each hook.
	if err := traits.Dispatch(context.Background(), nil, "strong", bus.Envelope{}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(order) != 2 || order[0] != 1 || order[1] != 2 {
		t.Fatalf("got hook run order %v, want [1 2]", order)
	}
}

func TestTraitsTable_Dispatch_StopsAtFirstError(t *testing.T) {
	traits, err := tables.LoadTraits()
	if err != nil {
		t.Fatalf("load traits: %v", err)
	}

	wantErr := errors.New("boom")
	ran := false
	failingHook := tables.Hook(func(context.Context, pgx.Tx, bus.Envelope) error { return wantErr })
	secondHook := tables.Hook(func(context.Context, pgx.Tx, bus.Envelope) error { ran = true; return nil })
	if err := traits.RegisterHook("agile", failingHook); err != nil {
		t.Fatalf("register hook1: %v", err)
	}
	if err := traits.RegisterHook("agile", secondHook); err != nil {
		t.Fatalf("register hook2: %v", err)
	}

	err = traits.Dispatch(context.Background(), nil, "agile", bus.Envelope{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("got %v, want %v", err, wantErr)
	}
	if ran {
		t.Fatal("second hook ran despite the first one erroring — Dispatch should stop at the first error")
	}
}
```

- [ ] **Step 9: Run this package's tests**

Run: `go test ./internal/engine/timadorus/tables/... -v`
Expected: all tests pass.

- [ ] **Step 10: Repo-wide verification**

Run: `go build ./... && go vet ./...`
Expected: clean except for `cmd/timadorus-engine/main.go`'s already-known, expected-until-Task-6
build failure from Task 1 (confirm no NEW failing package beyond that one).

- [ ] **Step 11: Commit**

```bash
git add go.mod go.sum internal/engine/timadorus/tables/
git commit -m "timadorus-engine: load ruleset data tables from YAML with per-row hook registration

Adds internal/engine/timadorus/tables: Hook (mirrors projection.Projector.Handle's signature),
Syncable + RegisterTables (idempotent read-model upsert, shared across table files via a small
interface, not a generic container), and traits.go (TraitsRow/TraitsTable/LoadTraits) as the first
concrete table — Key first, Hooks last, YAML columns unpacked directly onto the struct's own
fields, per the design's explicit per-file-concrete-struct decision."
```

---

### Task 4: `internal/query/rulesettables` repository

**Files:**
- Create: `internal/query/rulesettables/repository.go`
- Create: `internal/query/rulesettables/repository_test.go`

**Interfaces:**
- Consumes: Task 2's `ruleset_tables_read_model` schema.
- Produces: `rulesettables.Repository`, `rulesettables.Row{Key, Data}`, `rulesettables.ErrNotFound`
  — Task 5 depends on all three.

- [ ] **Step 1: Add the repository**

Create `internal/query/rulesettables/repository.go`:

```go
// Package rulesettables reads the ruleset_tables_read_model table written by
// internal/engine/timadorus/tables at cmd/timadorus-engine startup.
package rulesettables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("rulesettables: not found")

type Row struct {
	Key  string
	Data json.RawMessage
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

// List returns every row of rulesetID's tableName table, ordered by row key.
func (r *Repository) List(ctx context.Context, rulesetID uuid.UUID, tableName string) ([]Row, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT row_key, data FROM ruleset_tables_read_model
		 WHERE ruleset_id = $1 AND table_name = $2
		 ORDER BY row_key`,
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

// Get returns one row's data, or ErrNotFound.
func (r *Repository) Get(ctx context.Context, rulesetID uuid.UUID, tableName, rowKey string) (json.RawMessage, error) {
	var data json.RawMessage
	err := r.pool.QueryRow(ctx,
		`SELECT data FROM ruleset_tables_read_model
		 WHERE ruleset_id = $1 AND table_name = $2 AND row_key = $3`,
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

- [ ] **Step 2: Add tests**

Create `internal/query/rulesettables/repository_test.go`:

```go
package rulesettables_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/query/rulesettables"
)

func newTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, "postgres:16-alpine",
		tcpostgres.WithDatabase("timadorus_test"),
		tcpostgres.WithUsername("timadorus"),
		tcpostgres.WithPassword("timadorus"),
		tcpostgres.WithOrderedInitScripts(
			"../../projection/rulesettables/migrations/0001_ruleset_tables_read_model.up.sql",
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

func seedRow(t *testing.T, pool *pgxpool.Pool, rulesetID uuid.UUID, tableName, rowKey, data string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO ruleset_tables_read_model (ruleset_id, table_name, row_key, data, updated_at)
		 VALUES ($1, $2, $3, $4, now())`,
		rulesetID, tableName, rowKey, data,
	); err != nil {
		t.Fatalf("seed row: %v", err)
	}
}

func TestRepository_List_ReturnsOrderedRows(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)
	rulesetID := uuid.New()

	seedRow(t, pool, rulesetID, "traits", "strong", `{"displayName":"Strong"}`)
	seedRow(t, pool, rulesetID, "traits", "agile", `{"displayName":"Agile"}`)

	rows, err := repo.List(context.Background(), rulesetID, "traits")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0].Key != "agile" || rows[1].Key != "strong" {
		t.Fatalf("got keys [%s %s], want [agile strong] (ordered by row_key)", rows[0].Key, rows[1].Key)
	}
}

func TestRepository_List_ScopedToRulesetAndTableName(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)
	rulesetA := uuid.New()
	rulesetB := uuid.New()

	seedRow(t, pool, rulesetA, "traits", "strong", `{"displayName":"Strong"}`)
	seedRow(t, pool, rulesetB, "traits", "strong", `{"displayName":"Different Strong"}`)
	seedRow(t, pool, rulesetA, "skills", "strong", `{"displayName":"A Skill Named Strong"}`)

	rows, err := repo.List(context.Background(), rulesetA, "traits")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1 (must not include ruleset B's or the \"skills\" table's row)", len(rows))
	}
}

func TestRepository_Get_Found(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)
	rulesetID := uuid.New()
	seedRow(t, pool, rulesetID, "traits", "strong", `{"displayName":"Strong"}`)

	data, err := repo.Get(context.Background(), rulesetID, "traits", "strong")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(data) != `{"displayName":"Strong"}` {
		t.Fatalf("got %s, want the seeded payload", data)
	}
}

func TestRepository_Get_NotFound(t *testing.T) {
	pool := newTestPool(t)
	repo := rulesettables.NewRepository(pool)

	_, err := repo.Get(context.Background(), uuid.New(), "traits", "nonexistent")
	if !errors.Is(err, rulesettables.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 3: Run this package's tests**

Run: `go test ./internal/query/rulesettables/... -v`
Expected: all tests pass.

- [ ] **Step 4: Commit**

```bash
git add internal/query/rulesettables/
git commit -m "query: add internal/query/rulesettables, reading ruleset_tables_read_model"
```

---

### Task 5: OpenAPI endpoints, codegen, and `internal/httpapi/query` wiring

**Files:**
- Modify: `api/query/openapi.yaml`
- Regenerate: `api/query/gen/server.gen.go`
- Modify: `internal/httpapi/query/server.go`
- Modify: `cmd/query-api/main.go`

**Interfaces:**
- Consumes: Task 4's `rulesettables.Repository`/`Row`/`ErrNotFound`.
- Produces: `GET /rulesets/{rulesetId}/tables/{tableName}`, `GET
  /rulesets/{rulesetId}/tables/{tableName}/{rowKey}` — no later task depends on this; it's the
  externally-visible surface this whole plan builds toward.

- [ ] **Step 1: Add the OpenAPI schema and paths**

In `api/query/openapi.yaml`, add this schema to the `schemas:` section, immediately after the
existing `Ruleset:` schema:

```yaml
    RulesetTableRow:
      type: object
      required: [key, data]
      properties:
        key:
          type: string
        # data is an opaque, table-specific payload — its shape varies per table (see
        # internal/engine/timadorus/tables for what each table's own Go struct actually contains;
        # there is no single fixed schema across tables, by design — traits.yaml's rows look
        # nothing like a hypothetical future skills table's rows).
        data:
          type: object
```

Add these two paths, immediately after the existing `/rulesets/{rulesetId}:` path block:

```yaml
  /rulesets/{rulesetId}/tables/{tableName}:
    get:
      operationId: listRulesetTableRows
      summary: List every row of a Ruleset's named data table.
      parameters:
        - $ref: "#/components/parameters/RulesetId"
        - name: tableName
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: Table rows.
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/RulesetTableRow"

  /rulesets/{rulesetId}/tables/{tableName}/{rowKey}:
    get:
      operationId: getRulesetTableRow
      summary: Get one row's data from a Ruleset's named data table.
      parameters:
        - $ref: "#/components/parameters/RulesetId"
        - name: tableName
          in: path
          required: true
          schema:
            type: string
        - name: rowKey
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: The row's opaque, table-specific data.
          content:
            application/json:
              schema:
                type: object
        "404":
          $ref: "#/components/responses/NotFound"
```

- [ ] **Step 2: Regenerate the query API's generated server code**

Run: `go generate ./api/query/...`
Expected: `api/query/gen/server.gen.go` changes (new types/interfaces for
`ListRulesetTableRows`/`GetRulesetTableRow`). Run `git diff --stat api/query/gen/server.gen.go` to
confirm it changed; do not hand-edit this file.

- [ ] **Step 3: Wire the new repository into `Server`**

In `internal/httpapi/query/server.go`, add this import:

```go
rulesettablesquery "github.com/timadorus/platform/internal/query/rulesettables"
```

Add a field to the `Server` struct and a parameter to `NewServer`:

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
	}
}
```

Add these two handlers, immediately after the existing `ListRulesets` handler:

```go
func (s *Server) ListRulesetTableRows(ctx context.Context, request gen.ListRulesetTableRowsRequestObject) (gen.ListRulesetTableRowsResponseObject, error) {
	rows, err := s.rulesetTables.List(ctx, request.RulesetId, request.TableName)
	if err != nil {
		return nil, err
	}
	out := make([]gen.RulesetTableRow, len(rows))
	for i, row := range rows {
		var data map[string]interface{}
		if err := json.Unmarshal(row.Data, &data); err != nil {
			return nil, err
		}
		out[i] = gen.RulesetTableRow{Key: row.Key, Data: data}
	}
	return gen.ListRulesetTableRows200JSONResponse(out), nil
}

func (s *Server) GetRulesetTableRow(ctx context.Context, request gen.GetRulesetTableRowRequestObject) (gen.GetRulesetTableRowResponseObject, error) {
	data, err := s.rulesetTables.Get(ctx, request.RulesetId, request.TableName, request.RowKey)
	if err != nil {
		if errors.Is(err, rulesettablesquery.ErrNotFound) {
			return gen.GetRulesetTableRow404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return gen.GetRulesetTableRow200JSONResponse(out), nil
}
```

Add `"encoding/json"` to the import block if it isn't already imported in this file (check first
— it may already be imported for another handler).

If the generated response type names in Step 2's output don't match exactly
(`ListRulesetTableRowsRequestObject`/`ResponseObject`, `GetRulesetTableRowRequestObject`/
`ResponseObject`, `ListRulesetTableRows200JSONResponse`, `GetRulesetTableRow200JSONResponse`,
`GetRulesetTableRow404ApplicationProblemPlusJSONResponse`, `gen.RulesetTableRow{Key, Data}`), read
the actual generated names from `api/query/gen/server.gen.go` and use those instead — oapi-codegen
capitalizes `operationId` mechanically (`listRulesetTableRows` → `ListRulesetTableRows`), matching
every existing handler's own naming (`getRuleset` → `GetRuleset`), so this should already be
exactly right, but confirm against the actual generated file rather than assuming.

- [ ] **Step 4: Wire the new repository into `cmd/query-api/main.go`**

In `cmd/query-api/main.go`, add this import:

```go
rulesettablesquery "github.com/timadorus/platform/internal/query/rulesettables"
```

Add, alongside the existing repository constructions:

```go
	rulesetTablesRepo := rulesettablesquery.NewRepository(pool)
```

Update the `httpquery.NewServer(...)` call to pass it as the new final argument:

```go
	server := httpquery.NewServer(universeRepo, userRepo, campaignRepo, entityRepo, characterRepo, objectRepo, rulesetRepo, rulesetTablesRepo)
```

- [ ] **Step 5: Build and typecheck**

Run: `go build ./... && go vet ./...`
Expected: clean except for `cmd/timadorus-engine/main.go`'s already-known, expected-until-Task-6
`RegisterRuleset` build failure from Task 1 — confirm no other package fails, in particular
`cmd/query-api` and `internal/httpapi/query` must build cleanly now.

- [ ] **Step 6: Commit**

```bash
git add api/query/openapi.yaml api/query/gen/server.gen.go internal/httpapi/query/server.go cmd/query-api/main.go
git commit -m "query-api: expose ruleset data tables via GET /rulesets/{id}/tables/{name}[/{rowKey}]"
```

---

### Task 6: `cmd/timadorus-engine` startup wiring and final integration

**Files:**
- Modify: `cmd/timadorus-engine/main.go`

**Interfaces:**
- Consumes: Task 1's `RegisterRuleset(ctx, pool) (uuid.UUID, error)`, Task 3's
  `tables.LoadTraits()`/`tables.RegisterTables(...)`.

- [ ] **Step 1: Update the startup sequence**

In `cmd/timadorus-engine/main.go`, add this import:

```go
"github.com/timadorus/platform/internal/engine/timadorus/tables"
```

Replace:

```go
	// Registering the Timadorus Ruleset here, before anything else starts, means a registration
	// failure (anything other than "it already exists") terminates this process via run()'s
	// existing error return -> main()'s os.Exit(1), before /readyz ever starts listening and
	// before this binary consumes a single event.
	if err := timadorusengine.RegisterRuleset(ctx, pool); err != nil {
		return err
	}
```

with:

```go
	// Registering the Timadorus Ruleset here, before anything else starts, means a registration
	// failure (anything other than "it already exists") terminates this process via run()'s
	// existing error return -> main()'s os.Exit(1), before /readyz ever starts listening and
	// before this binary consumes a single event. The returned id scopes the data-table sync
	// immediately below — both run once, synchronously, before the router starts.
	rulesetID, err := timadorusengine.RegisterRuleset(ctx, pool)
	if err != nil {
		return err
	}

	traits, err := tables.LoadTraits()
	if err != nil {
		return err
	}
	if err := tables.RegisterTables(ctx, pool, rulesetID, traits); err != nil {
		return err
	}
```

- [ ] **Step 2: Update the package doc comment**

At the top of `cmd/timadorus-engine/main.go`, update:

```go
// timadorus-engine subscribes to the Character and Campaign event streams and reacts to their
// respective trigger events (ActionRequested, ConfigurationRequested, CampaignCreated) — see
// internal/engine/timadorus for the actual logic. Structurally identical to cmd/projector (same
// Router/checkpoint machinery), but registers two processors sharing one RulesetCache instead
// of the seven read-model projectors, which is why it's a separate binary: unlike every
// projector, it legitimately imports full write-side packages (domain/character,
// domain/campaign, eventsourcing, eventstore/postgres).
```

to:

```go
// timadorus-engine subscribes to the Character and Campaign event streams and reacts to their
// respective trigger events (ActionRequested, ConfigurationRequested, CampaignCreated) — see
// internal/engine/timadorus for the actual logic. Structurally identical to cmd/projector (same
// Router/checkpoint machinery), but registers two processors sharing one RulesetCache instead
// of the seven read-model projectors, which is why it's a separate binary: unlike every
// projector, it legitimately imports full write-side packages (domain/character,
// domain/campaign, eventsourcing, eventstore/postgres). Also syncs the Timadorus Ruleset's data
// tables (traits.yaml today, more to follow) into ruleset_tables_read_model at startup — see
// internal/engine/timadorus/tables.
```

- [ ] **Step 3: Build, vet, test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: entirely clean — this is the step where the `cmd/timadorus-engine` build failure
deliberately left open since Task 1 finally resolves. Confirm zero failing packages anywhere in
the repo.

- [ ] **Step 4: Commit**

```bash
git add cmd/timadorus-engine/main.go
git commit -m "timadorus-engine: sync ruleset data tables into the read model at startup"
```

---

## Final Verification

- `go build ./... && go vet ./... && go test ./...` clean across the whole repo.
- `go generate ./...` regenerates `api/query/gen` with no further diff (proves the committed
  generated code in Task 5 is actually in sync with the final `openapi.yaml`).
- Read the final `internal/engine/timadorus/tables/traits.go` and confirm `TraitsRow`'s field
  order is literally `Key` first, `Hooks` last, with the YAML-tagged columns in between.
- Grep the whole repo for `RegisterRuleset(ctx, pool)` (the old call shape, ignoring the new
  return value) to confirm no stale caller remains beyond what Task 6 fixed.
- Confirm `go.mod`'s `gopkg.in/yaml.v3` line no longer has the `// indirect` comment.
