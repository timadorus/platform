# Character Action Trigger + timadorus-engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `PUT /characters/{characterId}/action` endpoint that raises a trigger event
without mutating the Character, and a new `timadorus-engine` binary that reacts to that event by
appending a timestamp into the Character's `info` field — but only when the Character's
Campaign's Ruleset is named "Timadorus" (case-insensitively) — caching that ruleset-name lookup
per campaign since it never changes.

**Architecture:** A command-side trigger (event with a no-op `Apply`) plus a brand-new
process-manager-shaped consumer that reuses the existing `projection.Projector` interface
unmodified but lives in its own binary, since it needs full write-side imports the `projector`
binary is documented to never have.

**Tech Stack:** Go, oapi-codegen (strict-server), Watermill/NATS JetStream (existing
`internal/bus`/`internal/projection`), pgx, testcontainers-go, Helm, Docker.

## Global Constraints

- Spec: `docs/superpowers/specs/2026-08-23-character-action-timadorus-engine-design.md`.
- `ActionRequested`'s `Apply` case is an explicit no-op (not omitted from the switch) — self-
  documenting, matches the design's "does not directly change the aggregate" requirement exactly.
- The `action` endpoint's request body is `type: object` with no `properties` — oapi-codegen
  generates this as `map[string]interface{}` (confirmed by a real trial generation during
  planning). No `422` response — there is no content to fail domain validation.
- `timadorus-engine` reuses `internal/projection.Projector`/`Router`/checkpoint machinery
  unmodified — no changes to `internal/projection/*`.
- The `info` shape is `{"actions": ["<RFC3339 timestamp>", ...]}`. When merging in a new
  timestamp, parse the existing `info` as a generic `map[string]any` and only touch the
  `"actions"` key — never discard other top-level keys a human might have set via the
  already-existing `PUT .../info` endpoint. If the existing `info` isn't valid JSON or isn't a
  JSON object at all (allowed today since `SetInfo` accepts any string), start from a fresh empty
  map rather than erroring — an unparseable existing value would otherwise retry forever and
  never self-heal, since retrying does not fix malformed content.
- The timestamp appended is the `ActionRequested` event's own `OccurredAt` (recorded at command
  time), never wall-clock time at engine-processing time — keeps replay deterministic.
- `timadorus-engine`'s HTTP address env var is `TIMADORUS_ENGINE_ADDR`, default `:8084`.
- New Helm values section is `timadorusEngine:` (camelCase, matching `commandApi`/`queryApi`'s
  convention), image repository `timadorus/timadorus-engine`, `containerPort: 8084`.
- `imageValuesKey("timadorus-engine")` returns `"timadorusEngine"` (mirrors the existing
  `"migrate"` → `"migration"` renaming case, not a straight passthrough).
- No new migrations anywhere — `timadorus-engine` reads existing read-model tables and reuses the
  existing shared `projection_checkpoints` table, keyed by `Name() = "timadorus-engine"`.

---

### Task 1: Character `action` trigger — domain, command, HTTP, OpenAPI, CLI

**Files:**
- Modify: `internal/domain/character/events/events.go`
- Modify: `internal/domain/character/character.go`
- Modify: `internal/domain/character/character_test.go`
- Modify: `internal/command/character/service.go`
- Modify: `api/command/openapi.yaml`
- Modify: `api/command/gen/server.gen.go` (regenerated, not hand-edited)
- Modify: `internal/httpapi/command/server.go`
- Modify: `internal/cliapp/root.go`
- Modify: `internal/cliapp/character.go`

**Interfaces:**
- Produces: `events.TypeActionRequested`, `events.ActionRequested{Payload string, OccurredAt time.Time}`.
- Produces: `character.Character.RequestAction(payload string) error`.
- Produces: `charactercmd.Service.RequestAction(ctx context.Context, id uuid.UUID, payload string) error`.
- Produces: `PUT /characters/{characterId}/action`, request body `map[string]interface{}` (any JSON object), `204`/`400`/`404`/`409`.
- Consumes (Task 2): none — Task 2 only *reads* `events.TypeActionRequested`/`events.ActionRequested`, defined here.

- [ ] **Step 1: Add the event to `internal/domain/character/events/events.go`**

Change the `const` block:
```go
const (
	TypeCharacterCreated  = "character.created.v1"
	TypeCharacterRenamed  = "character.renamed.v1"
	TypePlayerChanged     = "character.player_changed.v1"
	TypeInfoChanged       = "character.info_changed.v1"
	TypeActionRequested   = "character.action_requested.v1"
	TypeCharacterArchived = "character.archived.v1"
)
```

Add, after the `InfoChanged` type/method (before `type CharacterArchived struct`):
```go
// ActionRequested is a pure trigger: raised by Character.RequestAction, applied as a no-op
// (see Character.Apply), with the actual effect, if any, decided later and asynchronously by
// timadorus-engine (internal/engine/timadorus) — never by the Character aggregate itself.
// Payload is the PUT /characters/{id}/action request body, re-marshaled to a canonical JSON
// string (same opaque-string representation Info already uses), unused by any command here.
type ActionRequested struct {
	Payload    string    `json:"payload"`
	OccurredAt time.Time `json:"occurredAt"`
}

func (ActionRequested) EventType() string { return TypeActionRequested }
```

Add to `Register`:
```go
func Register(reg *eventsourcing.Registry) {
	reg.Register(TypeCharacterCreated, func() eventsourcing.Event { return &CharacterCreated{} })
	reg.Register(TypeCharacterRenamed, func() eventsourcing.Event { return &CharacterRenamed{} })
	reg.Register(TypePlayerChanged, func() eventsourcing.Event { return &PlayerChanged{} })
	reg.Register(TypeInfoChanged, func() eventsourcing.Event { return &InfoChanged{} })
	reg.Register(TypeActionRequested, func() eventsourcing.Event { return &ActionRequested{} })
	reg.Register(TypeCharacterArchived, func() eventsourcing.Event { return &CharacterArchived{} })
}
```

- [ ] **Step 2: Add `RequestAction` to `internal/domain/character/character.go`**

Add, after `SetInfo` (before the `Archive` method):
```go
// RequestAction raises ActionRequested without mutating any aggregate field (see Apply below) —
// the actual effect, if any, happens later and asynchronously in timadorus-engine, and only
// conditionally on the Character's Campaign's Ruleset (see that package's own docs). Guarded by
// the same archived check every other mutating command uses, since a request against an
// archived Character shouldn't be accepted even though it has no direct effect here.
func (c *Character) RequestAction(payload string) error {
	if c.archived {
		return ErrArchived
	}
	c.raise(&events.ActionRequested{Payload: payload, OccurredAt: time.Now().UTC()})
	return nil
}
```

Add a case to `Apply`'s switch, after `case *events.InfoChanged:`:
```go
	case *events.ActionRequested:
		// Intentionally a no-op — see RequestAction's doc comment. An explicit case (rather
		// than falling through to no case at all) keeps this switch exhaustive and
		// self-documenting: a future reader shouldn't have to wonder whether it was forgotten.
```

- [ ] **Step 3: Add domain tests to `internal/domain/character/character_test.go`**

Add a new test function:
```go
func TestRequestAction(t *testing.T) {
	c, err := character.New(uuid.New(), uuid.New(), uuid.New(), "Elminster")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	c.ClearPending()

	nameBefore, campaignBefore, entityBefore, playerBefore, infoBefore := c.Name(), c.CampaignID(), c.EntityID(), c.PlayerUserID(), c.Info()

	if err := c.RequestAction(`{"foo":"bar"}`); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(c.Pending()); got != 1 {
		t.Fatalf("got %d pending events, want 1", got)
	}
	if c.Name() != nameBefore || c.CampaignID() != campaignBefore || c.EntityID() != entityBefore ||
		c.PlayerUserID() != playerBefore || c.Info() != infoBefore {
		t.Fatalf("RequestAction must not mutate any field, but at least one changed")
	}
}
```

Add to `TestArchive`'s guard-list (alongside the existing `Rename`/`SetPlayer`/`SetInfo` checks):
```go
	if err := c.RequestAction("{}"); err != character.ErrArchived {
		t.Fatalf("got %v, want ErrArchived", err)
	}
```

- [ ] **Step 4: Run domain tests**

Run: `go test ./internal/domain/character/... -v`
Expected: all tests pass, including the two new ones.

- [ ] **Step 5: Add `RequestAction` to `internal/command/character/service.go`**

Add, after `SetInfo`:
```go
func (s *Service) RequestAction(ctx context.Context, id uuid.UUID, payload string) error {
	c, err := s.characters.Load(ctx, id)
	if err != nil {
		return err
	}
	if err := c.RequestAction(payload); err != nil {
		return err
	}
	return s.characters.Save(ctx, c)
}
```

- [ ] **Step 6: Add the OpenAPI path to `api/command/openapi.yaml`**

Add, immediately after the existing `/characters/{characterId}/info` block (before
`/universes/{universeId}/objects:`):
```yaml
  /characters/{characterId}/action:
    put:
      operationId: requestCharacterAction
      summary: >
        Request an action on a Character. Does not change the Character directly — raises an
        ActionRequested event that timadorus-engine may or may not act on, asynchronously.
      parameters:
        - $ref: "#/components/parameters/CharacterId"
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
      responses:
        "204":
          description: Action requested.
        "400":
          $ref: "#/components/responses/BadRequest"
        "404":
          $ref: "#/components/responses/NotFound"
        "409":
          $ref: "#/components/responses/Conflict"
```

- [ ] **Step 7: Regenerate the command API's generated server code**

Run: `go generate ./api/command/...`
Expected: clean exit, `api/command/gen/server.gen.go` gains
`RequestCharacterActionRequestObject`/`RequestCharacterActionResponseObject`/
`RequestCharacterAction200.../400.../404.../409...` response types, a `RequestCharacterAction`
method on `StrictServerInterface`, and a route registration
`.Methods(http.MethodPut)` for `/characters/{characterId}/action`. Confirm with:
`grep -n "RequestCharacterAction" api/command/gen/server.gen.go` — do not hand-edit this file.

- [ ] **Step 8: Add the handler to `internal/httpapi/command/server.go`**

Add `"encoding/json"` to the import block (alongside the existing `"context"`/`"errors"`).

Add, after `SetCharacterInfo`:
```go
func (s *Server) RequestCharacterAction(ctx context.Context, request gen.RequestCharacterActionRequestObject) (gen.RequestCharacterActionResponseObject, error) {
	if request.Body == nil {
		return gen.RequestCharacterAction400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", errMissingBody)),
		}, nil
	}

	payload, err := json.Marshal(*request.Body)
	if err != nil {
		return gen.RequestCharacterAction400ApplicationProblemPlusJSONResponse{
			BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(problem(400, "bad_request", err)),
		}, nil
	}

	if err := s.character.RequestAction(ctx, request.CharacterId, string(payload)); err != nil {
		status, title := classify(err)
		p := problem(status, title, err)
		switch status {
		case 404:
			return gen.RequestCharacterAction404ApplicationProblemPlusJSONResponse{
				NotFoundApplicationProblemPlusJSONResponse: gen.NotFoundApplicationProblemPlusJSONResponse(p),
			}, nil
		case 409:
			return gen.RequestCharacterAction409ApplicationProblemPlusJSONResponse{
				ConflictApplicationProblemPlusJSONResponse: gen.ConflictApplicationProblemPlusJSONResponse(p),
			}, nil
		default:
			return gen.RequestCharacterAction400ApplicationProblemPlusJSONResponse{
				BadRequestApplicationProblemPlusJSONResponse: gen.BadRequestApplicationProblemPlusJSONResponse(p),
			}, nil
		}
	}

	return gen.RequestCharacterAction204Response{}, nil
}
```

- [ ] **Step 9: Add the `action` verb to `internal/cliapp/root.go`**

Add a field to `App`:
```go
	actionCmd  *cobra.Command
```

Add to the struct literal in `newApp`:
```go
		actionCmd: &cobra.Command{
			Use:   "action",
			Short: "Request an action on an aggregate (PUT .../action) — effect, if any, is asynchronous",
		},
```

Add `a.actionCmd` to the `root.AddCommand(...)` call.

- [ ] **Step 10: Add the CLI subcommand to `internal/cliapp/character.go`**

Add `"encoding/json"` to the import block.

Add, after the `set player` command registration:
```go
	a.actionCmd.AddCommand(&cobra.Command{
		Use:   "character <characterId> <jsonPayload>",
		Short: "Request an action on a Character",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			client, _, err := a.client()
			if err != nil {
				return err
			}
			var payload map[string]any
			if err := json.Unmarshal([]byte(args[1]), &payload); err != nil {
				return fmt.Errorf("invalid JSON payload: %w", err)
			}
			return client.Command("PUT", "/characters/"+args[0]+"/action", payload)
		},
	})
```

- [ ] **Step 11: Build, vet, test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: all clean.

- [ ] **Step 12: Commit**

```bash
git add internal/domain/character/events/events.go internal/domain/character/character.go \
  internal/domain/character/character_test.go internal/command/character/service.go \
  api/command/openapi.yaml api/command/gen/server.gen.go internal/httpapi/command/server.go \
  internal/cliapp/root.go internal/cliapp/character.go
git commit -m "character: add PUT /characters/{id}/action trigger endpoint

Raises ActionRequested without mutating the aggregate (Apply has an
explicit no-op case) -- the actual effect, if any, is decided later
and asynchronously by timadorus-engine (added in a following commit),
never by the Character aggregate itself. New top-level 'action' CLI
verb, since none of the existing 8 verbs fit an arbitrary trigger.

go build/vet/test clean, including new RequestAction domain tests
(raises exactly one event, mutates no field, archived guard)."
```

---

### Task 2: `internal/engine/timadorus` — ruleset cache + Processor

**Files:**
- Create: `internal/engine/timadorus/cache.go`
- Create: `internal/engine/timadorus/cache_test.go`
- Create: `internal/engine/timadorus/processor.go`
- Create: `internal/engine/timadorus/processor_test.go`

**Interfaces:**
- Consumes: `events.TypeActionRequested`/`events.ActionRequested` (Task 1),
  `projection.Projector` interface (`Name() string`, `Subjects() []string`,
  `Handle(ctx, tx pgx.Tx, env bus.Envelope) error` — `internal/projection/projector.go`),
  `eventsourcing.NewRegistry()`/`NewRepository[T Aggregate](store, aggregateType, newBlank) *Repository[T]`,
  `postgres.NewStore(pool, registry) *Store`, `postgres.WithTx(ctx, tx) context.Context`
  (`internal/eventstore/postgres`), `character.New`/`character.Character.Info()`/`SetInfo`.
- Produces: `timadorus.NewProcessor(pool *pgxpool.Pool) *Processor` (consumed by Task 3).

- [ ] **Step 1: Write the ruleset-name cache — `internal/engine/timadorus/cache.go`**

```go
// Package timadorus is timadorus-engine's Processor: an internal/projection.Projector that
// reacts to Character ActionRequested events, conditionally (based on the Character's
// Campaign's Ruleset name) appending a timestamp to the Character's info field. See
// docs/superpowers/specs/2026-08-23-character-action-timadorus-engine-design.md.
package timadorus

import (
	"sync"

	"github.com/google/uuid"
)

// rulesetCache caches each Campaign's Ruleset name, keyed by campaign id. A Campaign's Ruleset
// reference is set once at creation and never changes (no such command exists on Campaign), so
// once resolved, a lookup never needs repeating — even though Handle runs on every incoming
// Character event, not just the ones that end up matching. Guarded by a mutex for
// defensiveness only: Processor.Handle runs on a single goroutine today (one subject,
// SubscribersCount: 1 — internal/bus.NewSubscriber's doc comment), mirroring how
// projection.Router itself guards its own single-goroutine-today attempts map
// (internal/projection/router.go).
type rulesetCache struct {
	mu    sync.RWMutex
	names map[uuid.UUID]string
}

func newRulesetCache() *rulesetCache {
	return &rulesetCache{names: make(map[uuid.UUID]string)}
}

func (c *rulesetCache) get(campaignID uuid.UUID) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	name, ok := c.names[campaignID]
	return name, ok
}

// set is only ever called after a fully successful lookup (see Processor.rulesetName) — a
// not-found/error result is never cached, so a transient "campaign not projected yet" failure
// doesn't poison the cache; the next redelivery attempt just re-queries.
func (c *rulesetCache) set(campaignID uuid.UUID, name string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.names[campaignID] = name
}
```

- [ ] **Step 2: Write the cache's unit test — `internal/engine/timadorus/cache_test.go`**

```go
package timadorus

import (
	"testing"

	"github.com/google/uuid"
)

func TestRulesetCache(t *testing.T) {
	c := newRulesetCache()
	campaignID := uuid.New()

	if _, ok := c.get(campaignID); ok {
		t.Fatal("got a hit on an empty cache, want a miss")
	}

	c.set(campaignID, "Timadorus")

	name, ok := c.get(campaignID)
	if !ok {
		t.Fatal("got a miss right after set, want a hit")
	}
	if name != "Timadorus" {
		t.Fatalf("got %q, want %q", name, "Timadorus")
	}

	// A second campaign must not collide with the first.
	other := uuid.New()
	if _, ok := c.get(other); ok {
		t.Fatal("got a hit for an unrelated campaign id, want a miss")
	}
}
```

- [ ] **Step 3: Run the cache test**

Run: `go test ./internal/engine/timadorus/... -run TestRulesetCache -v`
Expected: PASS.

- [ ] **Step 4: Write the Processor — `internal/engine/timadorus/processor.go`**

```go
package timadorus

import (
	"context"
	"encoding/json"
	"fmt"
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
)

// ProcessorName is both the durable JetStream consumer name and the checkpoint table key (see
// projection.Projector.Name's doc comment). It reuses the shared projection_checkpoints table
// (internal/projection/checkpoint) — no new migration needed.
const ProcessorName = "timadorus-engine"

// targetRulesetName is matched case-insensitively against each Character's Campaign's Ruleset
// name (design spec §2) — the one hardcoded piece of business logic this initial version has.
const targetRulesetName = "timadorus"

// Processor implements projection.Projector unmodified, but — unlike every read-model
// projector — legitimately re-enters the write side (loads and saves a Character aggregate).
// This is why it lives in its own binary (cmd/timadorus-engine) rather than alongside the
// read-only projectors in cmd/projector: importing domain/character/eventsourcing/eventstore
// here would break that binary's documented "never imports domain invariant code" guarantee.
type Processor struct {
	characters *eventsourcing.Repository[*character.Character]
	cache      *rulesetCache
}

// NewProcessor builds its own Registry scoped to just Character — the only aggregate type this
// processor ever loads/saves — mirroring cmd/command-api/main.go's construction pattern
// (registry -> postgres.NewStore(pool, registry) -> eventsourcing.NewRepository(store, ...)).
func NewProcessor(pool *pgxpool.Pool) *Processor {
	registry := eventsourcing.NewRegistry()
	events.Register(registry)
	store := postgres.NewStore(pool, registry)

	return &Processor{
		characters: eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
			return &character.Character{}
		}),
		cache: newRulesetCache(),
	}
}

func (p *Processor) Name() string { return ProcessorName }

func (p *Processor) Subjects() []string { return []string{bus.Subject(events.AggregateType)} }

func (p *Processor) Handle(ctx context.Context, tx pgx.Tx, env bus.Envelope) error {
	if env.EventType != events.TypeActionRequested {
		return nil
	}
	var e events.ActionRequested
	if err := json.Unmarshal(env.Payload, &e); err != nil {
		return fmt.Errorf("timadorus-engine: unmarshal %s: %w", env.EventType, err)
	}

	var campaignID uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT campaign_id FROM characters_read_model WHERE id = $1`, env.AggregateID,
	).Scan(&campaignID); err != nil {
		return fmt.Errorf("timadorus-engine: look up campaign for character %s: %w", env.AggregateID, err)
	}

	rulesetName, err := p.rulesetName(ctx, tx, campaignID)
	if err != nil {
		return err
	}
	if !strings.EqualFold(rulesetName, targetRulesetName) {
		return nil // not our ruleset — no-op, still checkpointed as handled
	}

	return p.appendActionTimestamp(ctx, tx, env.AggregateID, e.OccurredAt)
}

func (p *Processor) rulesetName(ctx context.Context, tx pgx.Tx, campaignID uuid.UUID) (string, error) {
	if name, ok := p.cache.get(campaignID); ok {
		return name, nil
	}

	var name string
	err := tx.QueryRow(ctx,
		`SELECT r.name FROM campaigns_read_model c
		 JOIN rulesets_read_model r ON r.id = c.ruleset_id
		 WHERE c.id = $1`, campaignID,
	).Scan(&name)
	if err != nil {
		return "", fmt.Errorf("timadorus-engine: look up ruleset for campaign %s: %w", campaignID, err)
	}

	p.cache.set(campaignID, name)
	return name, nil
}

// appendActionTimestamp loads the Character within tx (via postgres.WithTx, so the write
// commits atomically with the Router's own checkpoint advance — a concurrency conflict here
// just fails Handle, which the Router already retries via Nack + redelivery, re-Loading the
// current version), appends occurredAt to info's "actions" list, and saves via the
// already-existing SetInfo — reusing the whole InfoChanged/projector/read-model pipeline
// built for that feature, not a new mutation path.
func (p *Processor) appendActionTimestamp(ctx context.Context, tx pgx.Tx, characterID uuid.UUID, occurredAt time.Time) error {
	txCtx := postgres.WithTx(ctx, tx)

	c, err := p.characters.Load(txCtx, characterID)
	if err != nil {
		return fmt.Errorf("timadorus-engine: load character %s: %w", characterID, err)
	}

	// Parse into a generic map, touching only "actions" — never discard other top-level keys a
	// human might have set via the already-existing PUT .../info endpoint. If the existing
	// info isn't valid JSON, or isn't a JSON object at all (allowed today: SetInfo accepts any
	// string), start fresh rather than erroring — retrying won't fix malformed content, so
	// erroring here would get this Character permanently stuck instead of self-healing.
	info := map[string]any{}
	if raw := c.Info(); raw != "" {
		_ = json.Unmarshal([]byte(raw), &info) // best-effort; info stays {} on failure
	}
	actions, _ := info["actions"].([]any)
	info["actions"] = append(actions, occurredAt.UTC().Format(time.RFC3339))

	newInfo, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("timadorus-engine: marshal updated info for character %s: %w", characterID, err)
	}

	if err := c.SetInfo(string(newInfo)); err != nil {
		return fmt.Errorf("timadorus-engine: set info for character %s: %w", characterID, err)
	}
	if err := p.characters.Save(txCtx, c); err != nil {
		return fmt.Errorf("timadorus-engine: save character %s: %w", characterID, err)
	}
	return nil
}
```

- [ ] **Step 5: Build**

Run: `go build ./internal/engine/... 2>&1`
Expected: clean.

- [ ] **Step 6: Write the Processor's integration test — `internal/engine/timadorus/processor_test.go`**

This mirrors `internal/projection/universe/projector_test.go`'s testcontainers pattern, but also
seeds `campaigns_read_model`/`rulesets_read_model` rows directly (no need to build full
Campaign/Ruleset aggregates — the Processor only reads these tables) and drives real
Character `Create`/`RequestAction` through the real event store so `Handle`'s `Load`/`SetInfo`/
`Save` round-trip is genuinely exercised.

```go
package timadorus_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/pubsub/gochannel"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/domain/character"
	"github.com/timadorus/platform/internal/domain/character/events"
	"github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/eventsourcing"
	"github.com/timadorus/platform/internal/eventstore/postgres"
	"github.com/timadorus/platform/internal/projection"
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
			"../../projection/checkpoint/migrations/0001_projection_checkpoints.up.sql",
			"../../projection/character/migrations/0001_character_read_model.up.sql",
			"../../projection/character/migrations/0002_character_info.up.sql",
			"../../projection/campaign/migrations/0001_campaign_read_model.up.sql",
			"../../projection/campaign/migrations/0002_campaign_ruleset_id.up.sql",
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

func runEngine(t *testing.T, pool *pgxpool.Pool) (publish func(env bus.Envelope), wait func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())

	inMemory := gochannel.NewGoChannel(gochannel.Config{Persistent: true}, watermill.NopLogger{})
	t.Cleanup(func() { _ = inMemory.Close() })

	p := timadorus.NewProcessor(pool)
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

func TestProcessor_MatchingRuleset_AppendsTimestamp(t *testing.T) {
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

	deadline := time.Now().Add(5 * time.Second)
	var info string
	for time.Now().Before(deadline) {
		if err := pool.QueryRow(context.Background(),
			`SELECT info FROM characters_read_model WHERE id = $1`, characterID,
		).Scan(&info); err != nil {
			t.Fatalf("query info: %v", err)
		}
		if info != "" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	var decoded struct {
		Actions []string `json:"actions"`
	}
	if err := json.Unmarshal([]byte(info), &decoded); err != nil {
		t.Fatalf("info %q is not the expected shape: %v", info, err)
	}
	if len(decoded.Actions) != 1 || decoded.Actions[0] != occurredAt.Format(time.RFC3339) {
		t.Fatalf("got actions %v, want [%q]", decoded.Actions, occurredAt.Format(time.RFC3339))
	}

	wait()
}

func TestProcessor_NonMatchingRuleset_NoOp(t *testing.T) {
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

	var info string
	if err := pool.QueryRow(context.Background(),
		`SELECT info FROM characters_read_model WHERE id = $1`, characterID,
	).Scan(&info); err != nil {
		t.Fatalf("query info: %v", err)
	}
	if info != "" {
		t.Fatalf("got info %q, want unchanged empty string (ruleset doesn't match)", info)
	}

	wait()
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

- [ ] **Step 7: Run the integration tests**

Run: `go test ./internal/engine/timadorus/... -v`
Expected: `TestRulesetCache`, `TestProcessor_MatchingRuleset_AppendsTimestamp`, and
`TestProcessor_NonMatchingRuleset_NoOp` all PASS. Requires Docker (testcontainers) — matching
this repo's existing `internal/eventstore/postgres`/`internal/projection/universe` test
requirements.

If any migration filename in Step 6 doesn't match what's actually on disk (e.g.
`campaigns`/`rulesets` migration file naming may differ slightly), run
`find internal/projection/campaign internal/projection/ruleset internal/eventstore/postgres -name "*.up.sql" -o -name "*.sql"`
first and correct the `WithOrderedInitScripts` paths accordingly — the table/column names
(`campaigns_read_model.ruleset_id`, `rulesets_read_model.name`) are the load-bearing part, not
the exact filenames.

- [ ] **Step 8: Commit**

```bash
git add internal/engine/timadorus/
git commit -m "engine: add timadorus-engine's Processor (ruleset-cached action handler)

New internal/engine/timadorus package implementing the existing,
unmodified projection.Projector interface. Reacts only to
ActionRequested; looks up the Character's Campaign's Ruleset name
(cached per campaign id, since it never changes), and — only when it
matches \"timadorus\" case-insensitively — appends the event's own
OccurredAt into info's \"actions\" list via the already-existing
SetInfo, reusing the whole InfoChanged pipeline. The aggregate write
joins the Router's own transaction (postgres.WithTx), so a
concurrency conflict just fails Handle and the Router's existing
Nack+redelivery retries it safely.

go build/vet clean; go test ./internal/engine/timadorus/... green
(testcontainers-backed, mirrors internal/projection/universe's own
test pattern): cache hit/miss, matching-ruleset appends exactly one
timestamp, non-matching-ruleset is a verified no-op."
```

---

### Task 3: `cmd/timadorus-engine` binary + config

**Files:**
- Modify: `internal/config/config.go`
- Create: `cmd/timadorus-engine/main.go`

**Interfaces:**
- Consumes: `timadorus.NewProcessor(pool) *Processor` (Task 2), `projection.NewRouter`,
  `bus.NewSubscriber`, `observability.HealthzHandler`/`ReadyzHandler` (all pre-existing).
- Produces: `config.LoadTimadorusEngine() TimadorusEngine` (consumed only by this binary).

- [ ] **Step 1: Add `TimadorusEngine` config to `internal/config/config.go`**

Add, after the `Projector`/`LoadProjector` pair:
```go
type TimadorusEngine struct {
	// HTTPAddr serves /healthz, /readyz, /metrics only — same shape as Projector's, see that
	// type's doc comment.
	HTTPAddr    string
	DatabaseURL string
	NATSURL     string
}

func LoadTimadorusEngine() TimadorusEngine {
	return TimadorusEngine{
		HTTPAddr:    getEnv("TIMADORUS_ENGINE_ADDR", ":8084"),
		DatabaseURL: getEnv("DATABASE_URL", "postgres://timadorus:timadorus@localhost:5432/timadorus?sslmode=disable"),
		NATSURL:     getEnv("NATS_URL", "nats://localhost:4222"),
	}
}
```

- [ ] **Step 2: Write `cmd/timadorus-engine/main.go`**

```go
// timadorus-engine subscribes to the Character event stream and reacts to ActionRequested
// events — see internal/engine/timadorus for the actual logic. Structurally identical to
// cmd/projector (same Router/checkpoint machinery), but registers exactly one processor, which
// is why it's a separate binary: unlike every projector, it legitimately imports full
// write-side packages (domain/character, eventsourcing, eventstore/postgres).
package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/ThreeDotsLabs/watermill"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/timadorus/platform/internal/bus"
	"github.com/timadorus/platform/internal/config"
	timadorusengine "github.com/timadorus/platform/internal/engine/timadorus"
	"github.com/timadorus/platform/internal/observability"
	"github.com/timadorus/platform/internal/projection"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, logger); err != nil {
		logger.Error("timadorus-engine: fatal", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, logger *slog.Logger) error {
	cfg := config.LoadTimadorusEngine()

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	newSubscriber := func(durableName string) (message.Subscriber, error) {
		return bus.NewSubscriber(cfg.NATSURL, durableName, watermill.NewSlogLogger(logger))
	}
	router := projection.NewRouter(pool, newSubscriber, logger)

	processors := []projection.Projector{
		timadorusengine.NewProcessor(pool),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", observability.HealthzHandler())
	mux.HandleFunc("/readyz", observability.ReadyzHandler(pool))
	mux.Handle("/metrics", promhttp.Handler())
	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	var httpWG sync.WaitGroup
	httpWG.Add(1)
	go func() {
		defer httpWG.Done()
		logger.Info("timadorus-engine: observability endpoints listening", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("timadorus-engine: observability http server failed", "error", err)
		}
	}()

	logger.Info("timadorus-engine: starting", "processors", len(processors))
	runErr := router.Run(ctx, processors) // blocks until ctx is cancelled

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(shutdownCtx)
	httpWG.Wait()

	return runErr
}
```

- [ ] **Step 3: Build, vet**

Run: `go build ./... && go vet ./...`
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go cmd/timadorus-engine/main.go
git commit -m "engine: add cmd/timadorus-engine binary

Structurally identical to cmd/projector (same Router/checkpoint
wiring, same /healthz+/readyz+/metrics ops surface), registering
exactly one processor (internal/engine/timadorus.NewProcessor).
config.LoadTimadorusEngine mirrors LoadProjector's shape, its own
TIMADORUS_ENGINE_ADDR env var defaulting to :8084.

go build/vet clean."
```

---

### Task 4: Infra wiring (Dockerfile, Helm chart, devcluster) + live verification

**Files:**
- Create: `Dockerfile.timadorus-engine`
- Create: `deploy/helm/timadorus-platform/templates/timadorus-engine-deployment.yaml`
- Create: `deploy/helm/timadorus-platform/templates/timadorus-engine-service.yaml`
- Modify: `deploy/helm/timadorus-platform/values.yaml`
- Modify: `test/e2e/internal/images.go`
- Modify: `test/e2e/internal/platform.go`

**Interfaces:**
- Consumes: `test/e2e/internal.BuildTagLoadImages`/`InstallPlatform` (unmodified — both already
  iterate `ImageTags` generically; see Global Constraints for the one new `imageValuesKey` case).

- [ ] **Step 1: Write `Dockerfile.timadorus-engine`**

```dockerfile
# Build: docker build -f Dockerfile.timadorus-engine -t timadorus/timadorus-engine .
# Run:   docker run -p 8084:8084 --env-file .env timadorus/timadorus-engine

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/timadorus-engine ./cmd/timadorus-engine

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/timadorus-engine /timadorus-engine
EXPOSE 8084
ENTRYPOINT ["/timadorus-engine"]
```

- [ ] **Step 2: Add the `timadorusEngine` values section to `values.yaml`**

Add, immediately after the `projector:` block:
```yaml
timadorusEngine:
  image:
    repository: timadorus/timadorus-engine
    tag: ""
    pullPolicy: IfNotPresent
  replicas: 1
  containerPort: 8084
  resources:
    requests:
      cpu: 100m
      memory: 128Mi
    limits:
      cpu: 500m
      memory: 256Mi
```

- [ ] **Step 3: Write `timadorus-engine-deployment.yaml`**

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-timadorus-engine
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
    app.kubernetes.io/component: timadorus-engine
spec:
  replicas: {{ .Values.timadorusEngine.replicas }}
  strategy:
    type: Recreate
  selector:
    matchLabels:
      {{- include "timadorus-platform.selectorLabels" . | nindent 6 }}
      app.kubernetes.io/component: timadorus-engine
  template:
    metadata:
      labels:
        {{- include "timadorus-platform.selectorLabels" . | nindent 8 }}
        app.kubernetes.io/component: timadorus-engine
    spec:
      containers:
        - name: timadorus-engine
          image: "{{ .Values.timadorusEngine.image.repository }}:{{ .Values.timadorusEngine.image.tag | default .Chart.AppVersion }}"
          imagePullPolicy: {{ .Values.timadorusEngine.image.pullPolicy }}
          ports:
            - name: http
              containerPort: {{ .Values.timadorusEngine.containerPort }}
          env:
            - name: TIMADORUS_ENGINE_ADDR
              value: ":{{ .Values.timadorusEngine.containerPort }}"
            {{- include "timadorus-platform.databaseURLEnv" . | nindent 12 }}
            - name: NATS_URL
              value: {{ include "timadorus-platform.natsURL" . | quote }}
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
            {{- toYaml .Values.timadorusEngine.resources | nindent 12 }}
```

- [ ] **Step 4: Write `timadorus-engine-service.yaml`**

```yaml
apiVersion: v1
kind: Service
metadata:
  name: {{ include "timadorus-platform.fullname" . }}-timadorus-engine
  labels:
    {{- include "timadorus-platform.labels" . | nindent 4 }}
    app.kubernetes.io/component: timadorus-engine
spec:
  type: ClusterIP
  selector:
    {{- include "timadorus-platform.selectorLabels" . | nindent 4 }}
    app.kubernetes.io/component: timadorus-engine
  ports:
    - name: http
      port: {{ .Values.timadorusEngine.containerPort }}
      targetPort: http
```

- [ ] **Step 5: Add the component to `test/e2e/internal/images.go`**

Change:
```go
var imageComponents = []string{"command-api", "query-api", "projector", "migrate", "web"}
```
to:
```go
var imageComponents = []string{"command-api", "query-api", "projector", "timadorus-engine", "migrate", "web"}
```

- [ ] **Step 6: Add the mapping to `test/e2e/internal/platform.go`**

Change `imageValuesKey`:
```go
func imageValuesKey(component string) string {
	switch component {
	case "command-api":
		return "commandApi"
	case "query-api":
		return "queryApi"
	case "projector":
		return "projector"
	case "timadorus-engine":
		return "timadorusEngine"
	case "migrate":
		return "migration"
	default:
		return component
	}
}
```

- [ ] **Step 7: Build, vet, test**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add Dockerfile.timadorus-engine \
  deploy/helm/timadorus-platform/templates/timadorus-engine-deployment.yaml \
  deploy/helm/timadorus-platform/templates/timadorus-engine-service.yaml \
  deploy/helm/timadorus-platform/values.yaml \
  test/e2e/internal/images.go test/e2e/internal/platform.go
git commit -m "deploy: wire timadorus-engine into the Helm chart and devcluster

Dockerfile.timadorus-engine, a Deployment+Service pair mirroring
projector's exactly (own component label, own TIMADORUS_ENGINE_ADDR
env var), a new timadorusEngine values section, and the two devcluster
touch points every new component needs: imageComponents (so dev-up
builds/loads its image) and imageValuesKey (so its tag gets threaded
into the right Helm value — BuildTagLoadImages/InstallPlatform
themselves are unmodified, both already iterate ImageTags
generically).

go build/vet/test clean."
```

- [ ] **Step 9: Live end-to-end verification against the dev cluster**

Run `make dev-up` from the repo root (rebuilds/loads all images including the new
`timadorus-engine`, redeploys via `helm upgrade`). Confirm in the output/via `kubectl get pods
--namespace timadorus-dev` that a `*-timadorus-engine-*` pod is Running and Ready (readiness
probe hits `/readyz`).

Using the same "Direct API access" pattern `make dev-up` prints (fetch a token, port-forward
Traefik), exercise the full feature:

1. **Matching ruleset (should update `info`).** The dev cluster is already seeded with a
   "Timadorus" universe → "Bahamut" campaign whose Ruleset is named "Timadorus" (from prior
   session work), and characters already exist under it — reuse one, or create a fresh one via
   `POST /api/command/campaigns/{campaignId}/characters`. Call:
   ```bash
   curl -X PUT "http://localhost:8080/api/command/characters/$CHAR_ID/action" \
     -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" -d '{}'
   ```
   Expect `204`. Poll `GET /api/query/characters/$CHAR_ID` for a few seconds and confirm `info`
   becomes `{"actions":["<some RFC3339 timestamp>"]}`. Call the same `PUT .../action` a second
   time and confirm `info` now has **two** timestamps in `actions` (proving repeated calls
   append, not overwrite).

2. **Non-matching ruleset (should NOT update `info`).** Create a second Ruleset with a different
   name (e.g. `POST /api/command/rulesets` `{"name":"NotTimadorus"}`), a Campaign using it, and a
   Character under that Campaign. Call the same `PUT .../action` against that Character, wait a
   few seconds, and confirm via `GET` that its `info` is still `""` (unchanged) — the engine
   correctly decided not to act.

3. **Archived guard.** Archive a Character (`POST /api/command/characters/{id}/archive`), then
   call `PUT .../action` against it — expect `409`.

4. **Cache proof (optional but recommended).** After step 1's first successful `action` call,
   directly `UPDATE rulesets_read_model SET name = 'ChangedLater' WHERE id = ...` for that same
   Ruleset (via `kubectl exec` into the Postgres pod, or a temporary debug connection), then call
   `PUT .../action` again on a Character in that same Campaign — confirm `info` still gains a new
   timestamp (i.e. the engine used its cached name, not the just-changed one), demonstrating the
   cache is actually being used. Revert the row afterward if the cluster will be reused further
   (not required — this is disposable dev data).

If any step fails, this is real product behavior to debug and fix, not an environment issue to
explain away — this exercises a genuinely new binary/consumer for the first time.

## Final Verification

- `go build ./... && go vet ./... && go test ./...` clean from the repo root.
- `make dev-up` succeeds with a fourth, healthy `timadorus-engine` pod alongside
  `command-api`/`query-api`/`projector`/`web`.
- All 4 live-verification scenarios in Task 4 Step 9 pass against the real cluster.
