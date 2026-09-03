# Backlog

Deferred and follow-up work identified during final-review passes on recent branches. Nothing
here blocks anything currently on `main` — each item was explicitly triaged as non-blocking and
parked rather than fixed in-branch. Pull an item out of here into its own spec/plan when picked
up; don't grow this file into a design doc.

## `timadorus-engine` (`internal/engine/timadorus`, `cmd/timadorus-engine`)

- [x] **Fixed** (`f8fadc6`). `TestRulesetCache_ConcurrentGetSet_Race` (`internal/engine/timadorus/cache_test.go`)
  drives `RulesetCache.get`/`set` directly from 50 goroutines against 3 shared keys, no DB
  round-trip or processor in the way — confirmed to actually catch a regression by temporarily
  deleting the cache's mutex calls and observing `go test -race` report a real data race, then
  reverting. `TestSharedRulesetCache_ConcurrentAccess` stays as-is; it still proves the two
  processors correctly share one cache instance end to end, just not reliably under `-race`.

- [x] **Fixed** (`eff7a90`). `cmd/timadorus-engine/main.go` now builds its pool via `pgxpool.ParseConfig` +
  `pgxpool.NewWithConfig`, setting `MaxConns` from the new `TimadorusEngine.PoolMaxConns` config
  field (`internal/config/config.go`, default 8, overridable via
  `TIMADORUS_ENGINE_POOL_MAX_CONNS`). A 3rd processor sharing this binary can now get headroom via
  config alone, no code change.

- [ ] **`RulesetCache` never invalidates on Ruleset rename.** The doc comment on
  `internal/engine/timadorus/cache.go`'s `RulesetCache` now correctly *states* this trade-off,
  but the behavior itself is unfixed: renaming a Ruleset to or away from "Timadorus" via
  `PATCH /rulesets/{id}` has no effect on already-cached campaigns until the `timadorus-engine`
  pod restarts. Accepted for now; would need invalidation on `RulesetRenamed` if it ever matters
  in practice.

- [ ] **Architecture note for a 3rd aggregate type** (not a defect, just a heads-up): the
  extraction has now partially happened within `CampaignProcessor` itself —
  `CampaignProcessor.mutateConfiguration` is shared between its own two handlers
  (`handleCampaignCreated`'s "traits" overwrite and `handleConfigurationRequested`'s "configs"
  append), replacing what used to be a separate `appendConfigurationTimestamp`. The remaining
  cross-processor duplication is the ~35-line "parse opaque field → mutate under key K → save,
  swallowing `ErrArchived`" body, still copy-adapted between
  `CharacterProcessor.appendActionTimestamp` and `CampaignProcessor.mutateConfiguration`. If this
  trigger-endpoint pattern (`Request*`/creation event → an engine processor conditionally
  mutating the aggregate) gets reused a third time, these two survivors should get extracted into
  a shared helper over a small interface. Two instances of copy-adapt is correct by this
  codebase's own convention; three would earn the abstraction.

- [ ] **No backfill for pre-existing Campaigns — default traits only apply going forward.**
  Campaigns created before this branch's engine is deployed get no default traits: the merge is
  triggered by `CampaignCreated`, not by a backfill pass, so only Campaigns created *after* the
  new engine is running are affected. Resetting `CampaignProcessor`'s checkpoint to force a
  "replay" is **not** a safe way to backfill existing Campaigns — replaying `CampaignCreated` for
  a Campaign that has also received `ConfigurationRequested` events since would re-run the
  non-idempotent "configs" append (see `mutateConfiguration`'s own doc comment) for every one of
  those historical events too, not just merge in the missing traits.

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

- [x] **Fixed** (`c97b9f6`). `ruleset.Service.Rename` (`internal/command/ruleset/service.go`) now reserves the
  new name and releases the old one in the same transaction as the `RulesetRenamed` save, mirroring
  `Create`'s own reservation shape exactly. A rename to an already-taken name now correctly fails
  with `ErrNameAlreadyExists` and leaves the old reservation untouched; a rename to the current
  name is a pure no-op. `RegisterRuleset`'s and `Create`'s doc comments updated to match.

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

- [ ] **No end-to-end coverage for the two new query-api endpoints
  (`GET /rulesets/{id}/tables/{name}[/{rowKey}]`).** `test/e2e/e2e_test.go` exercises
  `GET /rulesets` and `GET /rulesets/{id}` but neither new path, and nothing asserts that the
  engine's startup table-sync is actually readable through the query API on a real running
  cluster. A single e2e assertion (e.g. `GET /rulesets/{timadorusRulesetId}/tables/traits` returns
  the 3 expected rows after `make dev-up`) would cover the sync, the endpoint, and the migration
  image all in one shot — the Critical fixed in this same fix-wave (a broken migration image) went
  undetected specifically because no such coverage existed. This is a recommendation for a future
  branch, not a defect in this one — the plan never asked for it.

## Web SPA (`web/src`)

All three items previously listed here are fixed (`e7ad69c`, `e89a5f3`): `waitForUser` now takes
an `AbortSignal` that `CreatingUserModal` aborts on unmount, stopping the poll instead of letting
it run in the background; `waitForUser` also bails out immediately on a hard fetch error instead
of retrying for the full timeout, and the modal shows that error distinctly from an honest
timeout; and `query.types.ts`/`command.types.ts` are regenerated and current. All three verified
live with a real headless-Chromium session.

- [ ] **Character detail page (`character-detail-redesign`) and Campaign manage panel
  (`campaign-tabbed-panel`): a rejected rename discards the user's typed draft.** Both
  `BaseInfoTable.vue`'s and `ManageCampaignPanel.vue`'s `saveName()` collapse the inline editor
  immediately after emitting `submit-rename`, before the parent's `PATCH` call has resolved.
  Verified live with a mocked `400`: the error banner appears, the display reverts to the old
  name, and the text the user typed is gone — they must reopen the editor and retype from scratch
  to retry. Not data-damaging (the old name is never lost server-side), just a retry-ergonomics
  regression versus the previous always-editable input, which kept the attempted text in the box.
  For Character, this is a pre-existing, accepted trade-off; for Campaign, `campaign-tabbed-panel`
  introduced it as a genuine regression, not an inherited one — the deleted
  `ManageCampaignModal.vue` had exactly that always-editable input (`<input v-model="name">` plus
  a Rename button) and kept the typed text across a rejected rename, while the new click-to-reveal
  `ManageCampaignPanel.vue` does not. This remains an accepted, conscious trade-off for both
  components — a fix costs an extra round trip (e.g. a `saving`/`error` prop from the parent, or
  the emit carrying a callback). Separately, and pre-existing rather than introduced by either
  branch, `saveName()` used to have no emptiness guard: clearing the field and clicking Save sent
  `PATCH {"name":""}` unguarded (verified live for Character; the server rejected it and the
  banner explained, so nothing broke). That gap has now been closed in both components with the
  two-line `v-if` guard this entry used to price as the cheap half of the fix.

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

- [ ] **`web/e2e/character-creation.spec.ts`'s selectors will need hardening before a second test
  is added.** Several selectors work today only because of incidental page state, flagged by a
  final review as exactly what the "minimal additive change" allowance (e.g. a missing
  `aria-label`) was meant for, deliberately not touched to avoid churning a passing test: (1)
  `page.locator('form')` is unscoped to the modal — correct only because exactly one `<form>` is
  ever mounted at a time; (2) `modalForm.locator('input[type="text"]').first()` picks the Name
  field positionally — correct only because it happens to precede `UserPicker`'s own text input in
  DOM order; (3) the player-selection button lookup is page-scoped rather than scoped to the
  picker; (4) the Base Info card is located via a Tailwind utility class (`div.rounded-md`) rather
  than a stable hook. Before writing the harness's second test, add `aria-label`s or
  `data-testid`s to `CreateCharacterModal.vue`/`BaseInfoTable.vue` rather than propagating these
  same patterns.

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

- [ ] **Creating a Character no longer refreshes `CharactersPanel`'s own `users`/`gamemasterIds`.**
  The old `bumpSidebarRefresh()` call ran the panel's full `refresh()` (`list` + `listUsers` +
  `listGamemasters`); the new `waitForCharacterInList` only calls `list()`. `playerLabel()` reads
  `users.value`/`gamemasterIds.value`, both loaded once at mount — so a Character assigned to a
  User created after this panel mounted will show the raw `playerUserId` UUID in the sidebar until
  some unrelated refresh fires. Narrow, cosmetic, and self-healing.

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

- [ ] **The mock's `POST /universes/{universeId}/campaigns` route discards `gamemasterUserIds`, so
  `universe-manage.spec.ts`'s "creating a Campaign" test can only prove the URL changed, not that
  the right Gamemaster reached the request.** `mockBackend.ts`'s handler destructures
  `gamemasterUserIds` out of the body and never uses it — the pushed `MockCampaign` has no
  gamemaster field. The test's `expect(page.getByRole('checkbox').first()).toBeChecked()` proves
  `UserMultiSelect` ticked the box in the DOM, not that the selected user id reached the request
  body; the mock would 201 just as happily on `gamemasterUserIds: []`, which the real backend
  rejects with 422. Optional fix: assert against the mock's recorded state after navigation (e.g.
  `expect(state.campaigns.at(-1)).toMatchObject({ name: 'New Campaign', rulesetId: 'r1', universeId:
  'u1' })`), and/or actually store `gamemasterUserIds` on the pushed record and assert it.

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

- [ ] **`useChangeFeed.ts`'s `poll()` only re-checks its epoch guard once per batch, not once per
  change.** `myEpoch` is captured and checked right after the fetch resolves, but the loop that
  follows awaits `nextTick()` between writes (added to fix the batching-drop bug above) without
  re-checking `epoch` inside the loop. If `stop()` (or a new `start()` for a different Universe)
  fires in the gap between two `nextTick()` awaits, the loop keeps writing the remaining
  already-fetched changes — meant for the just-abandoned Universe — to `cursor`/`lastChange` before
  the interval is actually cleared. Extremely narrow (same-tick `stop()` mid-batch), and the
  original fix's own reviewer-suggested shape already had this property, so it wasn't flagged as
  new breakage. Fix: also check `if (myEpoch !== epoch) return` inside the loop, after each
  `await nextTick()`.

- [ ] **`change.aggregateId === <id>.value` comparisons are case-sensitive string equality.** All
  five detail views compare the change feed's `aggregateId` (Go's lowercase-canonical UUID
  marshalling) against a raw route param. A hand-typed or pasted uppercase UUID in the URL would
  silently disable that view's change reactions. Very low likelihood, and consistent with how ids
  are already compared elsewhere in this codebase — the final reviewer noted it for completeness
  only and did not recommend a change.

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

- [ ] **No CI check that `web/src/api/{query,command}.types.ts` stay in sync with the OpenAPI
  specs.** `web/package.json`'s `npm run generate` (via `web/scripts/generate-api-clients.mjs`)
  already regenerates both files from `api/query/openapi.yaml` and `api/command/openapi.yaml` —
  that command already exists and is current. What's still missing is a CI step that runs it and
  fails the build on a diff, so a spec change without a regenerate can land unnoticed (as it did
  under an earlier, already-merged branch).
