# Character Action Trigger + timadorus-engine Design Spec

## Context

The Character aggregate recently gained an `info` field (an opaque JSON string, mutated only via
`PUT /characters/{characterId}/info`, commits `3729c27`/`bfe8b0b`). This spec adds a second,
independent way `info` can change: a `PUT /characters/{characterId}/action` endpoint that does
**not** mutate the aggregate directly. It only raises a trigger event; a brand-new, separate event
processor (`timadorus-engine`) reacts to that event, and — conditionally, based on the Character's
Campaign's Ruleset — appends a timestamp into `info`. This is the platform's first process-manager/
reactor-shaped consumer, as opposed to the read-model projectors built so far.

## 1. The `action` trigger

### Domain (`internal/domain/character`)

- New event `events.ActionRequested{ Payload string, OccurredAt time.Time }` — `Payload` is the
  PUT request's JSON body, re-marshaled to a canonical JSON string (same "opaque string, not a
  native Go type" representation `info` itself already uses). `TypeActionRequested =
  "character.action_requested.v1"`, registered like every other Character event.
- New aggregate method:
  ```go
  // RequestAction raises ActionRequested without mutating any aggregate field — the actual
  // effect (if any) happens later, asynchronously, in timadorus-engine, and only conditionally
  // (see §2). Guarded by the same archived check every other mutating command uses.
  func (c *Character) RequestAction(payload string) error {
      if c.archived {
          return ErrArchived
      }
      c.raise(&events.ActionRequested{Payload: payload, OccurredAt: time.Now().UTC()})
      return nil
  }
  ```
- `Apply()` gets an explicit `case *events.ActionRequested:` that does nothing but exists so the
  switch stays exhaustive and self-documenting (a reader shouldn't have to wonder whether the
  case was forgotten).

### Command service + HTTP + OpenAPI

- `command/character.Service.RequestAction(ctx, id uuid.UUID, payload string) error` — Load, call
  `RequestAction`, Save. Same shape as every other single-aggregate command method in this
  service.
- `api/command/openapi.yaml`: new path
  ```yaml
  /characters/{characterId}/action:
    put:
      operationId: requestCharacterAction
      summary: >
        Request an action on a Character. Does not change the Character directly — raises an
        ActionRequested event that timadorus-engine may or may not act on (see its own docs).
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
  A bare `type: object` schema (no `properties`) generates as `map[string]interface{}` in
  oapi-codegen's strict-server mode — the endpoint accepts any JSON object, unvalidated. No `422`:
  there's no content to fail domain validation.
- `internal/httpapi/command/server.go`: `RequestCharacterAction` handler, same
  missing-body-400/classify-driven-404-409/default-400 shape as `SetCharacterInfo`, marshaling
  `*request.Body` back to a JSON string before calling `s.character.RequestAction(ctx, id, ...)`.
- Regenerate `api/command/gen/server.gen.go` via `go generate ./api/command/...`.

### CLI

None of the 8 existing top-level verbs (`create`/`rename`/`archive`/`add`/`delete`/`set`/`get`/
`list` — `internal/cliapp/root.go`) fit "trigger an arbitrary action." Add a new top-level verb,
consistent with the documented verb-first design (`docs/PLAN.md` §14):
```go
actionCmd: &cobra.Command{
    Use:   "action",
    Short: "Request an action on an aggregate (PUT .../action) — effect, if any, is asynchronous",
},
```
registered in `root.AddCommand(...)`, with `internal/cliapp/character.go` adding:
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

## 2. `timadorus-engine`

### Why a fourth binary

`docs/PLAN.md`'s "three separate Go binaries" decision is deliberately narrowed by this spec:
`timadorus-engine` needs both (a) read access to `campaigns_read_model`/`rulesets_read_model` and
(b) full write-side access (`domain/character`, `eventsourcing`, `eventstore/postgres`) to
legitimately re-enter the write side and call `SetInfo`. The existing `projector` binary/package
tree is documented (`internal/projection.Projector`'s own doc comment) to import only aggregate
`events` sub-packages, never invariant-bearing domain code — folding this in would break that
guarantee for every projector sharing the binary. A separate binary keeps both guarantees intact.

### Package `internal/engine/timadorus`

Implements the **existing, unmodified** `projection.Projector` interface — no framework changes:

```go
package timadorus

const ProcessorName = "timadorus-engine"

type Processor struct {
    characters *eventsourcing.Repository[*character.Character]
    cache      *rulesetCache
}

// NewProcessor builds its own Registry scoped to just Character — the only aggregate type this
// processor ever loads/saves — mirroring cmd/command-api/main.go's construction pattern
// (registry -> postgres.NewStore(pool, registry) -> eventsourcing.NewRepository(store, ...)).
func NewProcessor(pool *pgxpool.Pool) *Processor {
    registry := eventsourcing.NewRegistry()
    characterevents.Register(registry)
    store := postgres.NewStore(pool, registry)

    return &Processor{
        characters: eventsourcing.NewRepository(store, character.AggregateType, func() *character.Character {
            return &character.Character{}
        }),
        cache: newRulesetCache(),
    }
}

func (p *Processor) Name() string         { return ProcessorName }
func (p *Processor) Subjects() []string   { return []string{bus.Subject(events.AggregateType)} }

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
    if !strings.EqualFold(rulesetName, "timadorus") {
        return nil // not our ruleset — no-op, still checkpointed as handled
    }

    return p.appendActionTimestamp(ctx, tx, env.AggregateID, e.OccurredAt)
}
```

- **`rulesetName`** — checks `p.cache` (campaign id → name) first; on a miss, runs the single
  joined query from the brainstorm (`campaigns_read_model JOIN rulesets_read_model`), caches the
  result, and returns it. A not-found/error result is never cached (see `rulesetCache` below).
- **`appendActionTimestamp`** — loads the Character via `postgres.WithTx(ctx, tx)` (joining the
  Router's own transaction, so this write commits atomically with the checkpoint advance), parses
  `Info()` as `{"actions": [...]}` (treating `""`/unparseable as a fresh empty list — a Character
  that's never had an action requested has `info == ""`), appends `e.OccurredAt` formatted as
  RFC3339 (the event's own recorded time, not wall-clock time at processing — keeps replay
  deterministic), marshals back, and calls the **already-existing** `character.SetInfo` — no new
  mutation path, reuses the whole `InfoChanged`/projector/read-model pipeline built for the
  previous feature. A concurrency conflict here just fails `Handle`, which the Router already
  retries via Nack + redelivery (`internal/projection/router.go`) — re-running `Handle` reloads
  the current version, so no bespoke retry loop is needed.

### `rulesetCache` (new file, same package)

```go
// rulesetCache caches each Campaign's Ruleset name, keyed by campaign id. A Campaign's Ruleset
// reference is set once at creation and never changes (no such command exists on Campaign), so
// once resolved, a lookup never needs repeating — even though Handle runs on every incoming
// Character event, not just the ones that end up matching. Guarded by a mutex for defensiveness
// only: Processor.Handle runs on a single goroutine today (one subject, SubscribersCount: 1 —
// internal/bus.NewSubscriber's doc comment), mirroring how projection.Router itself guards its
// own single-goroutine-today attempts map.
type rulesetCache struct {
    mu    sync.RWMutex
    names map[uuid.UUID]string
}

func newRulesetCache() *rulesetCache             { return &rulesetCache{names: make(map[uuid.UUID]string)} }
func (c *rulesetCache) get(id uuid.UUID) (string, bool) { c.mu.RLock(); defer c.mu.RUnlock(); n, ok := c.names[id]; return n, ok }
func (c *rulesetCache) set(id uuid.UUID, name string)   { c.mu.Lock(); defer c.mu.Unlock(); c.names[id] = name }
```

Only populated after a fully successful lookup, so a transient "campaign not projected yet" error
is never cached — the next redelivery attempt just re-queries.

### `cmd/timadorus-engine/main.go`

Near-identical to `cmd/projector/main.go`: same DB pool, same `bus.NewSubscriber`/
`projection.NewRouter` wiring, same `/healthz`/`/readyz`/`/metrics` ops surface, but registers
exactly one `projection.Projector`:
```go
processors := []projection.Projector{
    timadorusengine.NewProcessor(pool),
}
```
Reuses `config.LoadProjector()`'s exact shape (`HTTPAddr`/`DatabaseURL`/`NATSURL` — identical
needs) via a new `config.LoadTimadorusEngine()` that only changes the env var/default port:
`TIMADORUS_ENGINE_ADDR`, default `:8084` (next in the existing `:8081`/`:8082`/`:8083` sequence;
unrelated to devcluster's own local port-forward numbers, which live in a completely different
namespace).

### No new migrations

`timadorus-engine` reads existing tables owned by other projectors (read-only SQL, no schema
ownership) and writes only through the standard event store (`events`/`outbox`, already
provisioned). Its checkpoint reuses the existing shared `projection_checkpoints` table
(`internal/projection/checkpoint`), keyed by `Name() = "timadorus-engine"` — no new migration
directory, no new entry in `scripts/migrate-up.sh`'s schema-owner list.

## 3. Infra wiring

- `Dockerfile.timadorus-engine` — copy of `Dockerfile.projector`, binary name and `EXPOSE`
  changed, building `./cmd/timadorus-engine`.
- `deploy/helm/timadorus-platform/templates/timadorus-engine-deployment.yaml` /
  `-service.yaml` — copies of `projector-deployment.yaml`/`-service.yaml` with
  `app.kubernetes.io/component: timadorus-engine`, env var `TIMADORUS_ENGINE_ADDR` instead of
  `PROJECTOR_ADDR`.
- `values.yaml` — new `timadorusEngine:` section, same shape as `projector:`'s
  (`image.repository: timadorus/timadorus-engine`, `containerPort: 8084`, same default
  resources).
- `test/e2e/internal/images.go` — add `"timadorus-engine"` to `imageComponents`.
- `test/e2e/internal/platform.go` — add `case "timadorus-engine": return "timadorusEngine"` to
  `imageValuesKey` (the chart's existing camelCase convention for multi-word component names,
  same as `"migrate"` → `"migration"`).
- `test/e2e/cmd/devcluster/up.go` — no new wiring needed beyond what `BuildTagLoadImages`/
  `InstallPlatform` already do generically via `ImageTags`; `printStatus` doesn't need a new
  line (this component has no developer-facing port-forward — it's a pure background consumer,
  same as `projector` today, which also isn't mentioned in `printStatus`).

## Out of scope

- No new action *types* — the payload is carried but unused by `timadorus-engine`'s only current
  behavior; a future action type would be a separate, later change.
- No cross-restart cache persistence — `rulesetCache` is process-memory-only, acceptable given a
  cold cache just re-populates itself on first use per campaign.
- No cascading/notification back to the caller of `PUT .../action` about whether
  `timadorus-engine` actually acted — the endpoint's contract is "requested," not "completed."

## Verification

- `go build ./...`, `go vet ./...`, `go test ./...` clean, including new unit tests for
  `Character.RequestAction` (raises the event, no field mutation, `ErrArchived` guard) and for
  `rulesetCache` (miss populates, hit avoids a second query — via a fake/mock, not a real DB).
- Live, against the dev cluster (`make dev-up`, rebuilding/loading the new
  `timadorus-engine` image too):
  1. `PUT /characters/{id}/action` with an arbitrary JSON body returns `204`.
  2. For a Character whose Campaign's Ruleset is named `"Timadorus"` (any case) — confirm, via
     `GET /characters/{id}`, that `info` becomes `{"actions": ["<rfc3339>"]}` after a short delay,
     and that a second `action` call appends a second timestamp to the same list.
  3. For a Character under a differently-named Ruleset — confirm `info` stays unchanged after an
     `action` call.
  4. `PUT .../action` against an archived Character returns `409`.
  5. Confirm `timadorus-engine`'s pod logs / `/healthz` show it running independently of
     `projector`.
