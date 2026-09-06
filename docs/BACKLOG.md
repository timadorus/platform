# Backlog

Deferred and follow-up work identified during final-review passes on recent branches. Nothing
here blocks anything currently on `main` — each item was explicitly triaged as non-blocking and
parked rather than fixed in-branch. Pull an item out of here into its own spec/plan when picked
up; don't grow this file into a design doc.

## `cmd/` binary naming and organization

- [ ] **CLI-style tooling is scattered across multiple ad-hoc binaries instead of one
  consistently-named tool.** `cmd/timadorusctl` is this project's one binary with a "ctl" suffix,
  but it's scoped narrowly to HTTP-based customer/operator commands against command-api/query-api.
  Every other CLI-style task instead gets its own standalone binary: `test/e2e/cmd/devcluster`
  (spin up/tear down a local dev cluster) and `cmd/rebuild-read-models` (direct Postgres/NATS admin
  for a full read-model rebuild, added alongside the `statbudget-race-reconciliation` branch's
  projector-connection-budget work) are both operationally CLI tools in every real sense, just
  without the "ctl" naming or a shared home. As more ops/maintenance tasks accrue, this pattern
  would keep spawning new one-off binaries rather than growing one recognizable tool a new
  contributor would think to look for first. Worth a design pass on whether `timadorusctl` should
  absorb non-HTTP admin subcommands too (breaking its current "pure HTTP client" purity, but
  consolidating discoverability into one binary), or whether the convention should instead be "one
  `ctl` binary per privilege tier" (e.g. a separate `opsctl` for direct-DB/NATS admin tasks,
  keeping `timadorusctl` itself HTTP-only) — not resolved here, just flagged so the next tool added
  under `cmd/` doesn't repeat the same one-off-binary pattern without at least weighing this first.

## `timadorus-engine` (`internal/engine/timadorus`, `cmd/timadorus-engine`)

- [ ] **`TestReconciler_Character_CampaignStillHasNoBudget_LeftAlone` doesn't actually exercise the
  code path its name claims.** The test's outcome assertions (Character stays at version 1, `Info()`
  stays empty after a sweep) are correct and still a valid regression guard, but tracing the
  mechanics: `reconcileCharacters`'s own read-model pre-filter (`if config.CharacterCreation.
  MaxStatBudget == nil { continue }`, reading `campaigns_read_model.configuration`) intercepts this
  scenario and skips the Character before `backfillCharacter` — the specific line this test was
  meant to cover, `backfillCharacter`'s own `if config.CharacterCreation.MaxStatBudget == nil {
  return nil }` (checked fresh against the live aggregate, not the read model) — is never reached,
  since these unit tests use a bare Postgres pool with no live NATS/Router/projector, so nothing
  ever projects the aggregate's configuration into the read model during the test. Found by a
  scoped re-review during the `statbudget-race-reconciliation` branch's final-review fix wave;
  parked as Minor (the underlying production code is correct, confirmed by two independent
  reviews — this is a test-coverage precision gap, not a functional defect) rather than triggering
  another fix round. A real fix would need either a test that seeds a mismatched read-model/
  aggregate state (budget present when the aggregate is loaded, absent in the read-model scan) to
  force past the pre-filter, or a white-box internal test file calling `backfillCharacter` directly
  (mirroring `cache_test.go`'s own precedent for testing this package's private helpers without a
  full event-processing round trip).

- [ ] **`RulesetCache` never invalidates on Ruleset rename.** The doc comment on
  `internal/engine/timadorus/cache.go`'s `RulesetCache` now correctly *states* this trade-off,
  but the behavior itself is unfixed: renaming a Ruleset to or away from "Timadorus" via
  `PATCH /rulesets/{id}` has no effect on already-cached campaigns until the `timadorus-engine`
  pod restarts. Accepted for now; would need invalidation on `RulesetRenamed` if it ever matters
  in practice.

- [ ] **Architecture note for a 3rd aggregate type** (not a defect, just a heads-up): the
  extraction has now partially happened within `CampaignProcessor` itself —
  `CampaignProcessor.mutateConfiguration` is now shared between three call sites
  (`handleCampaignCreated`'s "traits"+"characterCreation" merge, `handleConfigurationRequested`'s
  recognized `setMaxStatBudget` branch, and that same handler's "configs"-append fallback for
  everything else), replacing what used to be a separate `appendConfigurationTimestamp`. This
  doesn't trip the "three earns the abstraction" threshold below, though — these three are three
  *callers* of one *already-shared* helper (each just passing its own `mutate` closure), not three
  copy-adapted bodies. The duplication this entry is actually tracking is a different axis: the
  ~35-line "parse opaque field → mutate under key K → save, swallowing `ErrArchived`" body itself,
  still copy-adapted between `CharacterProcessor.appendActionTimestamp` and
  `CampaignProcessor.mutateConfiguration` — still only two instances. If this trigger-endpoint
  pattern (`Request*`/creation event → an engine processor conditionally mutating the aggregate)
  gets reused a third time, these two survivors should get extracted into a shared helper over a
  small interface. Two instances of copy-adapt is correct by this codebase's own convention; three
  would earn the abstraction.

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

- [ ] **`setMaxStatBudget` silently falls through to the `configs`-append fallback on a
  non-integer `value`.** `configureAction.Value` is declared `int`, so a hand-crafted
  `PUT .../configure` payload like `{"action":"setMaxStatBudget","value":40.5}` fails to decode
  into it, and `handleConfigurationRequested` silently treats the whole action as unrecognized —
  appending a timestamp to `configs` instead of updating `characterCreation.maxStatBudget`, with
  no error or log signal anywhere. The SPA itself can no longer produce this payload (the
  `campaign-max-stat-budget` branch's final-review fix wave added a client-side
  `Number.isInteger` guard), so this is only reachable via `curl` or the CLI's generic `action`
  verb, not through the app. A real fix would decode `value` as `json.Number` (or `float64`) and
  reject/log a non-integer explicitly instead of silently falling through. Separately, and not
  fixed either: a *missing* `value` key decodes to Go's zero value (`0`), a valid `int` — so
  `maxStatBudget` silently becomes `0` rather than erroring, a narrower gap than the non-integer
  case above and not caught by the same fix.

- [ ] **Embedding-size threshold for future table files is undocumented.** This branch's design
  embeds `traits.yaml` directly via `//go:embed`, appropriate for a small, secret-free,
  `//go:embed`-able file the binary can't start without. But the design spec calls this "the
  first of an extensive set of such files" without saying at what size or count embedding stops
  being appropriate and a different content-delivery approach (a seeded pipeline, external
  config, etc.) becomes warranted. Note this so whoever adds the next table file (or the fifth,
  or the twentieth) has somewhere to weigh that judgment call rather than rediscovering it.

- [x] **Fixed** (`333f8c8`). `ruleset_tables_read_model`'s primary key now includes a `content_hash` column
  (`0002_content_hash_versioning.up.sql`). `RegisterTables` computes a sha256 of each row's
  marshaled content, so an edited row's new content inserts as an additional, newer row instead of
  being silently discarded by the old `ON CONFLICT (ruleset_id, table_name, row_key) DO NOTHING`.
  `internal/query/rulesettables.Repository.List`/`Get` always resolve the newest row per key by
  `updated_at`, so callers still see exactly one row per key — the current one.

- [x] **Fixed** (`fb907ad`). `test/e2e/e2e_test.go` now has a dedicated `It` asserting
  `GET /rulesets/{timadorusRulesetId}/tables/traits` returns all 3 seeded rows and
  `GET .../tables/traits/strong` returns the expected row content, resolving the "Timadorus"
  Ruleset by name from `GET /rulesets` rather than assuming a fixed id — covering the startup
  sync, both endpoints, and the migration image all in one test.

## Web SPA (`web/src`)

- [ ] **Character detail page: cosmetic table-column jitter in the Base Info card.** At some
  viewport widths (e.g. ~1400px) the table's `auto` layout re-measures column widths per state, so
  "Archive Character" wraps onto two lines in the default state but not when the Reassign Player
  picker row is open, and "Character Name" wraps in one state but not the other. Verified live via
  screenshot comparison across states. Purely visual; the design spec prescribed this exact table
  markup, so this is polish for a future pass, not a deviation from the spec.

- [ ] **`web/e2e`'s mock backend covers only the campaign-workspace page tree and Character
  creation — a new test needs new route arms.** `installMockBackend`'s state shape
  (`createMockState`) is genuinely general-purpose (arrays, `Partial` overrides, no hardcoded ids),
  but its route table is scenario-shaped: no `GET /universes` (list), no
  `GET /universes/{id}/campaigns`, no `GET /rulesets` (list), no single-entity/object GETs, and no
  command other than create-Character — all now honestly documented in the function's own doc
  comment rather than overstated. A future test exercising the Universe/Campaign picker screens, or
  any other command, needs to add the matching `matchPath` arm(s) first — each is a small, additive
  change (~5 lines) following the existing pattern, not a rewrite. Unmocked GETs return `[]`
  (by design); unmocked commands now fail loudly with a `501` (fixed in the final-review fix wave)
  rather than silently succeeding, so a missing arm surfaces immediately at the actual gap.

- [ ] **The poll-with-timeout pattern is now duplicated four times.** `useUsers.ts`'s `waitForUser`,
  `useCharacters.ts`'s `waitForCharacter` and `waitForCharacterInList`, and `useEntities.ts`'s
  `waitForEntityInList` all share an identical skeleton — the same 750ms/15000ms defaults, the same
  `opts` shape, the same `for (;;)` loop, the same pre-await/post-await abort guards, the same
  deadline check — varying only in which fetch to call and what counts as success. Recommend
  extracting a shared `pollUntil` helper (e.g. `web/src/composables/usePolling.ts`) once a fifth
  copy is needed — the design spec for `character-creation-eventual-consistency` already
  anticipates Universe/Campaign/Object creation having the same latent read-model-lag exposure, and
  a fifth copy is the trigger to extract, not a requirement to do it now.

- [ ] **A freshly created Campaign has no retry/timeout handling for read-model lag, unlike
  Character creation.** `CampaignPickerView.vue`'s `onCreated(id)` navigates straight to the
  Campaign workspace, which lands by default on `CampaignOverviewPanel.vue` — so a lagging read
  model shows the dead-end "Campaign not found." with no Retry, unlike the
  `character-creation-eventual-consistency` pattern (`waitForCharacter` plus
  `CharacterDetailView`'s Retry/Back-to-Campaign UI). `WorkspaceView.vue`'s own `getCampaign` call
  has the identical exposure, which would leave the header badge blank instead. This is unchanged,
  pre-existing behavior — `campaign-tabbed-panel` did not regress it — and deferring it was a
  deliberate, reasonable scope decision (the panel "wasn't reported broken"). Recorded here, per
  the poll-with-timeout entry above, so this specific instance of the latent exposure it already
  anticipates for Campaign creation does not quietly evaporate as untracked follow-up.

- [ ] **`npm run typecheck` is a no-op and has been for some time.** `web/tsconfig.json` is a
  solution-style config with `"files": []`, so `vue-tsc --noEmit` run against it checks zero files
  and exits 0 in ~0.2s regardless of real type errors — every past plan's "typecheck must be clean"
  gate has been vacuous; the actual type coverage has always ridden along inside `npm run build`'s
  `vue-tsc -b`. Not fixed here because correcting it (e.g. `"typecheck": "vue-tsc -b --noEmit"`)
  could surface a wave of pre-existing, unrelated type errors across `web/src` that have silently
  accumulated — a separate, dedicated fix, not a one-liner to fold into an unrelated branch.

- [ ] **The `pendingEntityId` provide/inject pair has a two-way-coupling wart.**
  `CharactersPanel.vue` writes it (sets the new Entity's id); `EntitiesPanel.vue` also writes to it
  (clears it back to `null` once its poll resolves) — both panels have write access to a ref
  neither owns. Harmless today (only one producer/consumer pair exists), but if a second
  `pendingXId`-shaped need ever arises, that's the signal to replace this narrow one-off with a
  single richer shared signal payload (e.g. `sidebarEvent: Ref<{ kind: string; id: string } | null>`)
  rather than accumulating more one-off refs on `WorkspaceView.vue`.

- [ ] **Three independent 750ms polls now fire after one Character creation** (the Characters
  sidebar, the Entities sidebar, and the main pane), each with no shared coordination — roughly
  tripling the SPA's request rate against a lagging backend for up to 15 seconds, precisely when it
  is already struggling. Acceptable at this scale and an inherent consequence of the current design
  (three independent consumers), not a defect to fix now — just a property worth knowing about if
  this polling pattern is reused elsewhere.

- [ ] **A genuinely nonexistent Character id takes the full 15 seconds to report as such.**
  Navigating to a stale bookmark or a hand-typed bad `characterId` shows "Loading…" for the full
  timeout before the Retry/Back-to-Campaign UI appears, since `waitForCharacter` cannot distinguish
  "not yet projected" from "will never exist" (both 404 identically). This is an accepted,
  documented trade-off from the `character-creation-eventual-consistency` design spec, not an
  oversight — recorded here so the cost is visible in one place alongside the rest of this
  feature's known limitations.

- [ ] **`CampaignPickerView.goTo` never records the selected Universe, unlike its sibling
  `UniverseOverviewPanel.goToCampaign` — a deep link can silently clear a user's restored Campaign
  selection.** `CampaignPickerView.vue`'s `goTo()` calls only `selection.setCampaign(id)`, while
  `UniverseOverviewPanel.vue`'s `goToCampaign()` (fixed in an earlier branch) correctly calls
  `selection.setUniverse(universeId)` first — required ordering, since `setUniverse` nulls the
  stored `selectedCampaignId` (`web/src/stores/selection.ts`), so campaign-after-universe is the
  only correct order. In the ordinary flow the two entry points agree, because `campaign-picker` is
  normally reached via `UniversePickerView.goTo`, which already called `setUniverse`. They diverge
  on a deep link: land directly on `/universes/u2` while `localStorage` holds
  `selectedUniverseId: 'u1'`. `CampaignPickerView`'s `onMounted` sees `selection.selectedUniverseId
  ('u1') !== universeId.value ('u2')` and falls through without recording `u2`; creating a Campaign
  there then persists the mismatched pair `{universe: u1, campaign: <a u2 campaign>}`. On the next
  cold boot, `UniversePickerView` restores `u1`, routes to `u1`'s campaign picker, and
  `CampaignPickerView` tries to load the stored campaign, finds `existing.universeId !==
  universeId.value`, and clears it — so the user's restored Campaign selection is silently lost.
  This is pre-existing behavior in an unchanged file, out of scope for the branch
  (`universe-panel-create-campaign`) that surfaced it, since that branch's own new entry point
  already does the right thing. Recommended fix: add `selection.setUniverse(universeId.value)` to
  `CampaignPickerView.goTo` before the existing `setCampaign` call — one line, matching
  `UniverseOverviewPanel.goToCampaign`'s shape exactly — plus a deep-link regression test. Small
  follow-up branch.

- [ ] **`CreateCampaignModal.vue`'s labels have no `for`/`id` pairing with their inputs — a real
  accessibility gap that has now also forced two separate test-writing passes to route around
  `page.getByLabel(...)` not working.** Its `<label>` elements are bare, with no `for`, and the
  `<input>`/`RulesetSelect`/`UserMultiSelect` have no matching `id`, so `getByLabel` cannot locate
  them; `universe-manage.spec.ts` falls back to `page.locator('form').getByRole(...)` instead,
  which is unscoped and strict-mode-dependent (works today only because no other `<form>` or
  input-bearing widget is mounted in that test). Fixing the pairing (plus adding `role="dialog"` to
  `BaseModal.vue`) would resolve both the accessibility gap and the test brittleness at the source,
  restoring `getByLabel` for every future test against every modal in the app. Out of scope for
  `universe-panel-create-campaign` per its plan's Global Constraints.

- [ ] **Universe-level change-feed events currently have no live SPA consumer.**
  `UniverseOverviewPanel.vue` sits outside `WorkspaceView`'s provide scope (it's a top-level route,
  not a child route of `WorkspaceView`), so `lastAggregateChange` is never injected there and the
  panel never reacts to `universe`-aggregate changes. Wire it up (its own `useChangeFeed` instance
  scoped to its own `universeId`, or hoist the `provide` to a shared ancestor) if a real need for
  it surfaces.

## Devcluster tooling (`test/e2e/internal`)

- [ ] **No `TraefikServiceName` exported constant.** `seed.go` and `up.go` both hardcode the
  literal `"traefik"` string independently. Cheap to fix whenever either file is next touched.

- [ ] **Seed retry budget is an untested heuristic.** `seedHTTPDoWithRetry`'s 6-attempt/2s-backoff
  budget was tuned against observed Traefik routing-sync delay, not derived from a proven bound.
  Constants are isolated at the top of the function for easy tuning if a slower environment ever
  needs more headroom.

## projector

- [ ] **`cmd/projector` now runs 12 projectors on a default-sized connection pool with no budget
  note.** `cmd/timadorus-engine/main.go` carries an explicit "Connection budget" comment for its 2
  processors; `cmd/projector/main.go`'s `pgxpool.New` has no equivalent, and the
  `universe-change-feed` branch took it from 7 to 12 projectors (a 71% increase in concurrent
  connection demand) with no explicit `pool_max_conns` (defaults to `max(4, NumCPU)`). Not a
  correctness bug today — each `Router.handle` holds exactly one connection and the
  `universechanges` projectors resolve on the ambient tx rather than acquiring a second pool
  connection, so there's no deadlock risk — but on a small node a simultaneous cold-start replay
  of all 12 could in principle queue long enough to trip Watermill's 30s `AckWaitTimeout` and
  cause redelivery churn. Add a "Connection budget" comment near `cmd/projector/main.go`'s
  `pgxpool.New` mirroring the engine's, and consider `pool_max_conns` if this is ever measured to
  matter in practice.

- [ ] **A full read-model rebuild (all checkpoints reset) is not safe with the change-feed
  projectors in the mix.** Replay load at this platform's current scale is fine (cheap indexed
  lookup + insert per event, small backlogs). But if every checkpoint were ever reset to rebuild
  read models from scratch, the `universe-changes-*` projectors would race the base projectors
  with no ordering guarantee between independent durables, and the 5-attempt Nack budget (no
  `NakDelay`) would burn in milliseconds — non-`Created` events would dead-letter en masse and
  their change rows would be lost. Rebuild base read models first, then reset the
  `universe-changes-*` checkpoints, if a full rebuild is ever needed.

- [x] **Already fixed** (`05a84b7`, predates this entry). `.github/workflows/ci.yml`'s `web-build`
  job already has a "verify generated API clients are up to date" step: `npm run generate` followed
  by `git diff --exit-code -- src/api/command.types.ts src/api/query.types.ts`. This entry was
  simply never reconciled against that existing check — no code change needed.
