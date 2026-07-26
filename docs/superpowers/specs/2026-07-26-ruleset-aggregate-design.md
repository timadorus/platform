# Ruleset Aggregate + Campaign.rulesetId + Parentless-Aggregate List Endpoints

## Context

The platform currently has six aggregate types (User, Universe, Campaign, Character, Entity,
Object). This adds a seventh, **Ruleset** — an independent, top-level aggregate representing a
reusable game system (e.g. "D&D 5e", "Pathfinder 2e") — and wires an immutable reference to it
from Campaign, since every Campaign is played under exactly one ruleset and that choice cannot
change after creation (converting a campaign to a different ruleset is an out-of-scope future
migration tool). Alongside this, list endpoints are added for all three parentless aggregates
(User, Universe, Ruleset) — the query API currently has no "list all" capability for any
aggregate without a parent to scope by, and this closes that gap consistently for all three at
once rather than just the new one.

## Goals

- Add a `Ruleset` aggregate: `name` (required, renameable), `description` (optional, mutable
  via a dedicated command), `references` (list of strings, optional, mutable via a dedicated
  command — a full-list replace, not incremental add/remove, since there's no minimum-count
  invariant to protect).
- Add an immutable `rulesetId` field to `Campaign`, validated at creation exactly like
  `universeId` (existence + not-archived check via the existing
  `apperrors.ErrParentNotFound`/`ErrParentArchived` vocabulary) — set once in `CampaignCreated`,
  no setter, no way to change it through the API.
- Add `GET /users`, `GET /universes`, `GET /rulesets` (list, non-archived only, ordered by
  name, no pagination) to the query API, and corresponding `timadorusctl list user` /
  `list universe` / `list ruleset` CLI commands.
- Follow the existing mechanical pattern exactly (Entity is the copy-adapt template for the
  Create/Rename/Archive shape; User is the template for "no parent"; Campaign's own
  Universe-validation is the template for Campaign's new Ruleset-validation).

## Non-goals

- No URL-format validation on `references` entries, no minimum count (can be empty).
- No pagination or `includeArchived` filtering on any list endpoint (matches every existing
  list endpoint's behavior — plan §13 already flags this as deferred).
- No tooling to convert a Campaign from one Ruleset to another (explicitly out of scope, a
  future feature per the request).
- No new command-service-layer unit tests — matches existing precedent (Entity/Object/Campaign
  have no `service_test.go` files; parent/reference validation is only unit-tested at the
  domain layer today, and covered end-to-end by `test/e2e`).

## Domain layer: `internal/domain/ruleset`

Modeled on `internal/domain/user` for the "no parent" shape, with the mutation surface
extended for the two new mutable fields:

- **Events** (`events/events.go`): `AggregateType = "ruleset"`.
  `RulesetCreated{ID, Name, Description, References []string, OccurredAt}`,
  `RulesetRenamed{Name, OccurredAt}`,
  `RulesetDescriptionChanged{Description, OccurredAt}`,
  `RulesetReferencesChanged{References []string, OccurredAt}`,
  `RulesetArchived{OccurredAt}`.
- **Aggregate** (`ruleset.go`): `Ruleset{Base, name, description string, references []string,
  archived bool}`. `New(name, description string, references []string) (*Ruleset, error)`
  validates only `name != ""` (`ErrNameRequired`) — description/references have no validation,
  matching the "no strict URL validation" decision. `Rename`, `SetDescription`,
  `SetReferences` each guard `if r.archived { return ErrArchived }` then raise their event
  unconditionally (no "no-op if unchanged" short-circuit needed for description/references
  since they're not identity-bearing like `name`, though `Rename` keeps the existing
  no-op-if-same-name convention). `Archive` is idempotent, matching every other aggregate.
- **Errors** (`errors.go`): `ErrNameRequired`, `ErrArchived` — no new error needed for
  description/references since they accept any value.

## Campaign changes: `internal/domain/campaign`

- `events.CampaignCreated` gains `RulesetID uuid.UUID` (json `rulesetId`), placed alongside
  `UniverseID` — both are immutable parent-like references not derivable from the envelope.
- `Campaign` gains a `rulesetID uuid.UUID` field + `RulesetID() uuid.UUID` getter.
  `New(universeID, rulesetID uuid.UUID, name string, gamemasterIDs []uuid.UUID)` gains the new
  parameter (placed after `universeID`, before `name`, mirroring how `Character.New(campaignID,
  entityID, playerUserID uuid.UUID, name string)` orders all of its reference ids before its
  name). `Apply`'s `CampaignCreated` case sets
  `c.rulesetID = e.RulesetID`. No command changes it afterward.

## Command layer: `internal/command/ruleset` and `internal/command/campaign` changes

- New `internal/command/ruleset/service.go`: `Service{rulesets *eventsourcing.Repository[*ruleset.Ruleset]}`
  (single repository, no parent to validate — matches `internal/command/user/service.go`'s
  shape). `Create(ctx, name, description string, references []string) (uuid.UUID, error)`,
  `Rename`, `SetDescription`, `SetReferences`, `Archive` — each `Load`-then-mutate-then-`Save`,
  matching every existing service exactly.
- `internal/command/campaign/service.go`: add `rulesets *eventsourcing.Repository[*ruleset.Ruleset]`
  field + constructor parameter (appended positionally, after `users`, matching how
  `charactercmd.NewService` appends new repository parameters), add `RulesetID uuid.UUID` to
  `CreateCmd`, and add a validation block in `Create` immediately after the Universe check,
  using the same `apperrors.ErrParentNotFound`/`ErrParentArchived` pair:
  ```go
  rulesetAgg, err := s.rulesets.Load(ctx, cmd.RulesetID)
  if err != nil {
      return uuid.Nil, fmt.Errorf("%w: ruleset %s", apperrors.ErrParentNotFound, cmd.RulesetID)
  }
  if rulesetAgg.IsArchived() {
      return uuid.Nil, fmt.Errorf("%w: ruleset %s", apperrors.ErrParentArchived, cmd.RulesetID)
  }
  ```

## Projection layer

- New `internal/projection/ruleset/migrations/0001_ruleset_read_model.{up,down}.sql`:
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
  A native `TEXT[]` column, not a junction table — `references` entries are opaque strings
  with no foreign-key relationship to another aggregate, unlike Creators/Gamemasters (which
  reference real User ids and get a proper junction table for that reason). The column is
  named `reference_urls`, not `references` — confirmed by direct test against a real Postgres
  that `REFERENCES` is a reserved keyword and errors as an unquoted column name
  (`syntax error at or near "references"`). The Go field, JSON field, and API property all
  stay `references`/`References` — only the SQL column name differs.
- New `internal/projection/ruleset/projector.go`: handles all five Ruleset events, following
  `internal/projection/entity/projector.go`'s exact structure (one `handle*` method per event
  type, `ON CONFLICT (id) DO NOTHING` on create, plain `UPDATE ... WHERE id = $1` on every
  mutation).
- `internal/projection/campaign/projector.go`'s `handleCreated`: add `ruleset_id` to the
  `INSERT INTO campaigns_read_model` column list and args.
- New `internal/projection/campaign/migrations/0002_campaign_ruleset_id.{up,down}.sql`:
  `ALTER TABLE campaigns_read_model ADD COLUMN ruleset_id UUID NOT NULL` (up),
  `ALTER TABLE campaigns_read_model DROP COLUMN ruleset_id` (down). No index needed — nothing
  queries campaigns by ruleset id in this iteration (YAGNI; add one later if that query
  pattern actually arises).
- `scripts/migrate-up.sh`'s `schema_owners` array gains
  `"projection_ruleset:internal/projection/ruleset/migrations"`.

## Query layer

- New `internal/query/ruleset/repository.go`: `Ruleset{ID, Name, Description string,
  References []string, IsArchived bool}`. `Get(ctx, id)`, and `ListAll(ctx) ([]Ruleset,
  error)` (`SELECT ... FROM rulesets_read_model WHERE is_archived = false ORDER BY name` — no
  parent filter, matching `ListByUniverse`'s shape minus the `WHERE universe_id = $1` clause).
- `internal/query/user/repository.go` and `internal/query/universe/repository.go` each gain
  the identical `ListAll(ctx) ([]User/Universe, error)` method (same query shape as above,
  against `users_read_model`/`universes_read_model`).
- `internal/query/campaign/repository.go`: `Campaign` DTO gains `RulesetID uuid.UUID`; `Get`
  and `ListByUniverse`'s `SELECT`/`Scan` calls gain `ruleset_id`/`RulesetID`.

## HTTP API surface

**Command** (`api/command/openapi.yaml` + `internal/httpapi/command/server.go`):
```
POST   /rulesets                          { name, description?, references? }  -> { id }
PATCH  /rulesets/{rulesetId}               { name }                             -> 204  (rename)
PUT    /rulesets/{rulesetId}/description   { description }                      -> 204
PUT    /rulesets/{rulesetId}/references    { references }                      -> 204
POST   /rulesets/{rulesetId}/archive                                            -> 200  (idempotent)
```
`CreateCampaignRequest` gains required `rulesetId` (uuid). `Server` struct gains a
`ruleset *rulesetcmd.Service` field; `classify()` in `internal/httpapi/command/errors.go`
gains cases for `ruleset.ErrArchived` (409) and `ruleset.ErrNameRequired` (422), matching
Entity's exact two lines.

**Query** (`api/query/openapi.yaml` + `internal/httpapi/query/server.go`):
```
GET /rulesets           -> [Ruleset]   (new, list-all)
GET /rulesets/{rulesetId} -> Ruleset
GET /users              -> [User]      (new, list-all)
GET /universes          -> [Universe]  (new, list-all)
```
`Ruleset` DTO: `{id, name, description, references: [string], isArchived}`. `Campaign` DTO
gains required `rulesetId`. `Server` struct gains a `ruleset *rulesetquery.Repository` field.

## Wiring (all three binaries + CLI)

- `cmd/command-api/main.go`: register `rulesetevents.Register(registry)`; construct
  `rulesetRepo`/`rulesetService` **before** `campaignRepo`/`campaignService` (campaign's
  constructor now needs `rulesetRepo`); pass `rulesetRepo` into `campaigncmd.NewService(...)`;
  pass `rulesetService` into `httpcommand.NewServer(...)`.
- `cmd/query-api/main.go`: construct `rulesetquery.NewRepository(pool)`; pass into
  `httpquery.NewServer(...)`.
- `cmd/projector/main.go`: append `rulesetprojection.NewProjector()` to the projectors slice.
- `internal/cliapp/ruleset.go` (new file): `create ruleset <name> <description>
  <reference>...` (`cobra.MinimumNArgs(2)`), `rename ruleset <rulesetId> <name>`,
  `archive ruleset <rulesetId>`, `get ruleset <rulesetId>`, `list ruleset` (bare, no parent
  flag), `set description <rulesetId> <description>` and `set references <rulesetId>
  <reference>...` under the existing shared `setCmd` (alongside `set player`).
- `internal/cliapp/campaign.go`: `create campaign` becomes
  `Use: "campaign <name> <rulesetId> <gamemasterUserId>..."`,
  `Args: cobra.MinimumNArgs(3)`, body gains `"rulesetId": args[1]`,
  `gamemasterUserIds` becomes `args[2:]`.
- `internal/cliapp/user.go`: new `list user` command (bare, `GET /users`).
- `internal/cliapp/universe.go`: new `list universe` command (bare, `GET /universes`,
  distinct from the existing `list creator`/`list campaign`).

## Docs

`docs/PLAN.md` needs updates to: §2 (domain hierarchy diagram — add Ruleset, note Campaign's
new reference), §4.3 (cross-aggregate validation pseudocode — add the Ruleset check),
§4.5 (event catalog table — add Ruleset row, update Campaign's event payload), §8 (command API
route table — add Ruleset paths, update Campaign's create body), §9 (query API route table —
add `GET /rulesets`, `GET /rulesets/{id}`, `GET /users`, `GET /universes`), and §14 (CLI
reference — add Ruleset's command set, note the new bare list commands).

## Consequential fix: `test/e2e/e2e_test.go`

`rulesetId` becomes a required field on `CreateCampaignRequest`, so the existing e2e suite's
Campaign-creation step will start failing (400/422) the moment this ships. The plan must
include creating a Ruleset as part of the existing six-aggregate round-trip test (before the
Campaign create step) and passing its id through — this keeps the "one of each aggregate"
test honest by also covering the seventh aggregate type, not just unblocking Campaign creation.

## Testing / verification

- New `internal/domain/ruleset/ruleset_test.go`: `TestNew` (name required; description/
  references default-empty when omitted, arbitrary values accepted), `TestRenameAndArchive`
  (idempotent archive, rename fails when archived — copy `entity_test.go`'s exact structure),
  `TestSetDescriptionAndSetReferences` (mutate succeeds pre-archive, fails post-archive).
- `go build ./... && go vet ./... && go test ./...` clean.
- `go generate ./...` regenerates `api/command/gen`/`api/query/gen` with no diff-after-diff
  (i.e. running it twice produces the same output) once the OpenAPI specs are updated.
- Manual/e2e verification: `make test-e2e` passes with the updated round-trip test (creates a
  Ruleset, then a Campaign referencing it, confirms `GET /rulesets`, `GET /users`,
  `GET /universes` list endpoints return non-empty arrays including the just-created
  resources).
