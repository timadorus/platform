# Backlog

Deferred and follow-up work identified during final-review passes on recent branches. Nothing
here blocks anything currently on `main` — each item was explicitly triaged as non-blocking and
parked rather than fixed in-branch. Pull an item out of here into its own spec/plan when picked
up; don't grow this file into a design doc.

## `timadorus-engine` (`internal/engine/timadorus`, `cmd/timadorus-engine`)

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

All three items previously listed here are fixed (`e7ad69c`, `e89a5f3`): `waitForUser` now takes
an `AbortSignal` that `CreatingUserModal` aborts on unmount, stopping the poll instead of letting
it run in the background; `waitForUser` also bails out immediately on a hard fetch error instead
of retrying for the full timeout, and the modal shows that error distinctly from an honest
timeout; and `query.types.ts`/`command.types.ts` are regenerated and current. All three verified
live with a real headless-Chromium session.

## Devcluster tooling (`test/e2e/internal`)

- [ ] **No `TraefikServiceName` exported constant.** `seed.go` and `up.go` both hardcode the
  literal `"traefik"` string independently. Cheap to fix whenever either file is next touched.

- [ ] **Seed retry budget is an untested heuristic.** `seedHTTPDoWithRetry`'s 6-attempt/2s-backoff
  budget was tuned against observed Traefik routing-sync delay, not derived from a proven bound.
  Constants are isolated at the top of the function for easy tuning if a slower environment ever
  needs more headroom.
