# Ruleset Data Tables: Loading, Hooks, and Read-Model Exposure — Design

## Context

`internal/engine/timadorus/tables/traits.yaml` is the first of what will become "an extensive set"
of ruleset-specific reference-data files (traits today; skills, equipment, etc. later), each
describing a named table of rows scoped to the "timadorus" Ruleset. This spec covers three things:

1. Loading each table file into a native Go structure whose fields mirror the YAML columns
   directly, with room to attach one or more function "hooks" to each row.
2. Making that same data queryable through the query API, the same way every other read-only
   resource in this platform is.
3. Doing the load/sync idempotently at `cmd/timadorus-engine` startup, so it's safe to restart the
   engine (or run multiple replicas) without duplicating or erroring on already-present rows.

Table data is immutable for a given Ruleset — rule changes are always modeled as new Ruleset
aggregates, never as edits to an existing table's rows (plan-level premise, confirmed with the
user). This is why the design below treats tables as **seeded reference data**, not as
event-sourced aggregates: there's no lifecycle to model, no command to mutate a row, and no event
type for "a table row changed."

## Decisions

### One concrete Go struct per table file — not a generic wrapper

Each table file gets its own hand-written struct in its own file (e.g.
`internal/engine/timadorus/tables/traits.go` for `traits.yaml`), with the YAML columns unpacked
directly onto the struct's own fields (via `yaml` tags), not nested under a generic payload field.
By convention, every such struct's **first field is `Key`** (the row's identifier, taken from the
YAML file's outer map key — not itself a YAML-tagged field) and its **last field is `Hooks`** (a
slice of function values, never populated by YAML at all):

```go
// internal/engine/timadorus/tables/traits.go
type TraitsRow struct {
	Key         string          `yaml:"-" json:"-"` // set by Load from the row's own map key
	DisplayName string          `yaml:"displayName"`
	Description string          `yaml:"description"`
	Hooks       []Hook          `yaml:"-" json:"-"` // never serialized — see "Hook signature" below
}
```

Tagging `Key`/`Hooks` as excluded from both YAML and JSON is a technical necessity, not a style
choice: `Key` isn't present in the row's own YAML object (it's the *map* key one level up), and
`Hooks` holds `func(...)` values that `encoding/json` cannot marshal at all — omitting them keeps
both the loader and the read-model sync (which `json.Marshal`s each row) correct regardless of
whether any hooks have been registered yet.

Each table's own file also defines a small amount of boilerplate — a `TraitsTable` type wrapping
`map[string]*TraitsRow`, plus `RowKey()`/`AddHook()`/`RunHooks()` methods on `*TraitsRow` (below) —
copy-adapted per file rather than shared through a generic container. This matches how this
codebase already treats repeated shapes elsewhere ("two instances of copy-adapt is correct by this
codebase's own convention; three would earn the abstraction" — `docs/BACKLOG.md`) and avoids
introducing a second generic type into a codebase that deliberately has exactly one today
(`eventsourcing.Repository[T Aggregate]`).

### Hook signature: mirrors `projection.Projector.Handle` exactly

```go
// Hook is invoked when some future event handler decides a row is relevant to an incoming event
// — see "Explicitly Out of Scope" below for why no such caller exists yet. Its signature
// deliberately matches projection.Projector.Handle's: a hook can do anything a full event
// processor can (load/save aggregates within the same transaction), scoped to reacting on behalf
// of one row instead of a whole event type.
type Hook func(ctx context.Context, tx pgx.Tx, env bus.Envelope) error
```

`RegisterHook`/`AddHook` appends — multiple hooks per row run in registration order via
`RunHooks`, stopping at the first error (matching every other error-handling shape already used in
this package's processors).

### One generic sync mechanism, via a small interface — not a generic container

The per-table Go *structs* stay fully concrete and hand-written (per your decision), but the
*read-model upsert logic* — parse each table's rows into JSON, `INSERT ... ON CONFLICT DO
NOTHING` per row — is real, non-trivial, and would otherwise be copy-adapted once per table file
forever. It's written once, behind a two-method interface every table's wrapper type implements:

```go
// Syncable is implemented by every table's wrapper type (TraitsTable, and whichever follow it)
// so RegisterTables's upsert loop lives in one place.
type Syncable interface {
	TableName() string          // stable name, e.g. "traits" — the read model's partition key
	RowData() map[string]any    // row key -> row value, ready for json.Marshal (Key/Hooks excluded via tags)
}
```

This is a targeted, minimal sharing (one interface, one loop) rather than a generic type
parameter — deliberately smaller in scope than a `Table[T]` container would have been.

### Read model: one generic table across all files (per your confirmed decision)

```sql
CREATE TABLE ruleset_tables_read_model (
    ruleset_id UUID NOT NULL,
    table_name TEXT NOT NULL,
    row_key    TEXT NOT NULL,
    data       JSONB NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (ruleset_id, table_name, row_key)
);
```

Table names are unique **within** a Ruleset (the primary key), not globally — a future different
Ruleset can reuse the name "traits" for its own, unrelated table without collision, exactly as
you specified. Query API, nested like every other resource in this platform:

- `GET /rulesets/{rulesetId}/tables/{tableName}` — every row, `{key, data}` per entry.
- `GET /rulesets/{rulesetId}/tables/{tableName}/{rowKey}` — one row's `data`.

Backed by a new `internal/query/rulesettables/repository.go`, following the exact layering
convention every other read-only resource already uses (`internal/query/<name>/repository.go` →
`internal/httpapi/query/server.go` handler → an OpenAPI path). The migration lives at
`internal/projection/rulesettables/migrations/` — grouped with the other read-model schemas by
convention, even though nothing here is a `projection.Projector` reacting to bus events; it's
grouped there for discoverability by both the writer (`cmd/timadorus-engine`) and reader
(`cmd/query-api`), the same way every other read-model schema is.

### Startup sync: idempotent, and a real gap this surfaced in `RegisterRuleset`

`RegisterTables(ctx, pool, rulesetID uuid.UUID, tables ...Syncable) error` runs right after
`RegisterRuleset` in `cmd/timadorus-engine/main.go`'s startup, using
`INSERT ... ON CONFLICT (ruleset_id, table_name, row_key) DO NOTHING` per row — safe to run on
every startup and safe under concurrent replicas, without a separate existence check racing the
insert.

This needs the target Ruleset's real id, which `RegisterRuleset` today only returns on the
success path (`error` only, no id, and its "already exists" branch returns `nil` with no id at
all). Getting that id back **without reintroducing the exact class of read-model race the
`campaign-creation-default-traits` branch's final review already caught and fixed once** (resolve
identity from the event store / the same durable transaction as the invariant it's paired with,
never from an independently-racing projector) means `ruleset_names` — the reservation table
`Service.Create` already writes to, in the same transaction as the aggregate save — needs to
start carrying the id it reserves a name for, not just the name:

```sql
-- internal/command/ruleset/migrations/0002_ruleset_names_id.up.sql
ALTER TABLE ruleset_names ADD COLUMN id UUID;
```

`Service.Create`'s existing `INSERT INTO ruleset_names (name)` becomes `(name, id)`, populated
from the aggregate's own id in the same transaction it already runs in — no new race, since this
is the identical already-durable write, just widened by one column. A new
`Service.FindIDByName(ctx, name) (uuid.UUID, error)` reads it back for `RegisterRuleset`'s
"already exists" branch. `RegisterRuleset`'s signature becomes `(uuid.UUID, error)`.

Nullable (not `NOT NULL`) because `ruleset_names` may already hold rows reserved before this
migration ships, which have no id to backfill from (the same accepted, documented trade-off this
table's own `0001` migration already makes for the read-model-backfill problem — see its comment).
In practice the only Ruleset this platform creates today is "Timadorus", via this exact code path,
so any cluster where the updated `RegisterRuleset` has run even once has a correct id going
forward; a pre-migration reservation with a null id would surface as a clear, loud error from
`FindIDByName` rather than a silent wrong answer.

### Migration wiring

`scripts/migrate-up.sh`'s `schema_owners` list gains one new entry —
`"projection_ruleset_tables:internal/projection/rulesettables/migrations"` — the `ruleset_names`
id column rides along in the already-listed `command_ruleset` owner as a second migration file.

## Explicitly Out of Scope

- **Any actual `Dispatch`/hook-invocation call site.** No existing event currently references a
  table row by key (Character's `ActionRequested`/`InfoChanged` don't mention traits at all today)
  — inventing a caller now would mean guessing at an event shape that doesn't exist yet. This spec
  delivers the loading/hook-registration/read-model mechanism; wiring an actual event to an actual
  row's hooks is future work once a concrete need creates the event to react to.
- Any change to `internal/domain/*` or the command API — tables are read-only reference data with
  no command surface.
- Cascading/backfilling `id` into pre-existing `ruleset_names` rows created before this migration
  (see above — the accepted, already-precedented trade-off).
- A generic `Table[T]`/`Row[T]` container type (explicitly rejected in favor of per-file concrete
  structs, per your decision).
