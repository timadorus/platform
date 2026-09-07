
# Done

Tasks and work items that used to be in BACKLOG.md, but have been fixed or completed. In order to avoid BACKLOG.md growing to unwieldy lengths, items are copied here after they are done.

## `timadorus-engine` (`internal/engine/timadorus`, `cmd/timadorus-engine`)

- [x] **Fixed.** `handleCharacterCreated` now reads `characterCreation.maxStatBudget` from the
  write-side Campaign aggregate directly (not `campaigns_read_model`), removing the
  `ConfigurationChanged`-projection hop that made the race easy to lose. A new periodic
  `Reconciler` (`internal/engine/timadorus/reconcile.go`, no NATS/Router/checkpoint involvement —
  see its own doc comment) sweeps every 30 seconds and additively backfills any residual gap,
  including historical ones — this also closes the separate "no backfill for pre-existing
  Campaigns" item below, which the sweep uses the same mechanism to fix. Full history of why the
  two earlier retry-based attempts failed is preserved below for anyone who reaches for that
  approach again.

  **Original bug, kept for historical context:** `handleCharacterCreated`
  (`internal/engine/timadorus/character_processor.go`) used to seed a new Character's
  `stats.statBudget` by reading the Campaign's own `configuration`
  (`characterCreation.maxStatBudget`) directly from `campaigns_read_model`. That column was filled
  in by a completely independent, asynchronous event chain (`CampaignCreated` →
  `CampaignProcessor.handleCampaignCreated` → `ConfigurationChanged` → the outbox relay's own
  ~200ms poll cycle → NATS → the projector → the read-model row) with no ordering guarantee
  relative to `CharacterCreated`. A Character created immediately after its Campaign (a real,
  plausible workflow, not just a test artifact) could read the Campaign's configuration before that
  default had landed and permanently miss `statBudget` — silently, with `traitPoints`/`traits`/
  `attributes` seeded normally. This was the shipped behavior on the `character-attributes` branch,
  before the direct-aggregate-read fix and Reconciler above closed it; at the time,
  `test/e2e/e2e_test.go`'s own coverage waited for the Campaign's default to land before creating
  its Character specifically to avoid hitting this gap, rather than asserting it away.

  **Two attempts to fix this properly were made and reverted on the `character-attributes` branch
  — do not repeat either:**
  1. Make `handleCharacterCreated` return an error (triggering the router's existing
     Nack/redelivery, `internal/projection/router.go`, up to `defaultMaxAttempts = 5`) instead of
     silently omitting `statBudget`, coupling `traitPoints`/`traits`/`attributes` seeding to the
     same retry. This alone didn't converge: the shared NATS subscriber
     (`internal/bus.NewSubscriber`) configured no `NakDelay`, so all 5 retries exhausted in
     milliseconds — far faster than the real cross-service chain above ever completes.
  2. Adding `NakDelay: nats.NewStaticDelay(500 * time.Millisecond)` to give retries real backoff.
     This is platform-wide (affects all 12 read-model projectors plus both `timadorus-engine`
     processors) and did make the specific real-cluster e2e scenario pass. **But it is unsafe in
     general**: `internal/projection/checkpoint` tracks a single scalar watermark
     (`last_global_seq`) per projector, not per-message applied state.
     `CharacterProcessor.Subjects()` is one shared subject (`events_character`) for *every*
     Character in the platform. If any other Character's event (a different Character's
     `CharacterCreated`, or an unrelated `addTrait`/rename) is successfully handled during the
     ~500ms-to-2.5s retry window, the checkpoint advances past the retrying message's `GlobalSeq`.
     When that message is finally redelivered, `router.go`'s `env.GlobalSeq <= lastSeq` branch
     treats it as "already applied," Acks it without ever calling `Handle` again, and clears the
     retry-attempt counter — **no dead-letter row, no error, no trace.** The Character ends up
     with *no* `stats` object at all (not even `traitPoints`/`traits`/`attributes`), which is
     *worse* than the original silent gap this was meant to fix. This was caught only by a final
     whole-branch review reasoning about concurrent Character traffic — the Go unit test for the
     retry path uses Watermill's in-memory `gochannel` pubsub (which resends a Nacked message
     in-place, blocking, no interleaving possible) and the real-cluster e2e test creates exactly
     one Character in an otherwise-idle cluster, so neither test layer can reproduce this failure
     mode.

  **A real fix needs one of:** (a) per-aggregate applied-state tracking in the checkpoint model
  instead of a single watermark, so a redelivered message can be distinguished from "some later
  message already succeeded"; (b) a decoupled, out-of-band reconciliation/backfill job that
  periodically finds Characters missing `stats.statBudget` whose Campaign now has one, and patches
  them directly — sidestepping the event-processing retry path entirely; or (c) some other
  mechanism that doesn't rely on Nack-based redelivery for a business-logic (not infrastructure)
  retry on a shared, multi-aggregate subject. This would have been bigger than a single-branch fix
  and would have needed its own design pass — it was flagged urgent at the time because the
  behavior it described (silent, undetectable `statBudget` loss on fast Campaign→Character
  creation) was a real, if narrow, correctness gap in shipped behavior, not merely a defect in a
  fix attempt. Kept here for the reasoning; the delivered fix combines a direct-aggregate read (not
  listed above, since it shrinks the race rather than working around it) with a Reconciler matching
  option (b) above, together closing the gap without needing option (a)'s checkpoint-model rework.

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

- [x] **Fixed** (same commits as the URGENT statBudget entry above). Reconciler's Campaign pass
  backfills `traits`/`characterCreation.maxStatBudget` onto any Timadorus Campaign missing them,
  regardless of when it was created — additively, never overwriting a GM's own customization.

  **Original gap, kept for historical context:** Campaigns created before this engine's
  traits/max-stat-budget defaults shipped got no default traits, since the merge was triggered only
  by `CampaignCreated` — not by any backfill pass — so only Campaigns created *after* the new engine
  ran were affected. Resetting `CampaignProcessor`'s checkpoint would not have been a safe way to
  backfill existing Campaigns — replaying `CampaignCreated` for a Campaign that had also received
  `ConfigurationRequested` events since would have re-run the non-idempotent "configs" append (see
  `mutateConfiguration`'s own doc comment) for every one of those historical events too, not just
  merged in the missing traits. The same gap applied to `characterCreation.maxStatBudget`
  (introduced by the `campaign-max-stat-budget` branch): it was seeded by the same
  `handleCampaignCreated` merge, so a pre-existing Campaign never got a default `maxStatBudget`
  either. Reconciler's periodic, checkpoint-independent sweep closes this without any of that
  replay risk.

- [x] **Fixed** (`c97b9f6`). `ruleset.Service.Rename` (`internal/command/ruleset/service.go`) now reserves the
  new name and releases the old one in the same transaction as the `RulesetRenamed` save, mirroring
  `Create`'s own reservation shape exactly. A rename to an already-taken name now correctly fails
  with `ErrNameAlreadyExists` and leaves the old reservation untouched; a rename to the current
  name is a pure no-op. `RegisterRuleset`'s and `Create`'s doc comments updated to match.

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

All three items previously listed here are fixed (`e7ad69c`, `e89a5f3`): `waitForUser` now takes
an `AbortSignal` that `CreatingUserModal` aborts on unmount, stopping the poll instead of letting
it run in the background; `waitForUser` also bails out immediately on a hard fetch error instead
of retrying for the full timeout, and the modal shows that error distinctly from an honest
timeout; and `query.types.ts`/`command.types.ts` are regenerated and current. All three verified
live with a real headless-Chromium session.

- [x] **Fixed** (`db71a6e`). `BaseInfoTable.vue`'s and `ManageCampaignPanel.vue`'s `saveName()` no
  longer close the editor immediately on emit — each now `watch`es its own `name` prop and closes
  only once it changes to match what was submitted (i.e. only on a successful rename). A rejected
  rename never touches `name`, so the editor and the typed draft survive untouched, ready to retry
  alongside the error banner. Verified live with a throwaway Playwright repro forcing a 400 then a
  204: confirmed the draft survives the rejection and the editor closes on the successful retry,
  and confirmed the same repro genuinely fails against the pre-fix behavior.

- [x] **Fixed** (`db71a6e`). `BaseInfoTable.vue`'s Player cell now always renders `{{ playerName }}`,
  regardless of `editingPlayer` — the picker appears in its own row below, rather than replacing
  the name. A GM can now see who is being replaced while reassigning.

- [x] **Fixed** (`db71a6e`). `CharacterDetailView.vue`'s Stats tab wrapper is now
  `class="flex flex-wrap gap-4"`, so `AttributesTable`/`BaseInfoTable` stack instead of
  compressing on narrow viewports.

- [x] **Fixed** (`db71a6e`). Added `data-testid` hooks (`create-character-form`,
  `character-name-input`, `user-picker`, `base-info-card`) to `CreateCharacterModal.vue`,
  `UserPicker.vue`, and `BaseInfoTable.vue`, and migrated `character-creation.spec.ts` and
  `character-creation-lag.spec.ts` off all four fragile selectors this entry listed (the unscoped
  `page.locator('form')`, the positional Name-field lookup, the page-scoped player-selection
  button, and the Tailwind-class-based Base Info card lookup).

- [x] **Fixed** (`db71a6e`). `CharactersPanel.vue`'s `onCreated` now chains a full `refresh()`
  (not just another `list()`) onto `waitForCharacterInList`'s resolution once the new Character is
  actually visible, restoring the old `bumpSidebarRefresh()`'s full-refresh behavior for this call
  site so `users`/`gamemasterIds` pick up a brand-new User the Character might be assigned to.

- [x] **Fixed** (`db71a6e`). `mockBackend.ts`'s campaign-creation route now stores
  `gamemasterUserIds` on the pushed `MockCampaign`, and `universe-manage.spec.ts` asserts
  `state.campaigns.at(-1)` directly (name, rulesetId, universeId, and `gamemasterUserIds`)
  instead of only checking a checkbox's DOM state.

- [x] **Fixed** (`db71a6e`). `useChangeFeed.ts`'s `poll()` now also checks
  `if (myEpoch !== epoch) return` inside the batch loop, after every `await nextTick()`, not just
  once before it — closing the narrow same-tick-`stop()`-mid-batch window this entry described.

- [x] **Fixed** (`db71a6e`). The four remaining detail views (`CampaignOverviewPanel.vue`,
  `EntityDetailView.vue`, `CharacterDetailView.vue`, `ObjectDetailView.vue` — the fifth,
  `UniverseOverviewPanel.vue`, no longer has this comparison at all, since its change-feed wiring
  was removed as dead code in an earlier fix) now compare `change.aggregateId.toLowerCase()`
  against the route param's own `.toLowerCase()`, so a hand-typed or pasted uppercase UUID no
  longer silently disables that view's change reactions.

- [x] **Fixed** (`5604992`). The poll-with-timeout skeleton duplicated across `useUsers.ts`'s
  `waitForUser`, `useCharacters.ts`'s `waitForCharacter`/`waitForCharacterInList`, and
  `useEntities.ts`'s `waitForEntityInList` is now a single shared `pollUntil` helper
  (`web/src/composables/usePolling.ts`), extracted once `useCampaigns.ts`'s new `waitForCampaign`
  became the fifth copy — exactly the trigger the original BACKLOG entry named. All five call sites
  now share the same 750ms/15000ms defaults, `AbortSignal` handling, and deadline check.

- [x] **Fixed** (`701ba98`). A freshly created Campaign now gets the same retry/timeout parity
  Character creation already had: `WorkspaceView.vue`'s header badge polls via the new
  `waitForCampaign` instead of a single `get()` (so read-model lag no longer permanently blanks the
  badge), and `CampaignOverviewPanel.vue`'s `load()` polls via `waitForCampaign` with an
  `AbortController` (aborted on unmount and at the start of every new `load()`, mirroring
  `CharacterDetailView.vue`), showing the same Retry/Back-to-Universe timeout UI when the Campaign
  never becomes visible in time. Regression tests: `web/e2e/campaign-creation-lag.spec.ts`.

  **A pre-existing routing bug, found while writing that regression test and fixed in the same
  commit:** `UniverseOverviewPanel.vue`'s `goToCampaign` and `CampaignPickerView.vue`'s `goTo` both
  pushed `{ name: 'workspace' }` — the *parent* route — instead of `{ name: 'campaign-overview' }`,
  its default child (path `''`). A named push resolves `matched` by walking up the target record's
  own ancestors; it does not descend into a default-path child. Pushing the parent by name
  therefore resolved `matched` to `[workspace]` only, leaving `WorkspaceView`'s nested
  `<router-view>` (`CampaignOverviewPanel`) permanently unmounted after the client-side navigation
  — only a hard reload of the same URL happened to render it, since a full page load resolves the
  whole path fresh. This predated this branch and would have silently blanked the panel for every
  real Create-Campaign or pick-a-Campaign flow, not just the delayed-visibility case the new test
  targets — not merely a symptom of the lag-handling work being added alongside it. Both call sites
  now push `{ name: 'campaign-overview' }` instead. Regression coverage:
  `universe-manage.spec.ts`'s "creating a Campaign from the Universe panel navigates into its
  workspace" and `campaign-picker-deep-link.spec.ts` both now assert a heading actually renders
  after navigating, not just that the URL changed.

- [x] **Fixed.** Two further state-machine bugs in `CampaignOverviewPanel.vue`'s `load()`, found by
  a final whole-branch review of the `poll-until-and-campaign-retry` branch:
  1. A silent (change-feed-triggered) reload used to unconditionally `abort()` whatever load was
     already in flight. If that in-flight load was the initial non-silent one (which alone owns
     `loading`), its own early `if (controller.signal.aborted) return` fired before it ever reached
     `loading.value = false` — and the silent reload itself never touches `loading` at all — so the
     panel stayed on "Loading…" forever, even though the silent reload went on to populate
     `campaign.value` successfully. Fixed by having a silent `load()` return immediately, before
     aborting anything, whenever a non-silent load already owns `loading`.
  2. `campaign.value = found` used to run unconditionally, so a *silent* reload that timed out
     (`found === null`) unconditionally nulled out an already-rendered Campaign, dropping the panel
     straight to the dead-end "Campaign not found." view — directly contradicting the design intent
     that a background reload must never blow away an already-rendered page, and diverging from
     `CharacterDetailView.vue`'s matching silent-failure handling. Fixed by moving the assignment
     into the `found` branch and only nulling `campaign`/setting `loadTimedOut` on a *non-silent*
     failure.

  Regression test: `web/e2e/campaign-creation-lag.spec.ts`'s "a change-feed reload during the
  initial lag neither strands the panel on 'Loading…' nor blanks it once rendered" — seeds an
  8-second creation-visibility delay plus a matching `campaign`/`CampaignCreated` change-feed entry
  so a silent reload genuinely fires while the initial non-silent load is still polling (bug 1),
  then forces the already-rendered Campaign to stop resolving and fires a second matching change so
  a silent reload times out against it (bug 2). Confirmed to fail against the pre-fix code with the
  exact described symptom (the heading never appears; the panel stays on "Loading…") before the fix
  was restored.

  Additionally, the same `AbortController`-on-unmount/abort-and-replace-per-`load()` pattern was
  added to `WorkspaceView.vue`'s `load()` — it was the only remaining `waitForCampaign` caller
  without it, so an in-flight poll could otherwise keep running for up to 15s after unmount, and
  `watch(sidebarRefreshSignal, load)` could spawn a second concurrent poller during a lag window.

- [x] **Fixed.** **The poll-with-timeout pattern used to be duplicated four times.** `useUsers.ts`'s
  `waitForUser`, `useCharacters.ts`'s `waitForCharacter` and `waitForCharacterInList`, and
  `useEntities.ts`'s `waitForEntityInList` used to share an identical skeleton — the same
  750ms/15000ms defaults, the same `opts` shape, the same `for (;;)` loop, the same pre-await/
  post-await abort guards, the same deadline check — varying only in which fetch to call and what
  counts as success. The `poll-until-and-campaign-retry` branch's new `useCampaigns.ts` `waitForCampaign`
  became the fifth copy this entry itself said would be the trigger to extract — so it extracted a
  shared `pollUntil` helper (`web/src/composables/usePolling.ts`) instead, and migrated all five
  call sites onto it. See `docs/DONE.md`.

- [x] **Fixed.** **A freshly created Campaign had no retry/timeout handling for read-model lag,
  unlike Character creation.** `CampaignPickerView.vue`'s `onCreated(id)` navigates straight to the
  Campaign workspace, which lands by default on `CampaignOverviewPanel.vue` — so a lagging read
  model used to show the dead-end "Campaign not found." with no Retry, unlike the
  `character-creation-eventual-consistency` pattern (`waitForCharacter` plus
  `CharacterDetailView`'s Retry/Back-to-Campaign UI). `WorkspaceView.vue`'s own `getCampaign` call
  had the identical exposure, leaving the header badge blank instead. Both are now fixed by the
  `poll-until-and-campaign-retry` branch. See `docs/DONE.md`.

- [x] **Fixed** (`04142ab`). `web/package.json`'s `typecheck` script now runs `vue-tsc -b --noEmit`,
  making the CI gate meaningful by actually typechecking the full `web/src` tree. The fix surfaced
  zero pre-existing type errors (the build's own `vue-tsc -b` was already keeping the tree clean).
  This branch also activates the existing but-vacuous CI step described in the design spec's
  Out of Scope section, converting that dormant gate into a live one at approximately zero net
  CI cost.

- [x] **Fixed.** **`CampaignPickerView.goTo` never records the selected Universe, unlike its sibling
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
  Fixed not with a per-call-site patch but by moving the guard into `selection.ts`'s `setUniverse`
  itself: it now only nulls `selectedCampaignId` when the incoming Universe id actually differs
  from the stored one. That single change closes the whole bug class at once, including a
  previously-latent third instance the final review found in `UniversePickerView.goTo` (its own
  grid-select/`onCreated` path, unreachable in practice today but a real landmine) — not just the
  originally-reported `CampaignPickerView.goTo` call site. Regression test:
  `web/e2e/campaign-picker-deep-link.spec.ts`.

- [x] **Fixed.** `CreateCampaignModal.vue`'s labels now have proper `for`/`id` pairing with their
  `<input>`/`RulesetSelect`, and its `UserMultiSelect` group is tied to its label via
  `aria-labelledby`/`role="group"` (using the `id`/`labelledby` props `RulesetSelect.vue`/
  `UserMultiSelect.vue` now accept, plus `BaseModal.vue`'s `role="dialog"`/`aria-labelledby`).
  `universe-manage.spec.ts`'s Create Campaign test now uses `page.getByLabel(...)` and
  `page.getByRole('dialog'/'group', ...)` instead of the unscoped, strict-mode-dependent
  `page.locator('form').getByRole(...)` workaround.

- [x] **Fixed.** `UniverseOverviewPanel.vue` now owns its own `useChangeFeed()` instance (started/
  stopped on mount/unmount and re-scoped on `universeId` change), rather than relying on
  `WorkspaceView`'s `lastAggregateChange` provide/inject (out of reach anyway, since this panel is
  a top-level route, not `WorkspaceView`'s child). It filters for `universe`-type changes matching
  its own `universeId`, mirroring `CampaignOverviewPanel.vue`'s pattern, and triggers a `silent`
  reload on a match.


## projector

- [x] **Fixed.** `cmd/projector` now builds its pool via `pgxpool.ParseConfig` +
  `pgxpool.NewWithConfig`, with `MaxConns` from the new `Projector.PoolMaxConns` config field
  (default 16, overridable via `PROJECTOR_POOL_MAX_CONNS`), mirroring
  `cmd/timadorus-engine`'s already-established pattern exactly.

- [x] **Fixed.** `cmd/rebuild-read-models` (new standalone binary) automates a safe full replay of
  the retained event stream through `cmd/projector`'s registered projectors: it deletes each
  affected projector's JetStream durable consumer (a checkpoint reset alone does not cause NATS to
  redeliver an already-acked message — see `internal/rebuildreadmodels`'s own doc comment) and
  resets its Postgres checkpoint, in two required phases (`--phase=base` then
  `--phase=change-feed`), refusing to run the second phase until every base projector has actually
  caught up to its own target. Requires `cmd/projector` to already be stopped — an explicit
  interactive confirmation, plus a `ConsumerInfo(...).PushBound` liveness pre-check that aborts if
  any durable consumer still has a subscription bound to it. The shared list of "every registered
  projector" now lives in `internal/projection/registry`, used by both `cmd/projector/main.go` and
  this tool, so they can never drift out of sync.

  **What this tool is and is not.** It is an *idempotent replay*, not a wipe-and-rebuild: it
  truncates nothing. Every base projector's `Created` handler is `INSERT ... ON CONFLICT (id) DO
  NOTHING`, so replaying over surviving rows recovers projections that never got applied (after a
  checkpoint/consumer mismatch, or for events dropped before this branch's reconciliation fixes
  existed) but cannot correct a wrong column value and cannot remove a row a buggy projector wrote.
  Repairing already-written bad rows would need table truncation, which would in turn need each
  projector to expose the tables it owns — a separate design decision, deliberately not taken here.

  **Scope.** Strictly `cmd/projector`'s registered projectors (`internal/projection/registry`).
  `cmd/timadorus-engine`'s two processors (`CampaignProcessor`, `CharacterProcessor`) write to the
  same `projection_checkpoints` table but are NOT covered by this tool and are NOT safe to reset
  with it: `docs/DONE.md` records that replaying `CampaignProcessor` would re-run its
  non-idempotent `configs` append for every historical `ConfigurationRequested` event.

  **Per-projector catch-up targets.** Each projector's target is the maximum `global_seq` among
  events of *its own* aggregate type, bounded by a watermark captured once at the start of the
  rebuild — never the whole-table maximum. A projector's checkpoint only ever advances from
  messages on its own subject (the outbox relay publishes each event to exactly one subject, per
  `bus.Subject`), so at most one projector could ever reach the whole-table maximum; demanding it
  of all of them made phase 1 poll forever and phase 2's guard refuse to proceed. Found by the
  branch's final whole-branch review and fixed in the same wave; see
  `internal/rebuildreadmodels.ComputeTargets`.

- [x] **Fixed.** `UniverseOverviewPanel.vue` now owns its own `useChangeFeed()` instance (started/
  stopped on mount/unmount and re-scoped on `universeId` change), rather than relying on
  `WorkspaceView`'s `lastAggregateChange` provide/inject (out of reach anyway, since this panel is
  a top-level route, not `WorkspaceView`'s child). It filters for `universe`-type changes matching
  its own `universeId`, mirroring `CampaignOverviewPanel.vue`'s pattern, and triggers a `silent`
  reload on a match.

- [x] **Already fixed** (`05a84b7`, predates this entry). `.github/workflows/ci.yml`'s `web-build`
  job already has a "verify generated API clients are up to date" step: `npm run generate` followed
  by `git diff --exit-code -- src/api/command.types.ts src/api/query.types.ts`. This entry was
  simply never reconciled against that existing check — no code change needed.

- [x] **Fixed** (`04142ab`). `web/package.json`'s `typecheck` script now runs `vue-tsc -b --noEmit`,
  making the CI gate meaningful by actually typechecking the full `web/src` tree. The fix surfaced
  zero pre-existing type errors (the build's own `vue-tsc -b` was already keeping the tree clean).
  This branch also activates the existing but-vacuous CI step described in the design spec's
  Out of Scope section, converting that dormant gate into a live one at approximately zero net
  CI cost.
