# timadorus-engine Self-Registers the "Timadorus" Ruleset Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `timadorus-engine` registers the "Timadorus" Ruleset itself at startup instead of relying
on `make dev-up` to seed it — which requires adding real Ruleset name-uniqueness enforcement first,
since none exists today.

**Architecture:** A new Postgres-backed name-reservation table makes `ruleset.Service.Create`
atomic and duplicate-safe, surfaced as a new domain error mapped to HTTP 409. The engine calls
`Create` once at startup and treats "already exists" as success, anything else as fatal. The
devcluster's own seeding of this Ruleset is then deleted, since the engine's own readiness probe
already gates on registration succeeding before `make dev-up`'s seeding step ever runs.

**Tech Stack:** Go, Postgres (via `pgx`/`pgxpool`), `golang-migrate`, `oapi-codegen`,
testcontainers-go.

See `docs/superpowers/specs/2026-08-27-ruleset-self-registration-design.md` for the full design
rationale (why no uniqueness constraint exists today, why case-sensitive, why names are never
released, why the devcluster seeding removal is safe).

## Global Constraints

- Ruleset name uniqueness is **case-sensitive, exact-string match**. Do not add case-insensitive
  comparison or normalization anywhere in this plan.
- A Ruleset's reserved name is **never released**, including on `Archive` — do not add any cleanup
  of `ruleset_names` rows.
- `RegisterRuleset` (Task 3) must be called in `cmd/timadorus-engine/main.go`'s `run()` **before**
  the observability HTTP server starts and **before** `router.Run` — a registration failure must
  propagate out of `run()` so `main()`'s existing `os.Exit(1)` fires; it must never be logged and
  swallowed.
- `errors.Is(err, ruleset.ErrNameAlreadyExists)` is the *only* outcome `RegisterRuleset` treats as
  non-fatal besides a clean create. Every other error (validation, real DB failure) propagates
  unchanged.
- The new migration directory `internal/command/ruleset/migrations/` is a new schema owner
  (`command_ruleset`) and must be added to `scripts/migrate-up.sh`'s `schema_owners` array in the
  same `"name:path"` format every existing entry uses.
- No change to `internal/domain/character`, `internal/domain/campaign`, or the existing logic in
  `internal/engine/timadorus/cache.go`, `character_processor.go`, `campaign_processor.go` — this
  plan only adds new files/functions to that package plus one new call in
  `cmd/timadorus-engine/main.go`'s `run()`.
- `go build ./... && go vet ./...` and `go test ./...` must stay clean after every task.
- `go generate ./...` (Task 2) must produce a diff touching only the new `POST /rulesets` 409
  response types — no other generated-code changes.
- File layout after this plan:
  - `internal/domain/ruleset/errors.go` (modified) — adds `ErrNameAlreadyExists`.
  - `internal/eventstore/postgres/tx.go` (modified) — `txFromContext` renamed to exported
    `TxFromContext`.
  - `internal/eventstore/postgres/unit_of_work.go` (modified) — doc comment only, describing the
    new single-aggregate-plus-raw-SQL use case alongside the existing two-aggregate one.
  - `internal/eventstore/postgres/store.go` (modified) — adds exported `IsUniqueViolation`, uses
    it and `TxFromContext` internally.
  - `internal/command/ruleset/migrations/0001_ruleset_names.up.sql`,
    `.../0001_ruleset_names.down.sql` (new).
  - `internal/command/ruleset/service.go` (modified) — `NewService` takes a `*pgxpool.Pool`;
    `Create` reserves the name and saves the aggregate atomically.
  - `internal/command/ruleset/service_test.go` (new).
  - `cmd/command-api/main.go` (modified) — one call-site update.
  - `internal/httpapi/command/errors.go`, `server.go` (modified) — new 409 case/branch.
  - `api/command/openapi.yaml`, `api/command/gen/server.gen.go` (modified/regenerated).
  - `test/e2e/e2e_test.go` (modified) — one new assertion.
  - `internal/engine/timadorus/register.go`, `register_test.go` (new).
  - `internal/engine/timadorus/testutil_test.go` (modified) — one new migration path.
  - `cmd/timadorus-engine/main.go` (modified) — one new call.
  - `test/e2e/internal/seed.go`, `test/e2e/cmd/devcluster/up.go` (modified) — remove Ruleset
    seeding, adjust status message and doc comments.

---

### Task 1: Ruleset name-uniqueness enforcement

**Files:**
- Modify: `internal/domain/ruleset/errors.go`
- Modify: `internal/eventstore/postgres/tx.go`
- Modify: `internal/eventstore/postgres/unit_of_work.go`
- Modify: `internal/eventstore/postgres/store.go`
- Create: `internal/command/ruleset/migrations/0001_ruleset_names.up.sql`
- Create: `internal/command/ruleset/migrations/0001_ruleset_names.down.sql`
- Modify: `scripts/migrate-up.sh`
- Modify: `internal/command/ruleset/service.go`
- Modify: `cmd/command-api/main.go`
- Create: `internal/command/ruleset/service_test.go`

**Interfaces:**
- Produces: `ruleset.ErrNameAlreadyExists` (sentinel error), `postgres.TxFromContext(ctx)
  (pgx.Tx, bool)` (exported, renamed from `txFromContext`), `postgres.IsUniqueViolation(err
  error) bool` (new export), `rulesetcmd.NewService(repo *eventsourcing.Repository[*ruleset.Ruleset],
  pool *pgxpool.Pool) *Service` (new second parameter).
- Consumes (Task 2, 3): the two new exported errors/functions above, and the new `NewService`
  signature.

- [ ] **Step 1: Add the new sentinel error**

Edit `internal/domain/ruleset/errors.go` to:

```go
package ruleset

import "errors"

var (
	ErrNameRequired      = errors.New("ruleset: name is required")
	ErrArchived          = errors.New("ruleset: ruleset is archived")
	ErrNameAlreadyExists = errors.New("ruleset: name already exists")
)
```

- [ ] **Step 2: Export the ambient-transaction lookup**

Edit `internal/eventstore/postgres/tx.go` to:

```go
package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
)

type txKey struct{}

// WithTx stashes tx in ctx so that Store.Append, when called with the returned context,
// joins the caller's transaction instead of opening its own. Used by UnitOfWork to make a
// single Postgres transaction span multiple aggregates' Append calls (see unit_of_work.go).
func WithTx(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txKey{}, tx)
}

// TxFromContext returns the ambient transaction stashed by WithTx, if any. Exported so a
// command service that already opened a UnitOfWork (e.g. internal/command/ruleset, whose
// Create needs to run its own raw SQL — the name reservation insert — inside that same
// transaction, alongside a Repository.Save call made with the same context) can retrieve it.
func TxFromContext(ctx context.Context) (pgx.Tx, bool) {
	tx, ok := ctx.Value(txKey{}).(pgx.Tx)
	return tx, ok
}
```

(This is a rename of the previously-unexported `txFromContext` to `TxFromContext` — same body,
new name, new doc comment.)

- [ ] **Step 3: Update UnitOfWork's doc comment for its new second use case**

`internal/eventstore/postgres/unit_of_work.go`'s `UnitOfWork` doc comment currently only
describes spanning *two aggregates'* `Repository.Save` calls (Character + Entity). Task 1's
`ruleset.Service.Create` (Step 8 below) uses it differently — one `Repository.Save` plus one raw
SQL statement, both via the same transaction — so the comment needs a second example. Change:

```go
// UnitOfWork lets an application-layer command service make more than one aggregate's
// Repository.Save calls commit atomically (e.g. creating a Character and its auto-created
// Entity together, internal/command/character/service.go). It is deliberately postgres-
// specific and opt-in: single-aggregate command services never construct one, and
// Store.Append behaves identically whether or not an ambient transaction is present.
type UnitOfWork struct {
```
to:
```go
// UnitOfWork lets an application-layer command service commit more than one write atomically
// through the same transaction — either more than one aggregate's Repository.Save calls (e.g.
// creating a Character and its auto-created Entity together,
// internal/command/character/service.go), or a single Repository.Save alongside the command
// service's own raw SQL via TxFromContext (e.g. internal/command/ruleset/service.go's Create,
// which reserves a name in a side table before saving the new aggregate). It is deliberately
// postgres-specific and opt-in: single-aggregate command services with no cross-cutting
// invariant to enforce never construct one, and Store.Append behaves identically whether or not
// an ambient transaction is present.
type UnitOfWork struct {
```

- [ ] **Step 4: Update store.go's one call site and add IsUniqueViolation**

In `internal/eventstore/postgres/store.go`, change the call site inside `Append`:

```go
	if tx, ok := txFromContext(ctx); ok {
```
to:
```go
	if tx, ok := TxFromContext(ctx); ok {
```

Then, in `appendWith`, replace:

```go
		if err != nil {
			var pgErr *pgconn.PgError
			if errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
				return eventsourcing.ErrConcurrencyConflict
			}
			return fmt.Errorf("postgres: insert event: %w", err)
		}
```
with:
```go
		if err != nil {
			if IsUniqueViolation(err) {
				return eventsourcing.ErrConcurrencyConflict
			}
			return fmt.Errorf("postgres: insert event: %w", err)
		}
```

Then add this new exported function anywhere in the file below `const uniqueViolation = "23505"`
(e.g. directly after the `NewStore` constructor):

```go
// IsUniqueViolation reports whether err is a Postgres unique-constraint violation (SQLSTATE
// 23505) — exported so a command service running its own raw SQL inside a postgres.UnitOfWork
// (e.g. internal/command/ruleset's name-reservation insert) can distinguish "this name is
// already taken" from any other failure, the same way this file already does for optimistic-
// concurrency conflicts above.
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
```

The `errors` and `pgconn` imports are already present in this file (both were already used by the
code you just replaced) — no import changes needed.

- [ ] **Step 5: Run existing store tests to confirm the rename/refactor is behavior-preserving**

Run: `go test ./internal/eventstore/postgres/... -v`
Expected: all existing tests PASS unchanged (this step is a pure rename plus an extracted-but-
identical check — no behavior changed yet).

- [ ] **Step 6: Add the ruleset_names migration**

Create `internal/command/ruleset/migrations/0001_ruleset_names.up.sql`:

```sql
CREATE TABLE ruleset_names (
    name TEXT PRIMARY KEY
);
```

Create `internal/command/ruleset/migrations/0001_ruleset_names.down.sql`:

```sql
DROP TABLE ruleset_names;
```

- [ ] **Step 7: Register the new schema owner**

Edit `scripts/migrate-up.sh`'s `schema_owners` array — add one new line (keeping the existing
entries and their order unchanged):

```bash
schema_owners=(
  "eventstore:internal/eventstore/postgres/migrations"
  "projection_checkpoint:internal/projection/checkpoint/migrations"
  "projection_universe:internal/projection/universe/migrations"
  "projection_user:internal/projection/user/migrations"
  "projection_campaign:internal/projection/campaign/migrations"
  "projection_entity:internal/projection/entity/migrations"
  "projection_character:internal/projection/character/migrations"
  "projection_object:internal/projection/object/migrations"
  "projection_ruleset:internal/projection/ruleset/migrations"
  "command_ruleset:internal/command/ruleset/migrations"
)
```

- [ ] **Step 8: Make Create atomic and duplicate-safe**

Replace the full content of `internal/command/ruleset/service.go` with:

```go
// Package ruleset is the application-layer command service for Ruleset. It has no other
// aggregate type to validate against — Ruleset has no parent (plan §2) — but unlike
// internal/command/user, Create enforces a real name-uniqueness invariant via a Postgres-backed
// reservation table (see Create's own doc comment and migrations/0001_ruleset_names.up.sql),
// which is why this service also holds a *pgxpool.Pool — the same way internal/command/character
// does, for its own different reason of spanning two aggregates' Save calls atomically.
package ruleset

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/timadorus/platform/internal/domain/ruleset"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
)

type Service struct {
	repo *eventsourcing.Repository[*ruleset.Ruleset]
	pool *pgxpool.Pool
}

func NewService(repo *eventsourcing.Repository[*ruleset.Ruleset], pool *pgxpool.Pool) *Service {
	return &Service{repo: repo, pool: pool}
}

// Create enforces Ruleset name uniqueness: within one transaction, it reserves name in
// ruleset_names (migrations/0001_ruleset_names.up.sql) and saves the new aggregate — both commit
// or both roll back together. A unique-constraint violation on the reservation means the name is
// already taken and becomes ruleset.ErrNameAlreadyExists; no aggregate is created and no event is
// appended. Names are never released, even if the Ruleset is later archived.
func (s *Service) Create(ctx context.Context, name, description string, references []string) (uuid.UUID, error) {
	r, err := ruleset.New(name, description, references)
	if err != nil {
		return uuid.Nil, err
	}

	uow, txCtx, err := postgres.NewUnitOfWork(ctx, s.pool)
	if err != nil {
		return uuid.Nil, err
	}
	tx, _ := postgres.TxFromContext(txCtx) // always ok: txCtx was just built by NewUnitOfWork

	if _, err := tx.Exec(ctx, `INSERT INTO ruleset_names (name) VALUES ($1)`, name); err != nil {
		_ = uow.Rollback(ctx)
		if postgres.IsUniqueViolation(err) {
			return uuid.Nil, ruleset.ErrNameAlreadyExists
		}
		return uuid.Nil, fmt.Errorf("ruleset: reserve name %q: %w", name, err)
	}

	if err := s.repo.Save(txCtx, r); err != nil {
		_ = uow.Rollback(ctx)
		return uuid.Nil, err
	}
	if err := uow.Commit(ctx); err != nil {
		return uuid.Nil, err
	}
	return r.AggregateID(), nil
}

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

func (s *Service) SetDescription(ctx context.Context, id uuid.UUID, description string) error {
	r, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := r.SetDescription(description); err != nil {
		return err
	}
	return s.repo.Save(ctx, r)
}

func (s *Service) SetReferences(ctx context.Context, id uuid.UUID, references []string) error {
	r, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := r.SetReferences(references); err != nil {
		return err
	}
	return s.repo.Save(ctx, r)
}

func (s *Service) Archive(ctx context.Context, id uuid.UUID) error {
	r, err := s.repo.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := r.Archive(); err != nil {
		return err
	}
	return s.repo.Save(ctx, r)
}
```

(Only `Create` and the package doc comment change; `Rename`/`SetDescription`/`SetReferences`/
`Archive` are reproduced above unchanged, for a clean full-file replacement.)

- [ ] **Step 9: Update the one call site**

In `cmd/command-api/main.go`, change:

```go
	rulesetService := rulesetcmd.NewService(rulesetRepo)
```
to:
```go
	rulesetService := rulesetcmd.NewService(rulesetRepo, pool)
```

- [ ] **Step 10: Confirm the whole repo still builds**

Run: `go build ./... && go vet ./...`
Expected: clean (this will fail until Step 9 is done, since Step 8 changes `NewService`'s
signature).

- [ ] **Step 11: Write the new service-level tests**

Create `internal/command/ruleset/service_test.go`:

```go
package ruleset_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	rulesetcmd "github.com/timadorus/platform/internal/command/ruleset"
	"github.com/timadorus/platform/internal/domain/ruleset"
	rulesetevents "github.com/timadorus/platform/internal/domain/ruleset/events"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
)

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
			"migrations/0001_ruleset_names.up.sql",
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

func newService(t *testing.T, pool *pgxpool.Pool) *rulesetcmd.Service {
	t.Helper()
	registry := eventsourcing.NewRegistry()
	rulesetevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, ruleset.AggregateType, func() *ruleset.Ruleset {
		return &ruleset.Ruleset{}
	})
	return rulesetcmd.NewService(repo, pool)
}

func TestService_Create_Succeeds(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	id, err := service.Create(ctx, "D&D 5e", "a description", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id == uuid.Nil {
		t.Fatal("got nil id")
	}
}

func TestService_Create_DuplicateName_ReturnsErrNameAlreadyExists(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	if _, err := service.Create(ctx, "Pathfinder", "", nil); err != nil {
		t.Fatalf("first create: %v", err)
	}

	_, err := service.Create(ctx, "Pathfinder", "a different description", nil)
	if !errors.Is(err, ruleset.ErrNameAlreadyExists) {
		t.Fatalf("got %v, want ErrNameAlreadyExists", err)
	}

	var eventCount int
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM events WHERE aggregate_type = $1 AND event_type = $2`,
		ruleset.AggregateType, rulesetevents.TypeRulesetCreated,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("got %d RulesetCreated events, want 1 (duplicate must not create a second aggregate)", eventCount)
	}

	var nameCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ruleset_names WHERE name = $1`, "Pathfinder").Scan(&nameCount); err != nil {
		t.Fatalf("count ruleset_names: %v", err)
	}
	if nameCount != 1 {
		t.Fatalf("got %d ruleset_names rows for %q, want 1", nameCount, "Pathfinder")
	}
}

func TestService_Create_EmptyName_FailsBeforeTouchingDB(t *testing.T) {
	pool := newTestPool(t)
	service := newService(t, pool)
	ctx := context.Background()

	_, err := service.Create(ctx, "", "", nil)
	if !errors.Is(err, ruleset.ErrNameRequired) {
		t.Fatalf("got %v, want ErrNameRequired", err)
	}

	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM ruleset_names`).Scan(&count); err != nil {
		t.Fatalf("count ruleset_names: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d ruleset_names rows, want 0 (validation must run before any DB access)", count)
	}
}
```

- [ ] **Step 12: Run the new tests**

Run: `go test ./internal/command/ruleset/... -v`
Expected: all three tests PASS.

- [ ] **Step 13: Full build/vet/test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: clean.

- [ ] **Step 14: Commit**

```bash
git add internal/domain/ruleset/errors.go internal/eventstore/postgres/tx.go \
  internal/eventstore/postgres/unit_of_work.go internal/eventstore/postgres/store.go \
  internal/command/ruleset/migrations scripts/migrate-up.sh \
  internal/command/ruleset/service.go internal/command/ruleset/service_test.go \
  cmd/command-api/main.go
git commit -m "ruleset: enforce name uniqueness via a Postgres-backed reservation table"
```

---

### Task 2: HTTP 409 mapping for duplicate Ruleset names

**Files:**
- Modify: `internal/httpapi/command/errors.go`
- Modify: `internal/httpapi/command/server.go`
- Modify: `api/command/openapi.yaml`
- Regenerate: `api/command/gen/server.gen.go` (via `go generate ./...`)
- Modify: `test/e2e/e2e_test.go`

**Interfaces:**
- Consumes: `ruleset.ErrNameAlreadyExists` (Task 1).
- Produces: nothing new consumed by later tasks — Task 3 talks to the command service directly,
  not via HTTP.

- [ ] **Step 1: Add the openapi response**

In `api/command/openapi.yaml`, find the `/rulesets` `post` operation's `responses:` block:

```yaml
      responses:
        "201":
          description: Ruleset created.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/RulesetCreatedResponse"
        "400":
          $ref: "#/components/responses/BadRequest"
        "422":
          $ref: "#/components/responses/UnprocessableEntity"
```

Change it to:

```yaml
      responses:
        "201":
          description: Ruleset created.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/RulesetCreatedResponse"
        "400":
          $ref: "#/components/responses/BadRequest"
        "409":
          $ref: "#/components/responses/Conflict"
        "422":
          $ref: "#/components/responses/UnprocessableEntity"
```

- [ ] **Step 2: Regenerate the command API client code**

Run: `go generate ./...`
Expected: `git diff --stat -- api/command/gen` shows changes only inside
`api/command/gen/server.gen.go`, adding (among the generated scaffolding) a
`CreateRuleset409ApplicationProblemPlusJSONResponse` type embedding
`ConflictApplicationProblemPlusJSONResponse` — the same shape `RenameRuleset409ApplicationProblemPlusJSONResponse`
already has a few lines away in the same file. `git diff --stat -- api/query/gen` shows no
changes (this plan never touches the query spec).

- [ ] **Step 3: Add the new domain-error mapping**

In `internal/httpapi/command/errors.go`, inside `classify`'s `switch`, change:

```go
	case errors.Is(err, ruleset.ErrArchived):
		return 409, "archived"
	case errors.Is(err, ruleset.ErrNameRequired):
		return 422, "validation_failed"
```
to:
```go
	case errors.Is(err, ruleset.ErrArchived):
		return 409, "archived"
	case errors.Is(err, ruleset.ErrNameRequired):
		return 422, "validation_failed"
	case errors.Is(err, ruleset.ErrNameAlreadyExists):
		return 409, "name_already_exists"
```

- [ ] **Step 4: Add the HTTP response branch**

In `internal/httpapi/command/server.go`, change `CreateRuleset`'s error-handling switch from:

```go
	id, err := s.ruleset.Create(ctx, request.Body.Name, description, derefReferences(request.Body.References))
	if err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 422:
			return gen.CreateRuleset422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.CreateRuleset400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}
```
to:
```go
	id, err := s.ruleset.Create(ctx, request.Body.Name, description, derefReferences(request.Body.References))
	if err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 409:
			return gen.CreateRuleset409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.CreateRuleset422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.CreateRuleset400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}
```

- [ ] **Step 5: Full build/vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 6: Add the e2e duplicate-name assertion**

In `test/e2e/e2e_test.go`, immediately after the existing block that creates `rulesetResp` (the
`resp.StatusCode == http.StatusCreated` assertion right after `CreateRulesetRequest{Name:
rulesetName}`), insert:

```go
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/rulesets", env.BearerToken,
			commandgen.CreateRulesetRequest{Name: rulesetName}, nil)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusConflict))
```

- [ ] **Step 7: Build the e2e test binary (compile-only check — this suite needs a live cluster to run)**

Run: `go build -tags e2e ./test/e2e/...`
Expected: clean (this only proves the new assertion compiles against the real generated types;
actually running `-tags e2e` requires the dev cluster environment, which the final whole-branch
review will check separately if that environment is available).

- [ ] **Step 8: Full build/vet/test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: clean.

- [ ] **Step 9: Commit**

```bash
git add api/command/openapi.yaml api/command/gen internal/httpapi/command/errors.go \
  internal/httpapi/command/server.go test/e2e/e2e_test.go
git commit -m "httpapi: map duplicate Ruleset names to 409 Conflict"
```

---

### Task 3: timadorus-engine self-registration

**Files:**
- Create: `internal/engine/timadorus/register.go`
- Create: `internal/engine/timadorus/register_test.go`
- Modify: `internal/engine/timadorus/testutil_test.go`
- Modify: `cmd/timadorus-engine/main.go`

**Interfaces:**
- Consumes: `rulesetcmd.NewService(repo, pool)`, `ruleset.ErrNameAlreadyExists` (Task 1).
- Produces: `timadorus.TargetRulesetName` (exported const, `"Timadorus"`), `timadorus.RegisterRuleset(ctx
  context.Context, pool *pgxpool.Pool) error` (exported function) — both consumed by
  `cmd/timadorus-engine/main.go` in this same task.

- [ ] **Step 1: Add the new migration to the shared test pool**

In `internal/engine/timadorus/testutil_test.go`, inside `newTestPool`'s
`tcpostgres.WithOrderedInitScripts(...)` call, add one new line at the end of the list (after
`"../../projection/ruleset/migrations/0001_ruleset_read_model.up.sql"`):

```go
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
			"../../command/ruleset/migrations/0001_ruleset_names.up.sql",
		),
```

Also update this function's doc comment (currently: "...every migration this package's tests
need — Character's, Campaign's, and Ruleset's (read by RulesetCache.resolve), plus the shared
checkpoint tables...") to add a clause: "...plus the shared checkpoint tables and the Ruleset
name-reservation table Task 3's register_test.go needs."

- [ ] **Step 2: Write RegisterRuleset**

Create `internal/engine/timadorus/register.go`:

```go
package timadorus

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	rulesetcmd "github.com/timadorus/platform/internal/command/ruleset"
	"github.com/timadorus/platform/internal/domain/ruleset"
	rulesetevents "github.com/timadorus/platform/internal/domain/ruleset/events"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
)

// TargetRulesetName is the exact name this engine registers at startup (see RegisterRuleset),
// and the one targetRulesetName (character_processor.go/campaign_processor.go) matches against
// case-insensitively. Exported since it names a real, meaningful platform constant, not just an
// implementation detail — mirroring CharacterProcessorName/CampaignProcessorName's own export in
// this package.
const TargetRulesetName = "Timadorus"

// RegisterRuleset ensures a Ruleset named TargetRulesetName exists, creating it on this
// platform's first startup. Builds its own scoped registry/store/repo/service, mirroring exactly
// how NewCharacterProcessor/NewCampaignProcessor already build their own scoped repos — this
// function is only ever called once, at cmd/timadorus-engine startup, so there's no shared state
// to inject from outside.
//
// errors.Is(err, ruleset.ErrNameAlreadyExists) is the only outcome besides a clean create treated
// as success (idempotent across restarts). Any other error is returned as-is: the caller
// (cmd/timadorus-engine/main.go's run()) terminates the process on it, since a Campaign
// referencing this Ruleset by name has no other way to discover it if registration silently
// failed.
func RegisterRuleset(ctx context.Context, pool *pgxpool.Pool) error {
	registry := eventsourcing.NewRegistry()
	rulesetevents.Register(registry)
	store := postgres.NewStore(pool, registry)
	repo := eventsourcing.NewRepository(store, ruleset.AggregateType, func() *ruleset.Ruleset {
		return &ruleset.Ruleset{}
	})
	service := rulesetcmd.NewService(repo, pool)

	_, err := service.Create(ctx, TargetRulesetName, "", nil)
	if errors.Is(err, ruleset.ErrNameAlreadyExists) {
		return nil
	}
	if err != nil {
		return fmt.Errorf(errPrefix+"register ruleset %q: %w", TargetRulesetName, err)
	}
	return nil
}
```

(`errPrefix` is the existing `"timadorus-engine: "` constant already defined in `cache.go` in this
same package — no new import or redefinition needed.)

- [ ] **Step 3: Write the tests**

Create `internal/engine/timadorus/register_test.go`:

```go
package timadorus_test

import (
	"context"
	"errors"
	"testing"

	"github.com/timadorus/platform/internal/domain/ruleset"
	"github.com/timadorus/platform/internal/engine/timadorus"
)

func TestRegisterRuleset_CreatesOnFirstCall(t *testing.T) {
	pool := newTestPool(t)
	ctx := context.Background()

	if err := timadorus.RegisterRuleset(ctx, pool); err != nil {
		t.Fatalf("register: %v", err)
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

	if err := timadorus.RegisterRuleset(ctx, pool); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := timadorus.RegisterRuleset(ctx, pool); err != nil {
		t.Fatalf("second register: %v", err)
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

	err := timadorus.RegisterRuleset(ctx, pool)
	if err == nil {
		t.Fatal("got nil error from a closed pool, want a real error")
	}
	if errors.Is(err, ruleset.ErrNameAlreadyExists) {
		t.Fatalf("got ErrNameAlreadyExists from a closed pool, want a genuine failure: %v", err)
	}
}
```

(`pool.Close()` is safe to call again via `testutil_test.go`'s own `t.Cleanup(pool.Close)` —
`*pgxpool.Pool.Close` is idempotent.)

- [ ] **Step 4: Run the new tests**

Run: `go test ./internal/engine/timadorus/... -v`
Expected: all tests in this package PASS (the 3 new ones plus every existing test in this
package, unaffected).

- [ ] **Step 5: Wire it into main.go**

In `cmd/timadorus-engine/main.go`, inside `run`, change:

```go
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	newSubscriber := func(durableName string) (message.Subscriber, error) {
```
to:
```go
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Registering the Timadorus Ruleset here, before anything else starts, means a registration
	// failure (anything other than "it already exists") terminates this process via run()'s
	// existing error return -> main()'s os.Exit(1), before /readyz ever starts listening and
	// before this binary consumes a single event.
	if err := timadorusengine.RegisterRuleset(ctx, pool); err != nil {
		return err
	}

	newSubscriber := func(durableName string) (message.Subscriber, error) {
```

(`timadorusengine` is the existing import alias for this package already in this file — no new
import needed.)

- [ ] **Step 6: Full build/vet/test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: clean.

- [ ] **Step 7: Commit**

```bash
git add internal/engine/timadorus/register.go internal/engine/timadorus/register_test.go \
  internal/engine/timadorus/testutil_test.go cmd/timadorus-engine/main.go
git commit -m "timadorus-engine: self-register the Timadorus Ruleset at startup"
```

---

### Task 4: Remove Ruleset seeding from devcluster's dev-up

**Files:**
- Modify: `test/e2e/internal/seed.go`
- Modify: `test/e2e/cmd/devcluster/up.go`

**Interfaces:**
- Consumes: nothing from earlier tasks at the Go type level — this task is purely deleting a now-
  redundant HTTP call and adjusting prose, safe only because Task 3 makes the engine register the
  Ruleset itself before `dev-up`'s seeding step runs (see design spec §4 for why the ordering is
  guaranteed).
- Produces: nothing consumed elsewhere.

- [ ] **Step 1: Update SeedRulesetName's doc comment**

In `test/e2e/internal/seed.go`, change:

```go
// SeedRulesetName is the fixed Ruleset name devcluster seeds into a fresh platform — a literal
// per this tool's own requirement, exported so up.go's printStatus can reference the same
// constant seed.go seeds with instead of duplicating the string.
const SeedRulesetName = "Timadorus"
```
to:
```go
// SeedRulesetName is the fixed Ruleset name timadorus-engine registers into a fresh platform on
// startup (internal/engine/timadorus.TargetRulesetName) — seed.go no longer creates it itself
// (the engine's own /readyz gates on that registration succeeding, and InstallPlatform's `helm
// upgrade --wait` blocks on /readyz before SeedPlatformData ever runs), but this constant is kept
// exported so up.go's printStatus can reference the same literal instead of duplicating the
// string.
const SeedRulesetName = "Timadorus"
```

- [ ] **Step 2: Update SeedPlatformData's doc comment and remove the Ruleset seeding call**

In the same file, change:

```go
// SeedPlatformData creates baseline dev data through the platform's own HTTP APIs, reached via a
// temporary port-forward to the same shared Traefik Gateway a developer's browser uses (a live
// smoke test of that routing as a side effect): a User matching zitadel.TestLoginName (i.e.
// "devuser@timadorus.local" — derived from the same value the login form accepts, not
// re-hardcoded, so it can never drift from the account a developer actually logs in with) and a
// Ruleset named SeedRulesetName. That name match is cosmetic convenience only, for a developer
// eyeballing the seeded data — there is no actual linkage between this platform User row and the
// Zitadel/OIDC identity a developer logs in as (the web SPA's auth store never maps
// sub/preferred_username to a domain User). Each is checked against the query-api's own list by
// name first and skipped if already present — User/Ruleset names carry no uniqueness constraint
// at the domain level, so an unconditional create would pile up duplicates on every repeated
// `make dev-up` against an already-seeded cluster. zitadelPort/gatewayPort are reused transiently
// (matching InstallZitadel's own reuse of externalPort for its bootstrap port-forward) — nothing
// else holds either port open during `up` itself.
func SeedPlatformData(zitadel ZitadelBootstrap, zitadelPort, gatewayPort int) error {
	token, err := fetchSeedAccessToken(zitadel, zitadelPort)
	if err != nil {
		return fmt.Errorf("e2eutil: fetch seed access token: %w", err)
	}

	prevNamespace := Namespace
	Namespace = TraefikNamespace
	pf, err := StartPortForward("traefik", gatewayPort, 80, "/", 2*time.Minute)
	Namespace = prevNamespace
	if err != nil {
		return fmt.Errorf("e2eutil: port-forward traefik for seeding: %w", err)
	}
	defer pf.Stop()

	// "localhost", not "127.0.0.1": Traefik's HTTPRoute is host-matched against
	// gateway.pathRouting.hostname (up.go sets it to "localhost" — see PathRoutingHostname),
	// same as Zitadel's ExternalDomain check below — reproduced live as a 404 from Traefik
	// itself (no matching route) when this used 127.0.0.1.
	base := fmt.Sprintf("http://localhost:%d", gatewayPort)
	if err := ensureSeedResource(base, token, "/api/query/users", "/api/command/users", zitadel.TestLoginName); err != nil {
		return fmt.Errorf("e2eutil: seed user: %w", err)
	}
	if err := ensureSeedResource(base, token, "/api/query/rulesets", "/api/command/rulesets", SeedRulesetName); err != nil {
		return fmt.Errorf("e2eutil: seed ruleset: %w", err)
	}
	return nil
}
```
to:
```go
// SeedPlatformData creates a baseline dev User through the platform's own HTTP APIs, reached via
// a temporary port-forward to the same shared Traefik Gateway a developer's browser uses (a live
// smoke test of that routing as a side effect): a User matching zitadel.TestLoginName (i.e.
// "devuser@timadorus.local" — derived from the same value the login form accepts, not
// re-hardcoded, so it can never drift from the account a developer actually logs in with). There
// is no actual linkage between this platform User row and the Zitadel/OIDC identity a developer
// logs in as (the web SPA's auth store never maps sub/preferred_username to a domain User). It is
// checked against the query-api's own list by name first and skipped if already present — User
// names carry no uniqueness constraint at the domain level, so an unconditional create would pile
// up duplicates on every repeated `make dev-up` against an already-seeded cluster. (The
// "Timadorus" Ruleset used to be seeded here too; timadorus-engine now registers it itself at
// startup — see SeedRulesetName's doc comment.) zitadelPort/gatewayPort are reused transiently
// (matching InstallZitadel's own reuse of externalPort for its bootstrap port-forward) — nothing
// else holds either port open during `up` itself.
func SeedPlatformData(zitadel ZitadelBootstrap, zitadelPort, gatewayPort int) error {
	token, err := fetchSeedAccessToken(zitadel, zitadelPort)
	if err != nil {
		return fmt.Errorf("e2eutil: fetch seed access token: %w", err)
	}

	prevNamespace := Namespace
	Namespace = TraefikNamespace
	pf, err := StartPortForward("traefik", gatewayPort, 80, "/", 2*time.Minute)
	Namespace = prevNamespace
	if err != nil {
		return fmt.Errorf("e2eutil: port-forward traefik for seeding: %w", err)
	}
	defer pf.Stop()

	// "localhost", not "127.0.0.1": Traefik's HTTPRoute is host-matched against
	// gateway.pathRouting.hostname (up.go sets it to "localhost" — see PathRoutingHostname),
	// same as Zitadel's ExternalDomain check below — reproduced live as a 404 from Traefik
	// itself (no matching route) when this used 127.0.0.1.
	base := fmt.Sprintf("http://localhost:%d", gatewayPort)
	if err := ensureSeedResource(base, token, "/api/query/users", "/api/command/users", zitadel.TestLoginName); err != nil {
		return fmt.Errorf("e2eutil: seed user: %w", err)
	}
	return nil
}
```

- [ ] **Step 3: Trim seedNameExists's doc comment**

In the same file, change:

```go
// seedNameExists reflects only non-archived rows: the query-api list endpoints it queries
// (/users, /rulesets) are documented as "List non-archived", so if a developer manually archives
// the seeded User/Ruleset between runs, the next `make dev-up` won't detect it as already present
// and will create a new same-named one instead.
```
to:
```go
// seedNameExists reflects only non-archived rows: the query-api's /users list endpoint (its only
// remaining caller here) is documented as "List non-archived", so if a developer manually
// archives the seeded User between runs, the next `make dev-up` won't detect it as already
// present and will create a new same-named one instead.
```

- [ ] **Step 4: Update up.go's status message**

In `test/e2e/cmd/devcluster/up.go`, inside `printStatus`, change:

```go
	if seeded {
		fmt.Printf("Pre-seeded: User %q, Ruleset %q.\n\n", zitadel.TestLoginName, e2eutil.SeedRulesetName)
	}
```
to:
```go
	if seeded {
		fmt.Printf("Pre-seeded: User %q.\n", zitadel.TestLoginName)
	}
	fmt.Printf("Ruleset %q is auto-registered by timadorus-engine on startup.\n\n", e2eutil.SeedRulesetName)
```

- [ ] **Step 5: Full build/vet/test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add test/e2e/internal/seed.go test/e2e/cmd/devcluster/up.go
git commit -m "devcluster: stop seeding the Timadorus Ruleset; the engine registers it now"
```

---

## Final Verification

- `go build ./... && go vet ./... && go test ./...` clean.
- `go generate ./...` produces no diff (already regenerated and committed in Task 2).
- If a real Kubernetes cluster is available in the environment executing this plan's final review:
  `make dev-up` against a fresh cluster, then confirm via `GET /api/query/rulesets` (through the
  printed port-forward) that "Timadorus" exists without `seed.go` having created it, and that a
  second `make dev-up` against the same cluster still succeeds (the engine's restart hits the
  already-exists path cleanly, not a crash loop). If that environment isn't available, record
  honestly in the final review that this specific scenario was reasoned through and covered by
  `register_test.go`'s `TestRegisterRuleset_NoOpOnSecondCall`, but not re-verified live — following
  the same honest-caveat precedent used in this project's prior `campaign-configuration-timadorus-
  engine` branch.
