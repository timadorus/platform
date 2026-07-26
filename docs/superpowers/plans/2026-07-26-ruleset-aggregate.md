# Ruleset Aggregate Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a seventh aggregate type, `Ruleset` (independent, top-level, no parent —
`name`/`description`/`references []string`), wire an immutable `rulesetId` reference into
`Campaign` (validated at creation like `universeId`, never changeable after), and add
`GET /users`, `GET /universes`, `GET /rulesets` list-all endpoints for the three parentless
aggregate types, end to end: domain, command, projection, query, both HTTP APIs, both OpenAPI
specs, all three binaries' wiring, the CLI, `docs/PLAN.md`, and the e2e test suite.

**Architecture:** Every new file mechanically copies an existing template —
`internal/domain/user` for Ruleset's "no parent" shape (extended with two new mutation
commands, `SetDescription`/`SetReferences`, since Ruleset is the first aggregate with mutable
fields beyond `name`), `internal/domain/entity`'s parent-validation pattern for Campaign's new
Ruleset check (reusing the existing `apperrors.ErrParentNotFound`/`ErrParentArchived`
vocabulary, exactly how Campaign already validates its Universe parent), and
`internal/query/entity`'s `ListByUniverse` shape (minus the parent filter) for the three new
list-all query methods.

**Tech Stack:** No new dependencies — this is entirely additive/modificatory Go code, SQL
migrations, and OpenAPI YAML within the existing stack (pgx/v5, oapi-codegen, Watermill/NATS,
Cobra).

## Global Constraints

- The Postgres column backing `references` is named `reference_urls`, never `references` —
  confirmed by direct testing that `REFERENCES` is a reserved SQL keyword and errors as an
  unquoted column name (`syntax error at or near "references"`). The Go field, JSON field, and
  OpenAPI property name all stay `references`/`References` — only the SQL column differs.
- `references`/`description` have no validation beyond being present in the request body types
  oapi-codegen generates — empty string, empty list, and arbitrary non-URL strings are all
  valid. Only `name` is validated (`ErrNameRequired`, matching every other aggregate).
- Ruleset has no parent — no `universeId`-style field, no `ListByX` query method, no CLI parent
  flag. Its `SetDescription`/`SetReferences` commands are each a full-value replace (not
  incremental add/remove) since there is no minimum-count invariant to protect, unlike
  Creators/Gamemasters.
- `rulesetId` on Campaign is set once at creation via `campaign.New`'s constructor and is
  never mutated by any other command — no `SetRuleset`, no HTTP endpoint for it.
- Every new command-service validation reuses the existing
  `internal/command/apperrors` vocabulary (`ErrParentNotFound`, `ErrParentArchived`) — no new
  sentinel errors are introduced for cross-aggregate reference checks.
- The three new list-all query endpoints (`GET /users`, `GET /universes`, `GET /rulesets`)
  have no pagination and no `includeArchived` parameter, matching every existing list endpoint
  in this codebase exactly (plan §13 already documents this as the deliberate v1 scope).
- After every OpenAPI spec edit, `go generate ./...` must be run and must produce a
  `git diff --exit-code`-clean result the second time it's run (idempotent codegen) — this is
  the existing CI check (`.github/workflows/ci.yml`) and must keep passing.

---

### Task 1: Ruleset domain layer (TDD)

**Files:**
- Create: `internal/domain/ruleset/events/events.go`
- Create: `internal/domain/ruleset/ruleset.go`
- Create: `internal/domain/ruleset/errors.go`
- Create: `internal/domain/ruleset/ruleset_test.go`

**Interfaces:**
- Produces: `ruleset.AggregateType` (const `"ruleset"`), `ruleset.Ruleset` (type, embeds
  `eventsourcing.Base`), `ruleset.New(name, description string, references []string)
  (*Ruleset, error)`, `(*Ruleset) Rename/SetDescription/SetReferences/Archive`,
  `(*Ruleset) Name()/Description()/References()/IsArchived()`, `ruleset.ErrNameRequired`,
  `ruleset.ErrArchived` — every later task imports these exact names.

- [ ] **Step 1: Write the failing test — `internal/domain/ruleset/ruleset_test.go`**

```go
package ruleset_test

import (
	"reflect"
	"testing"

	"github.com/timadorus/platform/internal/domain/ruleset"
)

func TestNew(t *testing.T) {
	t.Run("requires a name", func(t *testing.T) {
		_, err := ruleset.New("", "a ruleset", []string{"https://example.com"})
		if err != ruleset.ErrNameRequired {
			t.Fatalf("got %v, want ErrNameRequired", err)
		}
	})

	t.Run("accepts empty description and empty references", func(t *testing.T) {
		r, err := ruleset.New("D&D 5e", "", nil)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Name() != "D&D 5e" {
			t.Fatalf("got name %q", r.Name())
		}
		if r.Description() != "" {
			t.Fatalf("got description %q, want empty", r.Description())
		}
		if len(r.References()) != 0 {
			t.Fatalf("got references %v, want empty", r.References())
		}
	})

	t.Run("creates with the given description and references", func(t *testing.T) {
		refs := []string{"https://example.com/rules", "https://example.com/srd"}
		r, err := ruleset.New("D&D 5e", "Fifth edition", refs)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if r.Description() != "Fifth edition" {
			t.Fatalf("got description %q", r.Description())
		}
		if !reflect.DeepEqual(r.References(), refs) {
			t.Fatalf("got references %v, want %v", r.References(), refs)
		}
	})
}

func TestMutateAndArchive(t *testing.T) {
	r, err := ruleset.New("D&D 5e", "Fifth edition", []string{"https://example.com"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	r.ClearPending()

	if err := r.SetDescription("Updated description"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if r.Description() != "Updated description" {
		t.Fatalf("got description %q", r.Description())
	}

	newRefs := []string{"https://example.com/new"}
	if err := r.SetReferences(newRefs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !reflect.DeepEqual(r.References(), newRefs) {
		t.Fatalf("got references %v, want %v", r.References(), newRefs)
	}

	if err := r.Archive(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := r.Archive(); err != nil {
		t.Fatalf("archiving twice should be idempotent, got: %v", err)
	}
	if err := r.Rename("Fail"); err != ruleset.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
	if err := r.SetDescription("Fail"); err != ruleset.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
	if err := r.SetReferences([]string{"https://fail.example.com"}); err != ruleset.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/domain/ruleset/...`
Expected: **FAIL** — `no Go files in .../internal/domain/ruleset` (the package doesn't exist
yet).

- [ ] **Step 3: Write `internal/domain/ruleset/events/events.go`**

```go
// Package events holds Ruleset's domain events and their registry hookup only — no business
// logic (same shape as domain/universe/events; see that package's doc comment).
package events

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType is the stable string used to namespace this aggregate's events in the event
// store and its subject on the event bus.
const AggregateType = "ruleset"

const (
	TypeRulesetCreated             = "ruleset.created.v1"
	TypeRulesetRenamed             = "ruleset.renamed.v1"
	TypeRulesetDescriptionChanged  = "ruleset.description_changed.v1"
	TypeRulesetReferencesChanged   = "ruleset.references_changed.v1"
	TypeRulesetArchived            = "ruleset.archived.v1"
)

type RulesetCreated struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	References  []string  `json:"references"`
	OccurredAt  time.Time `json:"occurredAt"`
}

func (RulesetCreated) EventType() string { return TypeRulesetCreated }

type RulesetRenamed struct {
	Name       string    `json:"name"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (RulesetRenamed) EventType() string { return TypeRulesetRenamed }

type RulesetDescriptionChanged struct {
	Description string    `json:"description"`
	OccurredAt  time.Time `json:"occurredAt"`
}

func (RulesetDescriptionChanged) EventType() string { return TypeRulesetDescriptionChanged }

type RulesetReferencesChanged struct {
	References []string  `json:"references"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (RulesetReferencesChanged) EventType() string { return TypeRulesetReferencesChanged }

type RulesetArchived struct {
	OccurredAt time.Time `json:"occurredAt"`
}

func (RulesetArchived) EventType() string { return TypeRulesetArchived }

// Register hooks every Ruleset event into reg so infrastructure can deserialize persisted
// payloads back into these concrete types during replay.
func Register(reg *eventsourcing.Registry) {
	reg.Register(TypeRulesetCreated, func() eventsourcing.Event { return &RulesetCreated{} })
	reg.Register(TypeRulesetRenamed, func() eventsourcing.Event { return &RulesetRenamed{} })
	reg.Register(TypeRulesetDescriptionChanged, func() eventsourcing.Event { return &RulesetDescriptionChanged{} })
	reg.Register(TypeRulesetReferencesChanged, func() eventsourcing.Event { return &RulesetReferencesChanged{} })
	reg.Register(TypeRulesetArchived, func() eventsourcing.Event { return &RulesetArchived{} })
}
```

- [ ] **Step 4: Write `internal/domain/ruleset/errors.go`**

```go
package ruleset

import "errors"

var (
	ErrNameRequired = errors.New("ruleset: name is required")
	ErrArchived     = errors.New("ruleset: ruleset is archived")
)
```

- [ ] **Step 5: Write `internal/domain/ruleset/ruleset.go`**

```go
// Package ruleset is an independent, top-level aggregate with no parent (same shape as
// domain/user) representing a reusable game system that a Campaign references immutably at
// creation (plan §2). Unlike User/Entity/Object, it has two mutable fields beyond name —
// description and references — each replaced wholesale by its own command rather than
// incrementally, since neither has a minimum-count invariant to protect.
package ruleset

import (
	"time"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/ruleset/events"
	"github.com/timadorus/platform/internal/eventsourcing"
)

// AggregateType re-exports events.AggregateType — see domain/universe.AggregateType for why.
const AggregateType = events.AggregateType

type Ruleset struct {
	eventsourcing.Base

	name        string
	description string
	references  []string
	archived    bool
}

func (r *Ruleset) Name() string          { return r.name }
func (r *Ruleset) Description() string   { return r.description }
func (r *Ruleset) References() []string  { return r.references }
func (r *Ruleset) IsArchived() bool      { return r.archived }

func New(name, description string, references []string) (*Ruleset, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	r := &Ruleset{}
	r.raise(&events.RulesetCreated{
		ID:          uuid.New(),
		Name:        name,
		Description: description,
		References:  references,
		OccurredAt:  time.Now().UTC(),
	})
	return r, nil
}

func (r *Ruleset) Rename(name string) error {
	if r.archived {
		return ErrArchived
	}
	if name == "" {
		return ErrNameRequired
	}
	if name == r.name {
		return nil
	}
	r.raise(&events.RulesetRenamed{Name: name, OccurredAt: time.Now().UTC()})
	return nil
}

func (r *Ruleset) SetDescription(description string) error {
	if r.archived {
		return ErrArchived
	}
	r.raise(&events.RulesetDescriptionChanged{Description: description, OccurredAt: time.Now().UTC()})
	return nil
}

func (r *Ruleset) SetReferences(references []string) error {
	if r.archived {
		return ErrArchived
	}
	r.raise(&events.RulesetReferencesChanged{References: references, OccurredAt: time.Now().UTC()})
	return nil
}

// Archive is idempotent — see universe.Universe.Archive's doc comment for why.
func (r *Ruleset) Archive() error {
	if r.archived {
		return nil
	}
	r.raise(&events.RulesetArchived{OccurredAt: time.Now().UTC()})
	return nil
}

func (r *Ruleset) Apply(event eventsourcing.Event) {
	switch e := event.(type) {
	case *events.RulesetCreated:
		r.SetID(e.ID)
		r.name = e.Name
		r.description = e.Description
		r.references = e.References
	case *events.RulesetRenamed:
		r.name = e.Name
	case *events.RulesetDescriptionChanged:
		r.description = e.Description
	case *events.RulesetReferencesChanged:
		r.references = e.References
	case *events.RulesetArchived:
		r.archived = true
	}
}

func (r *Ruleset) raise(event eventsourcing.Event) {
	r.Base.Raise(r, event)
}
```

- [ ] **Step 6: Run the tests again to verify they pass**

Run: `go test ./internal/domain/ruleset/... -v`
Expected: `PASS` — `TestNew` (all three subtests) and `TestMutateAndArchive`.

- [ ] **Step 7: Commit**

```bash
git add internal/domain/ruleset/
git commit -m "domain: add Ruleset aggregate"
```

---

### Task 2: Ruleset command service

**Files:**
- Create: `internal/command/ruleset/service.go`

**Interfaces:**
- Consumes: `ruleset.New`, `ruleset.Ruleset`, `eventsourcing.Repository[*ruleset.Ruleset]`
  (Task 1).
- Produces: `rulesetcmd.Service`, `rulesetcmd.NewService(repo) *Service`, and
  `(*Service) Create/Rename/SetDescription/SetReferences/Archive` — Task 9 (main.go wiring)
  and Task 7 (HTTP handlers) call these exact names.

- [ ] **Step 1: Write `internal/command/ruleset/service.go`**

```go
// Package ruleset is the application-layer command service for Ruleset. It has no other
// aggregate type to validate against — Ruleset has no parent (plan §2) — matching
// internal/command/user's shape exactly.
package ruleset

import (
	"context"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/domain/ruleset"
	"github.com/timadorus/platform/internal/eventsourcing"
)

type Service struct {
	repo *eventsourcing.Repository[*ruleset.Ruleset]
}

func NewService(repo *eventsourcing.Repository[*ruleset.Ruleset]) *Service {
	return &Service{repo: repo}
}

func (s *Service) Create(ctx context.Context, name, description string, references []string) (uuid.UUID, error) {
	r, err := ruleset.New(name, description, references)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.repo.Save(ctx, r); err != nil {
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

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/command/ruleset/...`
Expected: clean (no test — matches the plan's Non-goal of no command-service-layer tests,
consistent with `internal/command/entity`/`internal/command/campaign` having none either).

- [ ] **Step 3: Commit**

```bash
git add internal/command/ruleset/
git commit -m "command: add Ruleset command service"
```

---

### Task 3: Ruleset projection (migration + projector)

**Files:**
- Create: `internal/projection/ruleset/migrations/0001_ruleset_read_model.up.sql`
- Create: `internal/projection/ruleset/migrations/0001_ruleset_read_model.down.sql`
- Create: `internal/projection/ruleset/projector.go`

**Interfaces:**
- Consumes: `ruleset/events` (Task 1), `bus.Envelope`, `bus.Subject`, `projection.Projector`
  interface (existing).
- Produces: `rulesetprojection.NewProjector() *Projector` — Task 9 (projector main.go)
  registers this.

- [ ] **Step 1: Write `internal/projection/ruleset/migrations/0001_ruleset_read_model.up.sql`**

```sql
CREATE TABLE rulesets_read_model (
    id             UUID PRIMARY KEY,
    name           TEXT NOT NULL,
    description    TEXT NOT NULL DEFAULT '',
    reference_urls TEXT[] NOT NULL DEFAULT '{}',
    is_archived    BOOLEAN NOT NULL DEFAULT false,
    updated_at     TIMESTAMPTZ NOT NULL
);
```

- [ ] **Step 2: Write `internal/projection/ruleset/migrations/0001_ruleset_read_model.down.sql`**

```sql
DROP TABLE rulesets_read_model;
```

- [ ] **Step 3: Write `internal/projection/ruleset/projector.go`**

```go
// Package ruleset is the Ruleset read-model projector. Imports only domain/ruleset/events —
// see projection/universe's doc comment for why.
package ruleset

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/ruleset/events"
)

const projectorName = "ruleset-read-model"

type Projector struct{}

func NewProjector() *Projector { return &Projector{} }

func (p *Projector) Name() string { return projectorName }

func (p *Projector) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *Projector) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	switch env.EventType {
	case events.TypeRulesetCreated:
		return p.handleCreated(ctx, tx, env)
	case events.TypeRulesetRenamed:
		return p.handleRenamed(ctx, tx, env)
	case events.TypeRulesetDescriptionChanged:
		return p.handleDescriptionChanged(ctx, tx, env)
	case events.TypeRulesetReferencesChanged:
		return p.handleReferencesChanged(ctx, tx, env)
	case events.TypeRulesetArchived:
		return p.handleArchived(ctx, tx, env)
	default:
		return nil
	}
}

func (p *Projector) handleCreated(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.RulesetCreated
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("ruleset projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx,
		`INSERT INTO rulesets_read_model (id, name, description, reference_urls, is_archived, updated_at)
		 VALUES ($1, $2, $3, $4, false, $5)
		 ON CONFLICT (id) DO NOTHING`,
		env.AggregateID, e.Name, e.Description, e.References, e.OccurredAt,
	)
	return err
}

func (p *Projector) handleRenamed(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.RulesetRenamed
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("ruleset projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx, `UPDATE rulesets_read_model SET name = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, e.Name, e.OccurredAt)
	return err
}

func (p *Projector) handleDescriptionChanged(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.RulesetDescriptionChanged
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("ruleset projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx, `UPDATE rulesets_read_model SET description = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, e.Description, e.OccurredAt)
	return err
}

func (p *Projector) handleReferencesChanged(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.RulesetReferencesChanged
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("ruleset projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx, `UPDATE rulesets_read_model SET reference_urls = $2, updated_at = $3 WHERE id = $1`, env.AggregateID, e.References, e.OccurredAt)
	return err
}

func (p *Projector) handleArchived(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.RulesetArchived
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("ruleset projector: unmarshal %s: %w", env.EventType, err)
	}
	_, err := tx.Exec(ctx, `UPDATE rulesets_read_model SET is_archived = true, updated_at = $2 WHERE id = $1`, env.AggregateID, e.OccurredAt)
	return err
}
```

- [ ] **Step 4: Add the new schema owner to `scripts/migrate-up.sh`**

In `scripts/migrate-up.sh`, add a new line to the `schema_owners` array (after the
`projection_object` line):

```bash
  "projection_ruleset:internal/projection/ruleset/migrations"
```

- [ ] **Step 5: Run the migration for real against the dev database**

```bash
make dev-up   # if not already running
make migrate-up
```
Expected: nine `==> migrate up: <name>` lines (the existing eight plus
`projection_ruleset`), all succeeding.

Verify the table shape directly:
```bash
docker compose exec postgres psql -U timadorus -d timadorus -c '\d rulesets_read_model'
```
Expected: columns `id` (uuid), `name` (text), `description` (text), `reference_urls`
(text[]), `is_archived` (boolean), `updated_at` (timestamptz).

- [ ] **Step 6: Commit**

```bash
git add internal/projection/ruleset/ scripts/migrate-up.sh
git commit -m "projection: add Ruleset read-model projector and migration"
```

---

### Task 4: Ruleset query repository

**Files:**
- Create: `internal/query/ruleset/repository.go`

**Interfaces:**
- Consumes: `rulesets_read_model` table (Task 3).
- Produces: `rulesetquery.Ruleset` (DTO), `rulesetquery.Repository`,
  `rulesetquery.NewRepository(pool) *Repository`, `(*Repository) Get/ListAll`,
  `rulesetquery.ErrNotFound` — Task 8 (query HTTP handlers) and Task 9 (query-api main.go)
  consume these.

- [ ] **Step 1: Write `internal/query/ruleset/repository.go`**

```go
// Package ruleset reads the rulesets_read_model table written by internal/projection/ruleset.
package ruleset

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("ruleset: not found")

type Ruleset struct {
	ID          uuid.UUID
	Name        string
	Description string
	References  []string
	IsArchived  bool
}

type Repository struct {
	pool *pgxpool.Pool
}

func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{pool: pool}
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Ruleset, error) {
	var out Ruleset
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, description, reference_urls, is_archived FROM rulesets_read_model WHERE id = $1`, id,
	).Scan(&out.ID, &out.Name, &out.Description, &out.References, &out.IsArchived)
	if errors.Is(err, pgx.ErrNoRows) {
		return Ruleset{}, ErrNotFound
	}
	if err != nil {
		return Ruleset{}, fmt.Errorf("query/ruleset: get %s: %w", id, err)
	}
	return out, nil
}

// ListAll returns every non-archived Ruleset, ordered by name. Ruleset has no parent to scope
// by (plan §2), matching User/Universe's own list-all shape.
func (r *Repository) ListAll(ctx context.Context) ([]Ruleset, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, description, reference_urls, is_archived FROM rulesets_read_model
		 WHERE is_archived = false
		 ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("query/ruleset: list all: %w", err)
	}
	defer rows.Close()

	var rulesets []Ruleset
	for rows.Next() {
		var out Ruleset
		if err := rows.Scan(&out.ID, &out.Name, &out.Description, &out.References, &out.IsArchived); err != nil {
			return nil, fmt.Errorf("query/ruleset: scan row: %w", err)
		}
		rulesets = append(rulesets, out)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/ruleset: iterate rows: %w", err)
	}
	return rulesets, nil
}
```

- [ ] **Step 2: Verify it compiles**

Run: `go build ./internal/query/ruleset/...`
Expected: clean.

- [ ] **Step 3: Commit**

```bash
git add internal/query/ruleset/
git commit -m "query: add Ruleset read repository"
```

---

### Task 5: Campaign domain layer — add immutable `rulesetId`

**Files:**
- Modify: `internal/domain/campaign/events/events.go`
- Modify: `internal/domain/campaign/campaign.go`
- Modify: `internal/domain/campaign/campaign_test.go`

**Interfaces:**
- Produces: `campaign.New`'s new signature
  `New(universeID, rulesetID uuid.UUID, name string, gamemasterIDs []uuid.UUID) (*Campaign,
  error)` and `(*Campaign) RulesetID() uuid.UUID` — Task 6 (command service) and every other
  caller of `campaign.New` must use this new signature.

- [ ] **Step 1: Modify `internal/domain/campaign/events/events.go`**

Add `RulesetID` to `CampaignCreated`, placed after `UniverseID`:

```go
// CampaignCreated carries UniverseID and RulesetID (both immutable parent-like references)
// since neither is derivable from the envelope (plan §4.5).
type CampaignCreated struct {
	ID                uuid.UUID   `json:"id"`
	Name              string      `json:"name"`
	UniverseID        uuid.UUID   `json:"universeId"`
	RulesetID         uuid.UUID   `json:"rulesetId"`
	GamemasterUserIDs []uuid.UUID `json:"gamemasterUserIds"`
	OccurredAt        time.Time   `json:"occurredAt"`
}
```
(Replace the existing `CampaignCreated` struct and its preceding doc comment with this.)

- [ ] **Step 2: Modify `internal/domain/campaign/campaign.go`**

Add the field, getter, constructor parameter, and `Apply` case. The `Campaign` struct becomes:

```go
type Campaign struct {
	eventsourcing.Base

	name        string
	universeID  uuid.UUID
	rulesetID   uuid.UUID
	gamemasters map[uuid.UUID]struct{}
	archived    bool
}

func (c *Campaign) Name() string          { return c.name }
func (c *Campaign) UniverseID() uuid.UUID { return c.universeID }
func (c *Campaign) RulesetID() uuid.UUID  { return c.rulesetID }
func (c *Campaign) IsArchived() bool      { return c.archived }
```

`New` becomes:

```go
// New constructs and creates a new Campaign under universeID, referencing rulesetID
// immutably (plan §2 — a Campaign's Ruleset can never change; a new Campaign must be created
// to use a different one). The caller (the application command service) is responsible for
// having already validated that universeID and rulesetID refer to existing, non-archived
// aggregates and that every id in gamemasterIDs refers to an existing, non-archived User
// (plan §4.3) — this constructor only enforces the aggregate's own invariants (non-blank
// name, non-empty Gamemasters).
func New(universeID, rulesetID uuid.UUID, name string, gamemasterIDs []uuid.UUID) (*Campaign, error) {
	if name == "" {
		return nil, ErrNameRequired
	}
	ids := dedupe(gamemasterIDs)
	if len(ids) == 0 {
		return nil, ErrGamemastersRequired
	}
	c := &Campaign{}
	c.raise(&events.CampaignCreated{
		ID:                uuid.New(),
		Name:              name,
		UniverseID:        universeID,
		RulesetID:         rulesetID,
		GamemasterUserIDs: ids,
		OccurredAt:        time.Now().UTC(),
	})
	return c, nil
}
```

`Apply`'s `CampaignCreated` case gains one line:

```go
	case *events.CampaignCreated:
		c.SetID(e.ID)
		c.name = e.Name
		c.universeID = e.UniverseID
		c.rulesetID = e.RulesetID
		c.gamemasters = toSet(e.GamemasterUserIDs)
```

- [ ] **Step 3: Modify `internal/domain/campaign/campaign_test.go`**

Every `campaign.New(...)` call gains a second argument (a fresh `uuid.New()`) right after the
universe id. There are four call sites; update each:

```go
_, err := campaign.New(universeID, uuid.New(), "", []uuid.UUID{uuid.New()})
```
```go
_, err := campaign.New(universeID, uuid.New(), "Curse of Strahd", nil)
```
```go
c, err := campaign.New(universeID, uuid.New(), "Curse of Strahd", []uuid.UUID{gm})
```
(this one also gains an assertion — add right after the existing `UniverseID()` check:)
```go
		if c.UniverseID() != universeID {
			t.Fatalf("got universeID %s, want %s", c.UniverseID(), universeID)
		}
```
stays as-is; no new assertion strictly required here since `RulesetID()` isn't part of this
existing subtest's stated purpose — leave `TestNew`'s third subtest otherwise unchanged beyond
the added argument.

The remaining two call sites (in `TestGamemasters` and `TestArchive`):
```go
c, err := campaign.New(uuid.New(), uuid.New(), "Curse of Strahd", []uuid.UUID{gm})
```
```go
c, err := campaign.New(uuid.New(), uuid.New(), "Curse of Strahd", []uuid.UUID{uuid.New()})
```

- [ ] **Step 4: Run the tests**

Run: `go test ./internal/domain/campaign/... -v`
Expected: `PASS` — `TestNew`, `TestGamemasters`, `TestArchive`.

- [ ] **Step 5: Confirm nothing outside this package calls `campaign.New` yet**

Run: `grep -rn "campaign\.New(" --include=*.go .`
Expected: only `internal/domain/campaign/campaign_test.go` (this task) and
`internal/command/campaign/service.go` (fixed in Task 6, not yet). If anything else appears,
note it — it will need the same signature update.

- [ ] **Step 6: Commit**

```bash
git add internal/domain/campaign/
git commit -m "domain: add immutable RulesetID to Campaign"
```

---

### Task 6: Wire Ruleset through Campaign's command/projection/query layers

**Files:**
- Modify: `internal/command/campaign/service.go`
- Create: `internal/projection/campaign/migrations/0002_campaign_ruleset_id.up.sql`
- Create: `internal/projection/campaign/migrations/0002_campaign_ruleset_id.down.sql`
- Modify: `internal/projection/campaign/projector.go`
- Modify: `internal/query/campaign/repository.go`

**Interfaces:**
- Consumes: `ruleset.Ruleset`, `eventsourcing.Repository[*ruleset.Ruleset]` (Task 1);
  `campaign.New`'s new signature (Task 5).
- Produces: `campaigncmd.NewService`'s new signature (4 params) and `CreateCmd.RulesetID` —
  Task 7 (HTTP handler) and Task 9 (main.go) use these.

- [ ] **Step 1: Modify `internal/command/campaign/service.go`**

Add the import, field, constructor parameter, `CreateCmd` field, and validation block:

```go
import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/timadorus/platform/internal/command/apperrors"
	"github.com/timadorus/platform/internal/domain/campaign"
	"github.com/timadorus/platform/internal/domain/ruleset"
	"github.com/timadorus/platform/internal/domain/universe"
	"github.com/timadorus/platform/internal/domain/user"
	"github.com/timadorus/platform/internal/eventsourcing"
)

type Service struct {
	campaigns *eventsourcing.Repository[*campaign.Campaign]
	universes *eventsourcing.Repository[*universe.Universe] // existence/archived-state checks only
	users     *eventsourcing.Repository[*user.User]          // existence/archived-state checks only
	rulesets  *eventsourcing.Repository[*ruleset.Ruleset]     // existence/archived-state checks only
}

func NewService(
	campaigns *eventsourcing.Repository[*campaign.Campaign],
	universes *eventsourcing.Repository[*universe.Universe],
	users *eventsourcing.Repository[*user.User],
	rulesets *eventsourcing.Repository[*ruleset.Ruleset],
) *Service {
	return &Service{campaigns: campaigns, universes: universes, users: users, rulesets: rulesets}
}

type CreateCmd struct {
	UniverseID        uuid.UUID
	RulesetID         uuid.UUID
	Name              string
	GamemasterUserIDs []uuid.UUID
}

func (s *Service) Create(ctx context.Context, cmd CreateCmd) (uuid.UUID, error) {
	universeAgg, err := s.universes.Load(ctx, cmd.UniverseID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: universe %s", apperrors.ErrParentNotFound, cmd.UniverseID)
	}
	if universeAgg.IsArchived() {
		return uuid.Nil, fmt.Errorf("%w: universe %s", apperrors.ErrParentArchived, cmd.UniverseID)
	}

	rulesetAgg, err := s.rulesets.Load(ctx, cmd.RulesetID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%w: ruleset %s", apperrors.ErrParentNotFound, cmd.RulesetID)
	}
	if rulesetAgg.IsArchived() {
		return uuid.Nil, fmt.Errorf("%w: ruleset %s", apperrors.ErrParentArchived, cmd.RulesetID)
	}

	for _, id := range cmd.GamemasterUserIDs {
		userAgg, err := s.users.Load(ctx, id)
		if err != nil {
			return uuid.Nil, fmt.Errorf("%w: user %s", apperrors.ErrReferenceNotFound, id)
		}
		if userAgg.IsArchived() {
			return uuid.Nil, fmt.Errorf("%w: user %s", apperrors.ErrReferenceArchived, id)
		}
	}

	c, err := campaign.New(cmd.UniverseID, cmd.RulesetID, cmd.Name, cmd.GamemasterUserIDs)
	if err != nil {
		return uuid.Nil, err
	}
	if err := s.campaigns.Save(ctx, c); err != nil {
		return uuid.Nil, err
	}
	return c.AggregateID(), nil
}
```

`Rename`, `Archive`, `AddGamemaster`, `RemoveGamemaster` are unchanged — leave them exactly as
they are.

- [ ] **Step 2: Write `internal/projection/campaign/migrations/0002_campaign_ruleset_id.up.sql`**

```sql
ALTER TABLE campaigns_read_model ADD COLUMN ruleset_id UUID NOT NULL;
```

- [ ] **Step 3: Write `internal/projection/campaign/migrations/0002_campaign_ruleset_id.down.sql`**

```sql
ALTER TABLE campaigns_read_model DROP COLUMN ruleset_id;
```

- [ ] **Step 4: Modify `internal/projection/campaign/projector.go`'s `handleCreated`**

```go
func (p *Projector) handleCreated(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	var e events.CampaignCreated
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("campaign projector: unmarshal %s: %w", env.EventType, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO campaigns_read_model (id, name, universe_id, ruleset_id, is_archived, updated_at)
		 VALUES ($1, $2, $3, $4, false, $5)
		 ON CONFLICT (id) DO NOTHING`,
		env.AggregateID, e.Name, e.UniverseID, e.RulesetID, e.OccurredAt,
	); err != nil {
		return err
	}
	for _, userID := range e.GamemasterUserIDs {
		if _, err := tx.Exec(ctx,
			`INSERT INTO campaign_gamemasters (campaign_id, user_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			env.AggregateID, userID,
		); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: Modify `internal/query/campaign/repository.go`**

`Campaign` DTO gains a field:

```go
type Campaign struct {
	ID         uuid.UUID
	Name       string
	UniverseID uuid.UUID
	RulesetID  uuid.UUID
	IsArchived bool
}
```

`Get` and `ListByUniverse` each gain `ruleset_id`/`&c.RulesetID`:

```go
func (r *Repository) Get(ctx context.Context, id uuid.UUID) (Campaign, error) {
	var c Campaign
	err := r.pool.QueryRow(ctx,
		`SELECT id, name, universe_id, ruleset_id, is_archived FROM campaigns_read_model WHERE id = $1`, id,
	).Scan(&c.ID, &c.Name, &c.UniverseID, &c.RulesetID, &c.IsArchived)
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	if err != nil {
		return Campaign{}, fmt.Errorf("query/campaign: get %s: %w", id, err)
	}
	return c, nil
}
```

```go
func (r *Repository) ListByUniverse(ctx context.Context, universeID uuid.UUID) ([]Campaign, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, universe_id, ruleset_id, is_archived FROM campaigns_read_model
		 WHERE universe_id = $1 AND is_archived = false
		 ORDER BY name`,
		universeID,
	)
	if err != nil {
		return nil, fmt.Errorf("query/campaign: list by universe %s: %w", universeID, err)
	}
	defer rows.Close()

	var campaigns []Campaign
	for rows.Next() {
		var c Campaign
		if err := rows.Scan(&c.ID, &c.Name, &c.UniverseID, &c.RulesetID, &c.IsArchived); err != nil {
			return nil, fmt.Errorf("query/campaign: scan row: %w", err)
		}
		campaigns = append(campaigns, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/campaign: iterate rows: %w", err)
	}
	return campaigns, nil
}
```

- [ ] **Step 6: Run the new migration for real and verify**

```bash
make migrate-up
docker compose exec postgres psql -U timadorus -d timadorus -c '\d campaigns_read_model'
```
Expected: `migrate-up` succeeds (this migration is additive — `ruleset_id UUID NOT NULL` with
no default on a currently-empty table is safe; if the table already has rows from earlier
manual testing, note this in your report and truncate `campaigns_read_model`,
`campaign_gamemasters`, and the `events`/`outbox` rows for `aggregate_type = 'campaign'`
first, since this is dev data, not production). `campaigns_read_model` now shows a
`ruleset_id` column (uuid, not null).

- [ ] **Step 7: Verify the whole module still builds** (it won't fully — `internal/command/campaign` now references `internal/domain/ruleset`, and `internal/httpapi/command` / `cmd/command-api` still call the old 3-arg `NewService`/`CreateCmd` shape — that's expected and fixed in Tasks 7/9)

Run: `go build ./internal/command/campaign/... ./internal/projection/campaign/... ./internal/query/campaign/...`
Expected: clean. (Do NOT run `go build ./...` yet — `cmd/command-api` and
`internal/httpapi/command` won't compile until Tasks 7 and 9 land; that's expected, not a
regression to chase down now.)

- [ ] **Step 8: Commit**

```bash
git add internal/command/campaign/ internal/projection/campaign/ internal/query/campaign/
git commit -m "command,projection,query: wire Ruleset reference through Campaign"
```

---

### Task 7: Command API — Ruleset endpoints + Campaign's new field + OpenAPI spec

**Files:**
- Modify: `api/command/openapi.yaml`
- Modify: `internal/httpapi/command/server.go`
- Modify: `internal/httpapi/command/errors.go`

**Interfaces:**
- Consumes: `rulesetcmd.Service` (Task 2), `campaigncmd.CreateCmd.RulesetID` (Task 6).
- Produces: generated `gen.CreateRulesetRequestObject` etc. (via `go generate`), `Server`'s
  new `ruleset *rulesetcmd.Service` field — Task 9 (main.go) passes it in.

- [ ] **Step 1: Add Ruleset paths to `api/command/openapi.yaml`**

Insert immediately before the `components:` line (after the existing
`/objects/{objectId}/archive` block):

```yaml
  /rulesets:
    post:
      operationId: createRuleset
      summary: Create a new Ruleset.
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/CreateRulesetRequest"
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

  /rulesets/{rulesetId}:
    patch:
      operationId: renameRuleset
      summary: Rename a Ruleset.
      parameters:
        - $ref: "#/components/parameters/RulesetId"
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/RenameRequest"
      responses:
        "204":
          description: Renamed.
        "400":
          $ref: "#/components/responses/BadRequest"
        "404":
          $ref: "#/components/responses/NotFound"
        "409":
          $ref: "#/components/responses/Conflict"
        "422":
          $ref: "#/components/responses/UnprocessableEntity"

  /rulesets/{rulesetId}/description:
    put:
      operationId: setRulesetDescription
      summary: Replace a Ruleset's description.
      parameters:
        - $ref: "#/components/parameters/RulesetId"
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/SetRulesetDescriptionRequest"
      responses:
        "204":
          description: Updated.
        "400":
          $ref: "#/components/responses/BadRequest"
        "404":
          $ref: "#/components/responses/NotFound"
        "409":
          $ref: "#/components/responses/Conflict"

  /rulesets/{rulesetId}/references:
    put:
      operationId: setRulesetReferences
      summary: Replace a Ruleset's references list.
      parameters:
        - $ref: "#/components/parameters/RulesetId"
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/SetRulesetReferencesRequest"
      responses:
        "204":
          description: Updated.
        "400":
          $ref: "#/components/responses/BadRequest"
        "404":
          $ref: "#/components/responses/NotFound"
        "409":
          $ref: "#/components/responses/Conflict"

  /rulesets/{rulesetId}/archive:
    post:
      operationId: archiveRuleset
      summary: Archive a Ruleset. Idempotent.
      parameters:
        - $ref: "#/components/parameters/RulesetId"
      responses:
        "200":
          description: Archived (or already archived).
        "404":
          $ref: "#/components/responses/NotFound"
```

- [ ] **Step 2: Add the `RulesetId` parameter to `components/parameters`**

Add after the existing `ObjectId` entry:

```yaml
    RulesetId:
      name: rulesetId
      in: path
      required: true
      schema:
        type: string
        format: uuid
```

- [ ] **Step 3: Add Ruleset schemas to `components/schemas`**

Add anywhere in the `schemas:` block (e.g. right after `CreateObjectRequest`/
`ObjectCreatedResponse`):

```yaml
    CreateRulesetRequest:
      type: object
      required: [name]
      properties:
        name:
          type: string
        description:
          type: string
        references:
          type: array
          items:
            type: string

    RulesetCreatedResponse:
      type: object
      required: [id]
      properties:
        id:
          type: string
          format: uuid

    SetRulesetDescriptionRequest:
      type: object
      required: [description]
      properties:
        description:
          type: string

    SetRulesetReferencesRequest:
      type: object
      required: [references]
      properties:
        references:
          type: array
          items:
            type: string
```

- [ ] **Step 4: Update `CreateCampaignRequest` to require `rulesetId`**

Change:
```yaml
    CreateCampaignRequest:
      type: object
      required: [name, gamemasterUserIds]
      properties:
        name:
          type: string
        gamemasterUserIds:
          type: array
          minItems: 1
          items:
            type: string
            format: uuid
```
to:
```yaml
    CreateCampaignRequest:
      type: object
      required: [name, rulesetId, gamemasterUserIds]
      properties:
        name:
          type: string
        rulesetId:
          type: string
          format: uuid
        gamemasterUserIds:
          type: array
          minItems: 1
          items:
            type: string
            format: uuid
```

- [ ] **Step 5: Regenerate and inspect**

Run: `go generate ./...`
Expected: `api/command/gen/server.gen.go` changes — new types
(`CreateRulesetRequest`, `RulesetCreatedResponse`, `SetRulesetDescriptionRequest`,
`SetRulesetReferencesRequest`), new `StrictServerInterface` methods
(`CreateRuleset`/`RenameRuleset`/`SetRulesetDescription`/`SetRulesetReferences`/
`ArchiveRuleset`), and `CreateCampaignRequest` gains a `RulesetId openapi_types.UUID` field.
Run `go generate ./...` a second time and confirm `git diff --exit-code -- api/command/gen`
shows no further change (idempotent codegen, matching CI's own check).

Inspect the generated `CreateRulesetRequest` type (`grep -A6 "type CreateRulesetRequest" api/command/gen/server.gen.go`)
to confirm the exact Go types for the optional `description`/`references` fields before
writing Step 6 — oapi-codegen typically generates an optional scalar as a pointer
(`Description *string`) and an optional array as a plain slice (`References []string`, nil
when absent), but confirm this against the actual generated code rather than assuming.

- [ ] **Step 6: Add Ruleset handlers to `internal/httpapi/command/server.go`**

Add the import `rulesetcmd "github.com/timadorus/platform/internal/command/ruleset"`, add a
`ruleset *rulesetcmd.Service` field to `Server` and a matching parameter to `NewServer` (append
at the end, after `objectService`):

```go
type Server struct {
	universe  *universecmd.Service
	user      *usercmd.Service
	campaign  *campaigncmd.Service
	entity    *entitycmd.Service
	character *charactercmd.Service
	object    *objectcmd.Service
	ruleset   *rulesetcmd.Service
}

func NewServer(
	universeService *universecmd.Service,
	userService *usercmd.Service,
	campaignService *campaigncmd.Service,
	entityService *entitycmd.Service,
	characterService *charactercmd.Service,
	objectService *objectcmd.Service,
	rulesetService *rulesetcmd.Service,
) *Server {
	return &Server{
		universe:  universeService,
		user:      userService,
		campaign:  campaignService,
		entity:    entityService,
		character: characterService,
		object:    objectService,
		ruleset:   rulesetService,
	}
}
```

Update the existing `CreateCampaign` handler to pass `RulesetId` through — find:
```go
	id, err := s.campaign.Create(ctx, campaigncmd.CreateCmd{
		UniverseID:        request.UniverseId,
		Name:              request.Body.Name,
		GamemasterUserIDs: request.Body.GamemasterUserIds,
	})
```
and change it to:
```go
	id, err := s.campaign.Create(ctx, campaigncmd.CreateCmd{
		UniverseID:        request.UniverseId,
		RulesetID:         request.Body.RulesetId,
		Name:              request.Body.Name,
		GamemasterUserIDs: request.Body.GamemasterUserIds,
	})
```

Add five new handler methods, following `CreateEntity`/`RenameEntity`/`ArchiveEntity`'s exact
structure (adjust the `description`/`references` handling per what Step 5 confirmed about the
generated optional-field types — the code below assumes the typical
`*string`/`[]string` shape):

```go
func (s *Server) CreateRuleset(ctx context.Context, request gen.CreateRulesetRequestObject) (gen.CreateRulesetResponseObject, error) {
	if request.Body == nil {
		return gen.CreateRuleset400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	description := ""
	if request.Body.Description != nil {
		description = *request.Body.Description
	}

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

	return gen.CreateRuleset201JSONResponse{Id: id}, nil
}

func (s *Server) RenameRuleset(ctx context.Context, request gen.RenameRulesetRequestObject) (gen.RenameRulesetResponseObject, error) {
	if request.Body == nil {
		return gen.RenameRuleset400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.ruleset.Rename(ctx, request.RulesetId, request.Body.Name); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.RenameRuleset404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.RenameRuleset409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		case 422:
			return gen.RenameRuleset422ApplicationProblemPlusJSONResponse{
				UnprocessableEntityApplicationProblemPlusJSONResponse: gen.UnprocessableEntityApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.RenameRuleset400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.RenameRuleset204Response{}, nil
}

func (s *Server) SetRulesetDescription(ctx context.Context, request gen.SetRulesetDescriptionRequestObject) (gen.SetRulesetDescriptionResponseObject, error) {
	if request.Body == nil {
		return gen.SetRulesetDescription400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.ruleset.SetDescription(ctx, request.RulesetId, request.Body.Description); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.SetRulesetDescription404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.SetRulesetDescription409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.SetRulesetDescription400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.SetRulesetDescription204Response{}, nil
}

func (s *Server) SetRulesetReferences(ctx context.Context, request gen.SetRulesetReferencesRequestObject) (gen.SetRulesetReferencesResponseObject, error) {
	if request.Body == nil {
		return gen.SetRulesetReferences400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	if err := s.ruleset.SetReferences(ctx, request.RulesetId, request.Body.References); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.SetRulesetReferences404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.SetRulesetReferences409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.SetRulesetReferences400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.SetRulesetReferences204Response{}, nil
}

func (s *Server) ArchiveRuleset(ctx context.Context, request gen.ArchiveRulesetRequestObject) (gen.ArchiveRulesetResponseObject, error) {
	if err := s.ruleset.Archive(ctx, request.RulesetId); err != nil {
		status, title := classify(err)
		return gen.ArchiveRuleset404ApplicationProblemPlusJSONResponse{
			NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(problem(status, title, err)),
		}, nil
	}
	return gen.ArchiveRuleset200Response{}, nil
}

// derefReferences returns references unchanged — References is a plain []string in the
// generated request type (optional arrays are nil-safe, unlike optional scalars), so no
// dereference is actually needed; this helper exists only so the call site above reads the
// same regardless of exactly how oapi-codegen shaped the field. Delete this helper and call
// request.Body.References directly if Step 5's inspection confirms it's a plain []string (the
// expected case).
func derefReferences(references []string) []string { return references }
```

- [ ] **Step 7: Add `ruleset.ErrArchived`/`ruleset.ErrNameRequired` to `classify()` in `internal/httpapi/command/errors.go`**

Add the import `"github.com/timadorus/platform/internal/domain/ruleset"` and two new cases,
placed with the other aggregates (e.g. after the `object.ErrNameRequired` case):

```go
	case errors.Is(err, ruleset.ErrArchived):
		return 409, "archived"
	case errors.Is(err, ruleset.ErrNameRequired):
		return 422, "validation_failed"
```

- [ ] **Step 8: Verify the command-side packages build**

Run: `go build ./internal/httpapi/command/... ./api/command/...`
Expected: clean. (`go build ./...` still won't fully succeed until Task 9 updates
`cmd/command-api/main.go` — expected.)

- [ ] **Step 9: Commit**

```bash
git add api/command/openapi.yaml api/command/gen/ internal/httpapi/command/
git commit -m "httpapi/command: add Ruleset endpoints, require rulesetId on campaign creation"
```

---

### Task 8: Query API — Ruleset + list-all endpoints + OpenAPI spec

**Files:**
- Modify: `api/query/openapi.yaml`
- Modify: `internal/httpapi/query/server.go`

**Interfaces:**
- Consumes: `rulesetquery.Repository` (Task 4); `userquery`/`universequery`'s new `ListAll`
  methods (added in this task, since they're one-line additions naturally grouped with the
  other list-all wiring rather than Task 4/6).
- Produces: generated `gen.GetRulesetRequestObject` etc., `Server`'s new
  `ruleset *rulesetquery.Repository` field — Task 9 (query-api main.go) passes it in.

- [ ] **Step 1: Add `ListAll` to `internal/query/user/repository.go`**

```go
// ListAll returns every non-archived User, ordered by name.
func (r *Repository) ListAll(ctx context.Context) ([]User, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, is_archived FROM users_read_model WHERE is_archived = false ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("query/user: list all: %w", err)
	}
	defer rows.Close()

	var users []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Name, &u.IsArchived); err != nil {
			return nil, fmt.Errorf("query/user: scan row: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/user: iterate rows: %w", err)
	}
	return users, nil
}
```

- [ ] **Step 2: Add `ListAll` to `internal/query/universe/repository.go`**

```go
// ListAll returns every non-archived Universe, ordered by name.
func (r *Repository) ListAll(ctx context.Context) ([]Universe, error) {
	rows, err := r.pool.Query(ctx,
		`SELECT id, name, is_archived FROM universes_read_model WHERE is_archived = false ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("query/universe: list all: %w", err)
	}
	defer rows.Close()

	var universes []Universe
	for rows.Next() {
		var u Universe
		if err := rows.Scan(&u.ID, &u.Name, &u.IsArchived); err != nil {
			return nil, fmt.Errorf("query/universe: scan row: %w", err)
		}
		universes = append(universes, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query/universe: iterate rows: %w", err)
	}
	return universes, nil
}
```

- [ ] **Step 3: Add paths and schemas to `api/query/openapi.yaml`**

Insert immediately before the `components:` line:

```yaml
  /rulesets:
    get:
      operationId: listRulesets
      summary: List non-archived Rulesets.
      responses:
        "200":
          description: Rulesets.
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/Ruleset"

  /rulesets/{rulesetId}:
    get:
      operationId: getRuleset
      summary: Get a Ruleset by id.
      parameters:
        - $ref: "#/components/parameters/RulesetId"
      responses:
        "200":
          description: The Ruleset.
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/Ruleset"
        "404":
          $ref: "#/components/responses/NotFound"

  /users:
    get:
      operationId: listUsers
      summary: List non-archived Users.
      responses:
        "200":
          description: Users.
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/User"

  /universes:
    get:
      operationId: listUniverses
      summary: List non-archived Universes.
      responses:
        "200":
          description: Universes.
          content:
            application/json:
              schema:
                type: array
                items:
                  $ref: "#/components/schemas/Universe"
```

Add the `RulesetId` parameter to `components/parameters` (identical shape to the command
spec's):

```yaml
    RulesetId:
      name: rulesetId
      in: path
      required: true
      schema:
        type: string
        format: uuid
```

Add the `Ruleset` schema to `components/schemas`:

```yaml
    Ruleset:
      type: object
      required: [id, name, description, references, isArchived]
      properties:
        id:
          type: string
          format: uuid
        name:
          type: string
        description:
          type: string
        references:
          type: array
          items:
            type: string
        isArchived:
          type: boolean
```

Update `Campaign` to require `rulesetId`:
```yaml
    Campaign:
      type: object
      required: [id, name, universeId, rulesetId, isArchived]
      properties:
        id:
          type: string
          format: uuid
        name:
          type: string
        universeId:
          type: string
          format: uuid
        rulesetId:
          type: string
          format: uuid
        isArchived:
          type: boolean
```

- [ ] **Step 4: Regenerate**

Run: `go generate ./...`
Expected: `api/query/gen/server.gen.go` gains `Ruleset` type, `ListRulesets`/`GetRuleset`/
`ListUsers`/`ListUniverses` interface methods, and `Campaign` gains `RulesetId`. Run
`go generate ./...` a second time and confirm no further diff.

- [ ] **Step 5: Add handlers to `internal/httpapi/query/server.go`**

Add the import `rulesetquery "github.com/timadorus/platform/internal/query/ruleset"`, add a
`ruleset *rulesetquery.Repository` field to `Server` and matching `NewServer` parameter
(append at the end):

```go
type Server struct {
	universe  *universequery.Repository
	user      *userquery.Repository
	campaign  *campaignquery.Repository
	entity    *entityquery.Repository
	character *characterquery.Repository
	object    *objectquery.Repository
	ruleset   *rulesetquery.Repository
}

func NewServer(
	universeRepo *universequery.Repository,
	userRepo *userquery.Repository,
	campaignRepo *campaignquery.Repository,
	entityRepo *entityquery.Repository,
	characterRepo *characterquery.Repository,
	objectRepo *objectquery.Repository,
	rulesetRepo *rulesetquery.Repository,
) *Server {
	return &Server{
		universe:  universeRepo,
		user:      userRepo,
		campaign:  campaignRepo,
		entity:    entityRepo,
		character: characterRepo,
		object:    objectRepo,
		ruleset:   rulesetRepo,
	}
}
```

Update `GetCampaign`'s response construction to include `RulesetId` — find the line
constructing `gen.GetCampaign200JSONResponse{...}` (or equivalently-named response type for
the Campaign getter) and add `RulesetId: c.RulesetID` alongside the existing fields.

Add four new handlers, following `GetUser`'s exact structure for the two `Get*` methods and a
plain no-path-param pattern (like `ListEntitiesByUniverse` minus the parameter) for the two
`List*` methods:

```go
func (s *Server) GetRuleset(ctx context.Context, request gen.GetRulesetRequestObject) (gen.GetRulesetResponseObject, error) {
	r, err := s.ruleset.Get(ctx, request.RulesetId)
	if err != nil {
		if errors.Is(err, rulesetquery.ErrNotFound) {
			return gen.GetRuleset404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(notFound(err)),
			}, nil
		}
		return nil, err
	}
	return gen.GetRuleset200JSONResponse{
		Id:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		References:  r.References,
		IsArchived:  r.IsArchived,
	}, nil
}

func (s *Server) ListRulesets(ctx context.Context, request gen.ListRulesetsRequestObject) (gen.ListRulesetsResponseObject, error) {
	rulesets, err := s.ruleset.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Ruleset, len(rulesets))
	for i, r := range rulesets {
		out[i] = gen.Ruleset{Id: r.ID, Name: r.Name, Description: r.Description, References: r.References, IsArchived: r.IsArchived}
	}
	return gen.ListRulesets200JSONResponse(out), nil
}

func (s *Server) ListUsers(ctx context.Context, request gen.ListUsersRequestObject) (gen.ListUsersResponseObject, error) {
	users, err := s.user.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.User, len(users))
	for i, u := range users {
		out[i] = gen.User{Id: u.ID, Name: u.Name, IsArchived: u.IsArchived}
	}
	return gen.ListUsers200JSONResponse(out), nil
}

func (s *Server) ListUniverses(ctx context.Context, request gen.ListUniversesRequestObject) (gen.ListUniversesResponseObject, error) {
	universes, err := s.universe.ListAll(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]gen.Universe, len(universes))
	for i, u := range universes {
		out[i] = gen.Universe{Id: u.ID, Name: u.Name, IsArchived: u.IsArchived}
	}
	return gen.ListUniverses200JSONResponse(out), nil
}
```

- [ ] **Step 6: Verify the query-side packages build**

Run: `go build ./internal/query/... ./internal/httpapi/query/... ./api/query/...`
Expected: clean. (`go build ./...` still won't fully succeed until Task 9 — expected.)

- [ ] **Step 7: Commit**

```bash
git add api/query/openapi.yaml api/query/gen/ internal/query/user/ internal/query/universe/ internal/httpapi/query/
git commit -m "httpapi/query,query: add Ruleset endpoints and list-all for parentless aggregates"
```

---

### Task 9: Wire all three binaries

**Files:**
- Modify: `cmd/command-api/main.go`
- Modify: `cmd/query-api/main.go`
- Modify: `cmd/projector/main.go`

**Interfaces:**
- Consumes: everything from Tasks 1-8.
- Produces: a fully building, fully wired module — the first point since Task 6 where
  `go build ./...` succeeds end to end.

- [ ] **Step 1: Modify `cmd/command-api/main.go`**

Add imports:
```go
	rulesetcmd "github.com/timadorus/platform/internal/command/ruleset"
	"github.com/timadorus/platform/internal/domain/ruleset"
	rulesetevents "github.com/timadorus/platform/internal/domain/ruleset/events"
```

Register the new events (alongside the existing `Register` calls):
```go
	rulesetevents.Register(registry)
```

Construct the Ruleset repo/service **before** the Campaign repo/service (Campaign now needs
`rulesetRepo`):
```go
	rulesetRepo := eventsourcing.NewRepository(store, ruleset.AggregateType, func() *ruleset.Ruleset {
		return &ruleset.Ruleset{}
	})
	rulesetService := rulesetcmd.NewService(rulesetRepo)

	campaignRepo := eventsourcing.NewRepository(store, campaign.AggregateType, func() *campaign.Campaign {
		return &campaign.Campaign{}
	})
	campaignService := campaigncmd.NewService(campaignRepo, universeRepo, userRepo, rulesetRepo)
```

Update the `NewServer` call to append `rulesetService`:
```go
	server := httpcommand.NewServer(universeService, userService, campaignService, entityService, characterService, objectService, rulesetService)
```

- [ ] **Step 2: Modify `cmd/query-api/main.go`**

Add import: `rulesetquery "github.com/timadorus/platform/internal/query/ruleset"`

```go
	rulesetRepo := rulesetquery.NewRepository(pool)
	server := httpquery.NewServer(universeRepo, userRepo, campaignRepo, entityRepo, characterRepo, objectRepo, rulesetRepo)
```

- [ ] **Step 3: Modify `cmd/projector/main.go`**

Add import: `rulesetprojection "github.com/timadorus/platform/internal/projection/ruleset"`

```go
	projectors := []projection.Projector{
		universeprojection.NewProjector(),
		userprojection.NewProjector(),
		campaignprojection.NewProjector(),
		entityprojection.NewProjector(),
		characterprojection.NewProjector(),
		objectprojection.NewProjector(),
		rulesetprojection.NewProjector(),
	}
```

- [ ] **Step 4: Build and vet the whole module**

Run: `go build ./... && go vet ./...`
Expected: clean — this is the first fully-green build since Task 5 started the breaking
change.

- [ ] **Step 5: Run the full test suite**

Run: `go test ./...`
Expected: all packages `ok` (including `internal/eventstore/postgres` and
`internal/projection/universe`'s testcontainers-backed tests, and the new
`internal/domain/ruleset` and updated `internal/domain/campaign` tests).

- [ ] **Step 6: Commit**

```bash
git add cmd/
git commit -m "cmd: wire Ruleset into command-api, query-api, and projector"
```

---

### Task 10: CLI — `ruleset.go` + campaign/user/universe updates

**Files:**
- Create: `internal/cliapp/ruleset.go`
- Modify: `internal/cliapp/campaign.go`
- Modify: `internal/cliapp/user.go`
- Modify: `internal/cliapp/universe.go`
- Modify: `internal/cliapp/root.go`

**Interfaces:**
- Consumes: the now-live command/query APIs (Task 9).
- Produces: `timadorusctl create/rename/archive/get/list ruleset`,
  `timadorusctl set description/references`, updated `create campaign` (new `rulesetId` arg),
  new `list user`/`list universe`.

- [ ] **Step 1: Write `internal/cliapp/ruleset.go`**

```go
package cliapp

import (
	"fmt"

	"github.com/spf13/cobra"
)

// registerRulesetCommands wires the Ruleset aggregate's CLI surface. Ruleset has no parent
// (plan §2) — every id is always explicit, and `list ruleset` is bare, matching User's own
// no-parent shape. Unlike User/Entity/Object, Ruleset has two additional mutable fields
// beyond name, each getting its own `set` subcommand alongside Character's `set player`.
func registerRulesetCommands(a *App) {
	a.createCmd.AddCommand(&cobra.Command{
		Use:   "ruleset <name> <description> [reference...]",
		Short: "Create a new Ruleset",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", "/rulesets", map[string]any{
				"name":        args[0],
				"description": args[1],
				"references":  args[2:],
			})
		},
	})

	a.renameCmd.AddCommand(&cobra.Command{
		Use:   "ruleset <rulesetId> <name>",
		Short: "Rename a Ruleset",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PATCH", "/rulesets/"+args[0], map[string]any{"name": args[1]})
		},
	})

	a.archiveCmd.AddCommand(&cobra.Command{
		Use:   "ruleset <rulesetId>",
		Short: "Archive a Ruleset (idempotent)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("POST", fmt.Sprintf("/rulesets/%s/archive", args[0]), nil)
		},
	})

	a.getCmd.AddCommand(&cobra.Command{
		Use:   "ruleset <rulesetId>",
		Short: "Get a Ruleset by id",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/rulesets/" + args[0])
		},
	})

	a.listCmd.AddCommand(&cobra.Command{
		Use:   "ruleset",
		Short: "List non-archived Rulesets",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/rulesets")
		},
	})

	a.setCmd.AddCommand(&cobra.Command{
		Use:   "description <rulesetId> <description>",
		Short: "Replace a Ruleset's description",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PUT", "/rulesets/"+args[0]+"/description", map[string]any{"description": args[1]})
		},
	})

	a.setCmd.AddCommand(&cobra.Command{
		Use:   "references <rulesetId> [reference...]",
		Short: "Replace a Ruleset's references list",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Command("PUT", "/rulesets/"+args[0]+"/references", map[string]any{"references": args[1:]})
		},
	})
}
```

- [ ] **Step 2: Modify `internal/cliapp/campaign.go`'s `create campaign` command**

Change:
```go
	createCampaignCmd := &cobra.Command{
		Use:   "campaign <name> <gamemasterUserId>...",
		Short: "Create a new Campaign under a Universe (POST /universes/{universeId}/campaigns)",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := a.client()
			if err != nil {
				return err
			}
			flagValue, _ := cmd.Flags().GetString(universeFlag)
			universeID, err := resolveUniverseID(&cfg, flagValue)
			if err != nil {
				return err
			}
			return client.Command("POST", "/universes/"+universeID+"/campaigns", map[string]any{
				"name":              args[0],
				"gamemasterUserIds": args[1:],
			})
		},
	}
```
to:
```go
	createCampaignCmd := &cobra.Command{
		Use:   "campaign <name> <rulesetId> <gamemasterUserId>...",
		Short: "Create a new Campaign under a Universe (POST /universes/{universeId}/campaigns)",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, cfg, err := a.client()
			if err != nil {
				return err
			}
			flagValue, _ := cmd.Flags().GetString(universeFlag)
			universeID, err := resolveUniverseID(&cfg, flagValue)
			if err != nil {
				return err
			}
			return client.Command("POST", "/universes/"+universeID+"/campaigns", map[string]any{
				"name":              args[0],
				"rulesetId":         args[1],
				"gamemasterUserIds": args[2:],
			})
		},
	}
```

- [ ] **Step 3: Modify `internal/cliapp/user.go`** — add a `list user` command at the end of
  `registerUserCommands`, right after the existing `a.getCmd.AddCommand(...)` block:

```go
	a.listCmd.AddCommand(&cobra.Command{
		Use:   "user",
		Short: "List non-archived Users",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/users")
		},
	})
```

- [ ] **Step 4: Modify `internal/cliapp/universe.go`** — add a `list universe` command at the
  end of `registerUniverseCommands`, after the existing `listCreatorCmd` block:

```go
	a.listCmd.AddCommand(&cobra.Command{
		Use:   "universe",
		Short: "List non-archived Universes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			return client.Query("/universes")
		},
	})
```

- [ ] **Step 5: Modify `internal/cliapp/root.go`'s `Execute()`**

Add `registerRulesetCommands(app)` to the registration list:
```go
	registerUserCommands(app)
	registerUniverseCommands(app)
	registerCampaignCommands(app)
	registerEntityCommands(app)
	registerObjectCommands(app)
	registerCharacterCommands(app)
	registerRulesetCommands(app)
```

- [ ] **Step 6: Build and smoke-test the CLI**

Run: `go build ./cmd/timadorusctl/... && go vet ./...`
Expected: clean.

Run (help output only, no live server needed):
```bash
go run ./cmd/timadorusctl create ruleset --help
go run ./cmd/timadorusctl create campaign --help
go run ./cmd/timadorusctl list ruleset --help
go run ./cmd/timadorusctl list user --help
go run ./cmd/timadorusctl list universe --help
go run ./cmd/timadorusctl set description --help
go run ./cmd/timadorusctl set references --help
```
Expected: each prints correct usage strings matching what was written above (e.g.
`create campaign <name> <rulesetId> <gamemasterUserId>...`), no errors.

- [ ] **Step 7: Commit**

```bash
git add internal/cliapp/
git commit -m "cliapp: add ruleset commands, list user/universe, require rulesetId on create campaign"
```

---

### Task 11: `docs/PLAN.md` updates

**Files:**
- Modify: `docs/PLAN.md`

**Interfaces:** none — documentation only.

- [ ] **Step 1: §2 Domain Hierarchy — add Ruleset, note Campaign's new reference**

Find the domain hierarchy diagram (starts `## 2. Domain Hierarchy`) and change:
```
Campaign
 └── belongs to exactly one Universe (immutable parent ref)
 └── Gamemasters: Set<User>, non-empty, mutable (add/remove)
```
to:
```
Ruleset (independent, top-level — referenced by Campaign, never owned by a Universe)
 └── description: string, mutable via a dedicated command
 └── references: []string, mutable via a dedicated command (whole-list replace)

Campaign
 └── belongs to exactly one Universe (immutable parent ref)
 └── references exactly one Ruleset (immutable — set at creation, never changeable; create a
     new Campaign to use a different Ruleset)
 └── Gamemasters: Set<User>, non-empty, mutable (add/remove)
```

- [ ] **Step 2: §4.3 Cross-aggregate reference validation — add the Ruleset check to the pseudocode**

In the `CampaignService`/`CreateCampaign` pseudocode block, add a `rulesets` field and a
validation block mirroring the existing `universe` check, immediately after it:
```go
type CampaignService struct {
    campaigns eventsourcing.Repository[*campaign.Campaign]
    universes eventsourcing.Repository[*universe.Universe] // existence-check only
    rulesets  eventsourcing.Repository[*ruleset.Ruleset]     // existence-check only
    users     eventsourcing.Repository[*user.User]          // existence-check only
}
```
and, in `CreateCampaign`, after the existing `universe.IsArchived()` check:
```go
    rs, err := s.rulesets.Load(ctx, cmd.RulesetID)
    if err != nil {
        return uuid.Nil, fmt.Errorf("%w: ruleset %s", ErrParentNotFound, cmd.RulesetID)
    }
    if rs.IsArchived() {
        return uuid.Nil, fmt.Errorf("%w: ruleset %s", ErrParentArchived, cmd.RulesetID)
    }
```

- [ ] **Step 3: §4.5 Event catalog — add Ruleset row, update Campaign's row**

Change the Campaign row:
```
| Campaign | `CampaignCreated{ID, Name, UniverseID, GamemasterUserIDs}`, `CampaignRenamed{Name}`, `GamemasterAdded{UserID}`, `GamemasterRemoved{UserID}`, `CampaignArchived{}` |
```
to:
```
| Campaign | `CampaignCreated{ID, Name, UniverseID, RulesetID, GamemasterUserIDs}`, `CampaignRenamed{Name}`, `GamemasterAdded{UserID}`, `GamemasterRemoved{UserID}`, `CampaignArchived{}` |
```
and add a new row (e.g. after the Character row):
```
| Ruleset | `RulesetCreated{ID, Name, Description, References}`, `RulesetRenamed{Name}`, `RulesetDescriptionChanged{Description}`, `RulesetReferencesChanged{References}`, `RulesetArchived{}` |
```

- [ ] **Step 4: §8 Command API — add Ruleset paths, update Campaign's create body**

Change:
```
POST   /universes/{universeId}/campaigns             { name, gamemasterUserIds }
```
to:
```
POST   /universes/{universeId}/campaigns             { name, rulesetId, gamemasterUserIds }
```
Add, after the Object block:
```
POST   /rulesets                                      { name, description?, references? }
PATCH  /rulesets/{rulesetId}
PUT    /rulesets/{rulesetId}/description               { description }
PUT    /rulesets/{rulesetId}/references                { references }
POST   /rulesets/{rulesetId}/archive
```

- [ ] **Step 5: §9 Query API — add the four new list-all/get paths**

Change:
```
GET /users/{userId}
GET /universes/{universeId}
```
to:
```
GET /users
GET /users/{userId}
GET /universes
GET /universes/{universeId}
```
and add, after the existing `GET /objects/{objectId}` line:
```
GET /rulesets
GET /rulesets/{rulesetId}
```

- [ ] **Step 6: §14 CLI reference — note Ruleset's command set**

Add a short paragraph after the existing per-aggregate command reference (wherever §14
enumerates the six existing aggregate types' CLI surfaces) noting that Ruleset follows the
Entity/Object shape for create/rename/archive/get, is parentless like User (bare `list
ruleset`, no scoping flag), and — being the first aggregate with mutable fields beyond name —
introduces `set description`/`set references` alongside Character's existing `set player`.
Also note the new bare `list user`/`list universe` commands added alongside it.

- [ ] **Step 7: Commit**

```bash
git add docs/PLAN.md
git commit -m "docs: update PLAN.md for the Ruleset aggregate"
```

---

### Task 12: e2e test suite fix + full real verification

**Files:**
- Modify: `test/e2e/e2e_test.go`

**Interfaces:**
- Consumes: everything from Tasks 1-11.

- [ ] **Step 1: Update `test/e2e/e2e_test.go` to create a Ruleset before the Campaign**

`CreateCampaignRequest` now requires `rulesetId`, so the existing round-trip test breaks
without this fix. Add a Ruleset creation step right after the User creation and before the
Universe creation (or anywhere before the Campaign creation step — placement relative to
Universe doesn't matter since they're independent), and thread its id into the Campaign
create call:

```go
		rulesetName := "e2e-ruleset"
		var rulesetResp commandgen.RulesetCreatedResponse
		resp, err = doJSON(http.MethodPost, env.CommandAPIBaseURL+"/rulesets", env.BearerToken,
			commandgen.CreateRulesetRequest{Name: rulesetName}, &rulesetResp)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusCreated))
```

and change the existing Campaign creation call from:
```go
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/campaigns", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateCampaignRequest{Name: campaignName, GamemasterUserIds: []uuid.UUID{user.Id}}, &campaign)
```
to:
```go
		resp, err = doJSON(http.MethodPost, fmt.Sprintf("%s/universes/%s/campaigns", env.CommandAPIBaseURL, universe.Id), env.BearerToken,
			commandgen.CreateCampaignRequest{Name: campaignName, RulesetId: rulesetResp.Id, GamemasterUserIds: []uuid.UUID{user.Id}}, &campaign)
```

Add a corresponding `Eventually` read-back check for the Ruleset (after the existing User
read-back check, matching its exact style):
```go
		Eventually(func(g Gomega) {
			var got querygen.Ruleset
			resp, err := doJSON(http.MethodGet, fmt.Sprintf("%s/rulesets/%s", env.QueryAPIBaseURL, rulesetResp.Id), env.BearerToken, nil, &got)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(resp.StatusCode).To(Equal(http.StatusOK))
			g.Expect(got.Name).To(Equal(rulesetName))
			g.Expect(got.IsArchived).To(BeFalse())
		}, time.Minute, time.Second).Should(Succeed())
```

Also update the existing Campaign read-back `Eventually` block to additionally assert
`g.Expect(got.RulesetId).To(Equal(rulesetResp.Id))`, alongside its existing `UniverseId`
assertion.

- [ ] **Step 2: Run the full local test suite**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: clean, matching Task 9's Step 5 result (this task doesn't change non-e2e code).

- [ ] **Step 3: Run `make test-e2e` for real**

```bash
make test-e2e
```
Expected: `SUCCESS! -- 1 Passed | 0 Failed`, now covering all seven aggregate types
(User, Universe, Campaign, Entity, Object, Character, Ruleset) in the one round-trip test —
this is real evidence the entire feature works end to end against a live Kubernetes cluster,
not just local Postgres/NATS.

If it fails, debug via the same process established in the e2e test tool's own plan (check
`kubectl logs` for command-api/query-api/projector pods, confirm the migration Job applied the
new `projection_ruleset`/`0002_campaign_ruleset_id` migrations) before concluding anything —
this is exactly the kind of cross-layer integration bug a real cluster run is meant to catch.

- [ ] **Step 4: Commit**

```bash
git add test/e2e/e2e_test.go
git commit -m "test/e2e: create a Ruleset before Campaign in the aggregate round-trip test"
```

---

## Self-review notes (fixed inline before handoff)

- Confirmed every template file (`internal/domain/user`, `internal/domain/entity`,
  `internal/command/campaign/service.go`, `internal/query/entity/repository.go`,
  `internal/httpapi/{command,query}/server.go`, both `openapi.yaml` files, all three
  `cmd/*/main.go` files, `internal/cliapp/{root,user,universe,campaign,character}.go`) by
  reading the actual current file contents directly — no pattern was guessed from memory.
- Confirmed via a real `docker exec ... psql` test that `references` is a reserved SQL keyword
  and would break as an unquoted column name; the schema in Task 3 uses `reference_urls`
  instead, with the discrepancy from the Go/JSON field name called out explicitly.
- Confirmed `Character.New`'s actual parameter order (`campaignID, entityID, playerUserID,
  name`) directly rather than trusting an earlier paraphrase, before using it as the
  precedent for `Campaign.New`'s new parameter placement.
- Task 5 explicitly checks (Step 5) for any other caller of `campaign.New` beyond the two
  known call sites, since a breaking constructor signature change is exactly the kind of
  edit that silently breaks an overlooked caller.
- Task 6 explicitly does NOT run `go build ./...` (only the specific changed packages),
  and Task 7/8 do the same, since the module is deliberately left non-building between
  Tasks 5 and 9 (Campaign's new dependency chain isn't fully wired until Task 9) — flagging
  this loudly in each task's verification step so an implementer doesn't mistake it for a
  regression to chase down early.
- Task 7 flags the oapi-codegen optional-field-type uncertainty explicitly (Step 5) rather
  than asserting a type shape unverified against the actual generated code, and provides a
  fallback (`derefReferences` helper) that works whether or not the assumption holds.
- Added Task 12's requirement to update the e2e suite as its own task rather than silently
  folding it into Task 9, since it's the one place outside this feature's own new/modified
  files where the breaking `rulesetId`-required change has an external, easy-to-miss
  consequence.
