# timadorus-engine Self-Registers the "Timadorus" Ruleset — Design

## Context

`timadorus-engine` (`internal/engine/timadorus`, `cmd/timadorus-engine`) only reacts to Character
and Campaign trigger events whose Campaign's Ruleset is named "Timadorus" (case-insensitive). Today
that Ruleset has to exist before anyone can create such a Campaign, and the only thing that creates
it is `test/e2e/internal/seed.go`'s `SeedPlatformData`, called by `make dev-up` — a direct HTTP POST
to the command API's `/rulesets` endpoint, functionally equivalent to `timadorusctl create ruleset`
but not the CLI binary itself.

This spec makes the engine register that Ruleset itself, at startup, and removes the now-redundant
seeding step from `dev-up`.

## Problem: no uniqueness enforcement exists yet

`ruleset.New()` always creates a new aggregate with a fresh UUID — nothing anywhere in the platform
detects or rejects a duplicate Ruleset name today (`seed.go`'s own doc comment says as much: "User/
Ruleset names carry no uniqueness constraint at the domain level"). For the engine to "attempt to
register, accepting the correct error code if it already exists," that error code has to exist
first. Per discussion, this is built as a **real, global constraint** — `POST /rulesets` becomes
conflict-safe for the CLI and web SPA too, not just for the engine's own startup call.

## 1. Uniqueness enforcement (domain + command layer)

- New sentinel `ruleset.ErrNameAlreadyExists` in `internal/domain/ruleset/errors.go`.
- New Postgres table `ruleset_names (name TEXT PRIMARY KEY)` — a name reservation, not a read
  model. It has no relationship to `rulesets_read_model` (which stays owned by
  `internal/projection/ruleset`, populated asynchronously). Owned by a new migration directory
  `internal/command/ruleset/migrations/`, registered as a new schema owner (`command_ruleset`) in
  `scripts/migrate-up.sh`'s existing list — the same "one migration directory per schema owner"
  convention every projection already follows.
- `ruleset.Service.Create` gains a `pool *pgxpool.Pool` dependency (mirroring
  `character.Service`'s own `pool` field, used identically: to open a `postgres.UnitOfWork`
  spanning more than one write). Inside one transaction: `INSERT INTO ruleset_names(name)
  VALUES ($1)`, then `Save` the new aggregate through the same transaction — both commit or both
  roll back together. Domain validation (`ErrNameRequired`) still runs first, before any DB access,
  exactly as today.
- A unique-violation on that INSERT becomes `ErrNameAlreadyExists`; no aggregate is created and no
  event is appended.
- **Names are never released**, even if that Ruleset is later archived — consistent with this
  codebase's "archive, don't delete, history never regresses" rule elsewhere. This is a deliberate,
  simple default: revisit only if reusing an archived Ruleset's name ever becomes a real need.
- Uniqueness is **exact-string, case-sensitive**. The engine's own case-*insensitive* "does this
  campaign's Ruleset match 'timadorus'" business logic (`targetRulesetName` in
  `character_processor.go`/`campaign_processor.go`) is a separate, unrelated concern and is
  unaffected — "Timadorus" and "timadorus" would still both trigger the engine if both existed,
  exactly as today; this spec does not change that.
- Two small, reusable additions to `internal/eventstore/postgres`, needed to implement the above:
  - Export the unique-violation check the store already performs internally for
    optimistic-concurrency conflicts, as `IsUniqueViolation(err error) bool`, and have
    `appendWith` use it too (no behavior change, removes a near-duplicate check).
  - Rename the unexported `txFromContext` to exported `TxFromContext`, so a command service that
    already opened a `postgres.UnitOfWork` can retrieve its own transaction to run raw SQL (the
    reservation insert) inside it. One call site (`store.go`'s `Append`) updates accordingly.

## 2. HTTP surface

- `classify()` in `internal/httpapi/command/errors.go` gets one new case:
  `errors.Is(err, ruleset.ErrNameAlreadyExists) → 409, "name_already_exists"`.
- `CreateRuleset`'s handler (`internal/httpapi/command/server.go`) gets a `case 409` branch
  returning `gen.CreateRuleset409ApplicationProblemPlusJSONResponse`, matching the existing
  `RenameRuleset`/`SetRulesetDescription` 409 branches' shape exactly.
- `api/command/openapi.yaml`'s `POST /rulesets` gains `"409": $ref:
  "#/components/responses/Conflict"` (the same shared `Conflict` response every other 409-capable
  endpoint already reuses) — then `go generate ./...` regenerates `api/command/gen`.
- One new e2e assertion (`test/e2e/e2e_test.go`, which already creates a Ruleset named
  `"e2e-ruleset"`): POST that same name again, expect `409`.
- No CLI (`internal/cliapp/ruleset.go`) change needed: `internal/cliapp/client.go`'s
  `handleResponse` already prints any non-2xx `problem+json` body to stderr generically — a 409
  from `create ruleset` surfaces automatically, the same way every other command's 409 already
  does.

## 3. Engine self-registration

New `internal/engine/timadorus/register.go`:

```go
const TargetRulesetName = "Timadorus"

func RegisterRuleset(ctx context.Context, pool *pgxpool.Pool) error {
    // builds its own scoped registry/store/repo/service, mirroring exactly how
    // NewCharacterProcessor/NewCampaignProcessor already build their own scoped repos
    ...
    _, err := service.Create(ctx, TargetRulesetName, "", nil)
    if errors.Is(err, ruleset.ErrNameAlreadyExists) {
        return nil
    }
    return err
}
```

`cmd/timadorus-engine/main.go`'s `run()` calls `RegisterRuleset(ctx, pool)` right after building the
connection pool, **before** starting the observability HTTP server or the projection router. A
registration failure returns from `run()`, which `main()` already logs and turns into `os.Exit(1)`
— so the process terminates before `/readyz` ever starts listening, and before it consumes any
events. `errors.Is(err, ruleset.ErrNameAlreadyExists)` is the *only* outcome treated as success
besides a clean create; every other error (validation failure, a real DB problem) is fatal.

## 4. Removing the dev-up seeding

`timadorus-engine` is already deployed as a Helm-managed pod in the dev cluster
(`deploy/helm/timadorus-platform/templates/timadorus-engine-deployment.yaml`), and its readiness
probe hits `/readyz`, which only starts listening after `RegisterRuleset` succeeds (§3). Since
`test/e2e/cmd/devcluster/up.go` calls `helm upgrade --wait` before ever calling
`SeedPlatformData`, the Ruleset is guaranteed to already exist by the time seeding runs.

- `test/e2e/internal/seed.go`: delete the `ensureSeedResource(..., "/api/command/rulesets",
  SeedRulesetName)` call and its error-wrapping `if`. Keep the `SeedRulesetName` constant
  (`up.go`'s status message still needs the literal) but re-point its doc comment at the engine's
  own registration instead of claiming `seed.go` creates it.
- Doc comments in `seed.go` that jointly justify the User+Ruleset check-then-create dance (the
  `SeedPlatformData` doc comment, and `ensureSeedResource`'s "User/Ruleset names carry no
  uniqueness constraint" sentence) get trimmed to describe User only, since Ruleset creation is
  leaving this file entirely — and, per §1, that sentence would now be inaccurate for Ruleset
  regardless.
- `up.go`'s `printStatus`: currently prints `"Pre-seeded: User %q, Ruleset %q.\n\n"` gated on
  `SeedPlatformData`'s own success. Split this: the User half stays gated on `seeded`; the Ruleset
  is mentioned unconditionally (it's the engine's concern now, decoupled from whether the User-seed
  HTTP call happened to succeed), attributed to the engine rather than to dev-up.

## 5. Testing

- `internal/command/ruleset/service_test.go` (new — the first test file for any
  `internal/command/*` package; testcontainers, mirroring `internal/eventstore/postgres/
  store_test.go`'s existing pattern): create succeeds; a duplicate name returns
  `ErrNameAlreadyExists` and creates no second aggregate/event; an empty name still returns
  `ErrNameRequired` without ever touching `ruleset_names`.
- `internal/engine/timadorus/register_test.go` (new — reuses `testutil_test.go`'s shared
  `newTestPool`, whose migration list gains the one new `command/ruleset` migration file): first
  call creates the Ruleset (checked via the `events`/`ruleset_names` tables — this package's tests
  never run the async projector, so `rulesets_read_model` stays empty and is not what these tests
  check); a second call is a no-op (still exactly one `RulesetCreated` event); a genuine failure
  (a closed pool) propagates as a real error, not swallowed as `ErrNameAlreadyExists`.
- `test/e2e/e2e_test.go`: the one new 409-on-duplicate-name assertion from §2.

## Verification

- `go build ./... && go vet ./...` clean.
- `go generate ./...` regenerates `api/command/gen` with no other diff.
- `go test ./...` green, including the two new testcontainers-backed test files.
- `go test -tags e2e ./test/e2e/...` green (requires the dev cluster / docker-compose environment
  this repo's e2e suite already assumes) — not run as part of every task, but checked before this
  branch finishes if that environment is available.
- Manual/live check via `make dev-up` against a fresh cluster: confirm the "Timadorus" Ruleset
  exists (via `GET /api/query/rulesets`) without `seed.go` having created it, and confirm a second
  `make dev-up` against the same cluster still succeeds (the engine's own restart hits the
  already-exists path, not a crash loop).
