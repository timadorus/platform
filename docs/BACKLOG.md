# Backlog

Deferred and follow-up work identified during final-review passes on recent branches. Nothing
here blocks anything currently on `main` — each item was explicitly triaged as non-blocking and
parked rather than fixed in-branch. Pull an item out of here into its own spec/plan when picked
up; don't grow this file into a design doc.

## `timadorus-engine` (`internal/engine/timadorus`, `cmd/timadorus-engine`)

- [ ] **No real concurrency test for the shared `RulesetCache`.**
  `character_processor_test.go` and `campaign_processor_test.go` each define their own Postgres
  testcontainer setup and duplicate helpers (`mustMarshal`/`mustMarshalCampaign`,
  `discardLogger`/`discardCampaignLogger`) purely to dodge name collisions in the shared
  `timadorus_test` package. `TestSharedRulesetCache_ServesBothProcessors` also hand-rolls an
  inline `CREATE TABLE characters_read_model` instead of reusing the real migration files under
  `internal/projection/character/migrations/` — that will silently drift from the production
  schema the next time that table changes. More importantly, that test only proves
  **key-sharing** (publish one event, wait for it, then publish the second) — it never exercises
  two goroutines touching the shared cache/connection pool *at the same time*, so the concurrency
  the cache's mutex exists to protect has never actually been tested under `-race`.
  Follow-up: one shared test-pool helper across both files, driven by real migrations; add a
  genuinely concurrent (publish-both-before-waiting, run under `-race`) variant.

- [ ] **Connection pool headroom.** `cmd/timadorus-engine/main.go`'s `pgxpool.New` has no
  explicit `pool_max_conns` (defaults to `max(4, NumCPU)`). Two processors now share one pool,
  and each in-flight `Handle` can hold up to 2 connections at once (the Router's transaction +
  the aggregate's `Load`, which reads via the pool, not the ambient tx) — so 2 processors already
  peak at the default floor, with `/readyz`'s own health-check `Ping` as a 5th consumer under
  load. Documented in a comment near `pgxpool.New`, not yet fixed. A 3rd processor sharing this
  binary would need an explicit `pool_max_conns` bump.

- [ ] **`RulesetCache` never invalidates on Ruleset rename.** The doc comment on
  `internal/engine/timadorus/cache.go`'s `RulesetCache` now correctly *states* this trade-off,
  but the behavior itself is unfixed: renaming a Ruleset to or away from "Timadorus" via
  `PATCH /rulesets/{id}` has no effect on already-cached campaigns until the `timadorus-engine`
  pod restarts. Accepted for now; would need invalidation on `RulesetRenamed` if it ever matters
  in practice.

- [ ] **Architecture note for a 3rd aggregate type** (not a defect, just a heads-up): if this
  trigger-endpoint pattern (`Request*` → `*Requested` event with a no-op `Apply` → an engine
  processor conditionally mutating the aggregate) gets reused a third time, the ~35-line "parse
  opaque field → append timestamp under key K → save, swallowing `ErrArchived`" body — currently
  copy-adapted between `CharacterProcessor.appendActionTimestamp` and
  `CampaignProcessor.appendConfigurationTimestamp` — should get extracted into a shared helper
  over a small interface. Two instances of copy-adapt is correct by this codebase's own
  convention; three would earn the abstraction.

## Web SPA (`web/src`)

- [ ] **`CreatingUserModal` has no unmount-cancellation** on its polling loop
  (`waitForUser` in `useUsers.ts`). Bounded to a 15s timeout, so low impact if the modal is
  dismissed early, but a stray timer does keep running against an unmounted component until it
  resolves or times out.

- [ ] **`waitForUser` swallows fetch errors.** It calls `useUsers().list()` in a loop but never
  inspects the composable's own `error` ref — a hard network/auth failure looks identical to
  "still waiting for the projector" until the timeout fires, and nothing is surfaced to
  `CreatingUserModal` to distinguish the two.

- [ ] **Generated API client types are stale.** `web/src/api/query.types.ts` and
  `command.types.ts` don't reflect the Character `action` endpoint or the Campaign
  `configuration`/`configure` endpoints — `npm run generate` (the openapi-typescript codegen
  script) was never re-run after those backend changes shipped. Real, accumulating drift for
  anyone building UI against these; explicitly wave-through'd as out of scope on each backend
  branch so far.

## Devcluster tooling (`test/e2e/internal`)

- [ ] **No `TraefikServiceName` exported constant.** `seed.go` and `up.go` both hardcode the
  literal `"traefik"` string independently. Cheap to fix whenever either file is next touched.

- [ ] **Seed retry budget is an untested heuristic.** `seedHTTPDoWithRetry`'s 6-attempt/2s-backoff
  budget was tuned against observed Traefik routing-sync delay, not derived from a proven bound.
  Constants are isolated at the top of the function for easy tuning if a slower environment ever
  needs more headroom.
