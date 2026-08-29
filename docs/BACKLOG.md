# Backlog

Deferred and follow-up work identified during final-review passes on recent branches. Nothing
here blocks anything currently on `main` — each item was explicitly triaged as non-blocking and
parked rather than fixed in-branch. Pull an item out of here into its own spec/plan when picked
up; don't grow this file into a design doc.

## `timadorus-engine` (`internal/engine/timadorus`, `cmd/timadorus-engine`)

- [ ] **`TestSharedRulesetCache_ConcurrentAccess` has a real but unreliable race window.**
  `go test -race` is now wired into CI and `make test-race`, and the test genuinely runs
  `CampaignProcessor.Handle` and `CharacterProcessor.Handle` concurrently against one
  `RulesetCache` (released via a shared `close(start)` barrier, not sequenced) — this closed the
  original "never tested under `-race`" complaint at the wiring level. But a scoped re-review
  verified the test's actual power by temporarily deleting `RulesetCache.get`/`set`'s mutex calls
  entirely (the most direct possible regression) and running the test 28 times under `-race`:
  zero races were caught. `CharacterProcessor.Handle`'s extra `characters_read_model` DB hop before
  it calls `resolve` appears to reliably let the Campaign path populate the cache first in this
  environment, so the two goroutines' actual accesses to `RulesetCache.names` rarely truly overlap.
  Follow-up: a lower-level unit test that calls `RulesetCache.get`/`set` directly from N goroutines
  with no DB round-trip in the way, if the team wants `-race` to reliably catch a `RulesetCache`
  locking regression specifically rather than relying on whatever race happens to manifest
  elsewhere in the suite.

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

- [ ] **No backfill for pre-existing Ruleset names — an in-place cluster upgrade gets a
  duplicate "Timadorus" Ruleset.** `internal/command/ruleset/migrations/0001_ruleset_names.up.sql`
  creates the `ruleset_names` reservation table empty. On a cluster that already had a
  "Timadorus" Ruleset before this table existed (e.g. from the old `test/e2e` seed step), the
  reservation table starts with no knowledge of it, so `RegisterRuleset`'s next startup reserves
  "Timadorus" successfully and creates a second, distinct Ruleset aggregate with that name — two
  identical "Timadorus" entries in `rulesets_read_model` and the SPA's picker. Accepted as a known
  gap rather than fixed: resetting an existing dev cluster (`make dev-down && make dev-up`)
  avoids it entirely, so it was judged not worth a backfill for this branch. A real fix would need
  a backfill migration that derives each existing Ruleset's current name from its
  `RulesetCreated` event plus the latest `RulesetRenamed` event per aggregate (non-trivial, since
  the current name isn't stored anywhere but the event stream itself) and inserts it into
  `ruleset_names` with `ON CONFLICT DO NOTHING`.

- [ ] **`Rename` bypasses the `ruleset_names` reservation entirely, so it and the event store can
  disagree — including in a way that silently defeats `RegisterRuleset`.**
  `ruleset.Service.Rename` does a plain `Load` → `Rename` → `Save` and never touches
  `ruleset_names`, unlike `Create`, which reserves the name in the same transaction as the save.
  Consequences: (1) renaming a Ruleset from "A" to "B" leaves "A" reserved with no aggregate
  bearing that name, and leaves "B" completely unreserved, so a later `Create("B")` succeeds and
  produces a duplicate "B"; (2) renaming the "Timadorus" Ruleset away leaves the "Timadorus"
  reservation in place, so every future `RegisterRuleset` call gets `ErrNameAlreadyExists` and
  treats the platform as already registered, even though no Ruleset is actually named
  "Timadorus" any more. The design spec scoped the uniqueness invariant to `Create` only and
  never claimed `Rename` coverage, so this is a spec-level gap rather than an implementation bug;
  `Service.Create`'s and `RegisterRuleset`'s doc comments now say so explicitly instead of
  overstating the guarantee. Fix sketch for a follow-up branch: make `Rename` reserve the new
  name in the same transaction as the `RulesetRenamed` event — a near-copy of `Create`'s
  structure — returning `ruleset.ErrNameAlreadyExists` on conflict, and, ideally, release the old
  name in that same transaction.

## Web SPA (`web/src`)

All three items previously listed here are fixed (`e7ad69c`, `e89a5f3`): `waitForUser` now takes
an `AbortSignal` that `CreatingUserModal` aborts on unmount, stopping the poll instead of letting
it run in the background; `waitForUser` also bails out immediately on a hard fetch error instead
of retrying for the full timeout, and the modal shows that error distinctly from an honest
timeout; and `query.types.ts`/`command.types.ts` are regenerated and current. All three verified
live with a real headless-Chromium session.

- [ ] **Character detail page (`character-detail-redesign`): a rejected rename discards the
  user's typed draft, and an empty rename is sent unguarded.** `BaseInfoTable.vue`'s `saveName()`
  collapses the inline editor immediately after emitting `submit-rename`, before the parent's
  `PATCH` call has resolved. Verified live with a mocked `400`: the error banner appears, the
  display reverts to the old name, and the text the user typed is gone — they must reopen the
  editor and retype from scratch to retry. Not data-damaging (the old name is never lost server-
  side), just a retry-ergonomics regression versus the previous always-editable input, which kept
  the attempted text in the box. Separately, and pre-existing rather than introduced by this
  branch, `saveName()` has no emptiness guard: clearing the field and clicking Save sends
  `PATCH {"name":""}` unguarded (verified live); the server rejects it and the banner explains, so
  nothing breaks, but the gap is now slightly more visible because of the lost-draft issue above.
  Both are accepted, conscious trade-offs for this branch rather than defects — a fix for the
  first costs an extra round trip (e.g. a `saving`/`error` prop from the parent, or the emit
  carrying a callback); a fix for the second is a two-line `v-if` guard in `saveName()`.

- [ ] **Character detail page: cosmetic table-column jitter in the Base Info card.** At some
  viewport widths (e.g. ~1400px) the table's `auto` layout re-measures column widths per state, so
  "Archive Character" wraps onto two lines in the default state but not when the Reassign Player
  picker row is open, and "Character Name" wraps in one state but not the other. Verified live via
  screenshot comparison across states. Purely visual; the design spec prescribed this exact table
  markup, so this is polish for a future pass, not a deviation from the spec.

- [ ] **Character detail page: the current Player's name is hidden while reassigning.**
  `BaseInfoTable.vue`'s Player cell only renders `{{ playerName }}` when `!editingPlayer`, so the
  moment a GM clicks "Reassign Player" the current Player's name disappears, replaced by the
  picker — there is no way to see who is being replaced without cancelling first. This is the
  design spec's own given markup, faithfully implemented; flagged here as a UX opportunity for a
  future pass rather than a defect against the plan.

- [ ] **Character detail page: the Stats tab's two cards don't stack on narrow viewports.** The
  Stats tab wraps `AttributesTable` and `BaseInfoTable` in `class="flex gap-4"` with no
  `flex-wrap`, so both `flex-1` cards compress side-by-side rather than stacking at narrow widths.
  Matches the spec's markup verbatim; noted because `BaseTabs` and the two tables are otherwise
  responsive-friendly.

## Devcluster tooling (`test/e2e/internal`)

- [ ] **No `TraefikServiceName` exported constant.** `seed.go` and `up.go` both hardcode the
  literal `"traefik"` string independently. Cheap to fix whenever either file is next touched.

- [ ] **Seed retry budget is an untested heuristic.** `seedHTTPDoWithRetry`'s 6-attempt/2s-backoff
  budget was tuned against observed Traefik routing-sync delay, not derived from a proven bound.
  Constants are isolated at the top of the function for easy tuning if a slower environment ever
  needs more headroom.
