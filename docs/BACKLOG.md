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
  Related packaging gap, flagged for the same future pass rather than resolved here:
  `cmd/rebuild-read-models` has no Dockerfile and no Makefile target, unlike the long-running
  service binaries — so today it is only runnable via `go run`/`go build` against a reachable
  Postgres and NATS. Whatever consolidation this section lands on should decide how admin-tier
  tooling gets packaged, rather than bolting a one-off Dockerfile onto this binary.

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

- [ ] **`trySubmitPot` silently truncates non-integer `pot`/`statBudget` JSON values instead of
  rejecting them, and a no-op `submitPot` batch still writes an event.** `characterAction.Pot` is
  declared `map[string]float64`, and `trySubmitPot` converts each target (and the seeded
  `statBudget`) to `int` with a bare `int(...)` conversion — so a hand-crafted
  `{"action":"submitPot","pot":{"ST":60.9}}` is silently treated as `60` instead of being rejected,
  the same class of gap as the `setMaxStatBudget` entry above. The SPA always sends integers
  (`v-model.number` plus the `Number.isInteger` check in `AssignStatsBudgetModal.vue`'s `onBlur`),
  so this is only reachable via `curl` or the CLI's generic `action` verb, not through the app. A
  real fix would decode `pot`'s values (and `statBudget`) as `json.Number` and reject/log a
  non-integer explicitly instead of truncating. Separately, and not fixed either: an empty or
  absent `pot` map is accepted as a valid no-op batch (`totalCost` sums to `0` over zero entries,
  which is never `> statBudget`) — it still runs the full `SetInfo`/`Save` path and appends an
  `InfoChanged` event with no actual attribute change. This is *not* purely wasteful, though: it is
  load-bearing for the SPA's own "Submit with no edits" happy path, since `AttributesTable.vue`'s
  confirmation watch relies on exactly that event to drive the change-feed reload that closes the
  modal. A future fix must not simply skip the write for a zero-cost batch without also giving the
  SPA some other way to detect confirmation. Both found during this "Assign Stats Budget" branch's
  final-review fix wave; parked as Minor (curl/CLI-only reachability, and the second item is
  arguably intentional) rather than triggering another fix round.

- [ ] **Stat values below 1 silently resolve to a neutral Bonus of 0 instead of the bottom row's
  -25.** `internal/engine/timadorus/stat_bonus.go`'s `GetStatBonus(stat int) int` scans the
  `bonuses` table top-down and returns the first row whose `minval` the stat satisfies; a stat
  below every row's `minval` (i.e. below 1, including 0 and negative values) falls through the
  whole loop and returns a hardcoded `0` — the same Bonus as the baseline stat of 50, not the
  bottom row's `-25`. Today this is unreachable (the only caller, `defaultAttributes()`, only
  ever passes 50), so there's no production impact yet. But this branch's whole purpose is to
  build the one choke point (`setAttributeTemp`) that a *future* Temp-changing mechanic will call
  — and the first feature that can drive Temp down toward 0 (damage, drain, a curse effect, etc.)
  will silently get a neutral `0` bonus instead of the clearly-intended `-25` (or worse), with no
  error and no test failure to catch it. Note explicitly: this is the identical shape of bug to
  the minval-41/59 gap the user found and fixed in this same table during this branch's design
  review (a stat resolving into the wrong band) — just at the opposite end of the range, and not
  yet fixed because nothing can reach it yet. A real fix should decide whether `GetStatBonus`/
  `GetSpellPointBonus` ought to clamp to the bottom row instead of returning 0 for anything below
  the table, before any Temp-lowering mechanic ships.

- [ ] **`AssignStatsBudgetModal.vue`'s Pot `<input>` elements lack native HTML bounds and explicit
  labeling.** The ten Pot inputs have no `min`/`max` attributes — validation is entirely the
  JS-side `onBlur` check, so this is purely cosmetic (no native spinner-clamping or
  `:invalid`/`:out-of-range` styling hints the 0–100 bound before blur) — and no `<label>` or
  `aria-label` beyond the adjacent table cell's text, an accessibility polish gap for anyone using
  the input outside the visual context of its row. Found during this "Assign Stats Budget"
  branch's final-review fix wave; parked as Minor rather than triggering another fix round.

- [ ] **Embedding-size threshold for future table files is undocumented.** This branch's design
  embeds `traits.yaml` directly via `//go:embed`, appropriate for a small, secret-free,
  `//go:embed`-able file the binary can't start without. But the design spec calls this "the
  first of an extensive set of such files" without saying at what size or count embedding stops
  being appropriate and a different content-delivery approach (a seeded pipeline, external
  config, etc.) becomes warranted. Note this so whoever adds the next table file (or the fifth,
  or the twentieth) has somewhere to weigh that judgment call rather than rediscovering it.

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

- [ ] **The `pendingEntityId` provide/inject pair has a two-way-coupling wart.**
  `CharactersPanel.vue` writes it (sets the new Entity's id); `EntitiesPanel.vue` also writes to it
  (clears it back to `null` once its poll resolves) — both panels have write access to a ref
  neither owns. Harmless today (only one producer/consumer pair exists), but if a second
  `pendingXId`-shaped need ever arises, that's the signal to replace this narrow one-off with a
  single richer shared signal payload (e.g. `sidebarEvent: Ref<{ kind: string; id: string } | null>`)
  rather than accumulating more one-off refs on `WorkspaceView.vue`.

- [ ] **Independent 750ms polls now fire after one creation, with no shared coordination between
  them** — three after a Character creation (the Characters sidebar, the Entities sidebar, and the
  main pane), and, since the `poll-until-and-campaign-retry` branch added `waitForCampaign` retry
  coverage to Campaign creation, two more after a Campaign creation (`WorkspaceView`'s header badge
  and `CampaignOverviewPanel`'s Manage tab) — five independent pollers total across the two creation
  flows. Each roughly multiplies the SPA's request rate against a lagging backend for up to 15
  seconds, precisely when it is already struggling. Acceptable at this scale and an inherent
  consequence of the current design (independent consumers, each needing its own eventual-
  consistency wait), not a defect to fix now — just a property worth knowing about if this polling
  pattern is reused elsewhere.

- [ ] **A genuinely nonexistent Character id takes the full 15 seconds to report as such.**
  Navigating to a stale bookmark or a hand-typed bad `characterId` shows "Loading…" for the full
  timeout before the Retry/Back-to-Campaign UI appears, since `waitForCharacter` cannot distinguish
  "not yet projected" from "will never exist" (both 404 identically). This is an accepted,
  documented trade-off from the `character-creation-eventual-consistency` design spec, not an
  oversight — recorded here so the cost is visible in one place alongside the rest of this
  feature's known limitations.

- [ ] **`WorkspaceView.vue`'s deep link never writes to the selection store at all.** Confirmed by
  the final reviewer of the `campaign-picker-deep-link-fix` branch: navigating directly to
  `/universes/:universeId/campaigns/:campaignId` (the URL an actual bookmark or shared link would
  use) never calls `selection.setUniverse`/`setCampaign` — so on the next cold boot from `/`, the
  user is NOT restored to the campaign they bookmarked; whatever was previously stored (or nothing)
  wins instead. This is the deeper, more commonly bookmarked of the two deep-link entry points and
  produces the same "restored to the wrong thing" symptom that branch's own fix addresses for the
  Universe-picker deep link — but it's a different mechanism (a missing write, not an unconditional
  clear) and out of that branch's scope. Recommended fix: `WorkspaceView.vue`'s `onMounted`/`load()`
  should call `selection.setUniverse(universeId.value)` + `selection.setCampaign(campaignId.value)`
  once the Campaign is confirmed to exist.

- [ ] **`useUniverses().get()` collapses every API error to `null`, not just 404.**
  `UniversePickerView.vue`'s restore path treats a `null` result as "this Universe doesn't
  exist/is archived" and calls `selection.clearUniverse()` — but a transient network blip during
  cold boot would produce the same `null` and wipe the user's entire stored selection over a
  hiccup, not a real archival. Same class of concern the codebase already accepts for read-model
  lag elsewhere (`character-creation-lag.spec.ts`) but not yet addressed here. Not fixed in the
  `campaign-picker-deep-link-fix` branch that surfaced it — flagging only. The
  `universe-panel-change-feed` branch adds a second, unprompted trigger path into this same
  exposure: previously `UniverseOverviewPanel.load()` only ran on mount/param-change, both
  correlated with a user action, but now its own change-feed poll can also call `getUniverse()`
  in the background, silently exposed to the same transient-network-blip collapse with no user
  action involved at all.

- [ ] **The same `for`/`id`/`role="group"` gap flagged above for `CreateCampaignModal.vue` still
  exists in 5 of the other modals under `web/src/components/modals/`:** `CreateCharacterModal.vue`,
  `CreateEntityModal.vue`, `CreateObjectModal.vue`, `CreateUniverseModal.vue`, and
  `CreateUserModal.vue`. Each needs the same mechanical fix — pair `<label for>` with its
  input's/select's `id`, and wire any multi-select/group widgets up via
  `aria-labelledby`/`role="group"` — but none of that was done here; flagging only.
  (`CreatingUserModal.vue` was originally listed too, but it has no `<label>`/`<input>`/`<select>`
  at all — it's a progress/wait modal with only text and a Close button — so there's nothing to
  fix there; corrected here after the task reviewer caught the inaccuracy.)

- [ ] **`CampaignPickerView.vue`'s stored-selection restore path still uses single-shot
  `getCampaign`, not `waitForCampaign`.** Its `onMounted` calls `getCampaign(selection.
  selectedCampaignId)` directly to validate a restored bookmark, so a lagging read model for that
  Campaign (e.g. right after creation) fails the restore check immediately and clears the stored
  selection — unlike every other Campaign-lookup call site in this branch, which now retries via
  `waitForCampaign`. Flagged, not fixed, by the `poll-until-and-campaign-retry` branch's final
  review: it's genuinely unclear whether this is deliberate (a stale/bad bookmark arguably SHOULD
  clear fast rather than stall the picker for up to 15s) or an oversight. Needs a product decision
  before changing the behavior either way.

- [ ] **`BaseModal.vue` declares `role="dialog"`/`aria-modal="true"` but has no focus management.**
  There is no focus trap, no initial focus, no focus restoration on close, and no Escape-to-close
  handling (grepped: zero `keydown`/`Escape`/`focus()`/`inert`/`tabindex` hits anywhere in
  `web/src`). A complete WAI-ARIA dialog pattern needs all of these; `aria-modal` tells assistive
  tech to treat everything outside the dialog as if it doesn't exist, but without focus
  containment, keyboard focus can still leave the dialog into that "nonexistent" page — a
  regression in AT confusion specifically, even though `role="dialog"`/`aria-modal` overall is a
  clear improvement. Flagged by the `create-campaign-modal-a11y` branch's final review; not fixed
  here.

- [ ] **`web/e2e/**` and `playwright.config.ts` are not typechecked by anything.** `web/tsconfig.app.json`
  only includes `src/**/*.ts` and `src/**/*.vue`, and `web/tsconfig.node.json` only includes
  `vite.config.ts` — so `npm run typecheck` exits 0 even with a deliberately broken type error
  injected into an e2e spec file or into `playwright.config.ts`. This is pre-existing and out of
  scope for this branch's fix. However, at least one existing plan document
  (`docs/superpowers/plans/2026-08-30-spa-e2e-test-harness.md`) incorrectly asserts this coverage
  already exists. Recommend either adding `e2e/**` + `playwright.config.ts` to `tsconfig.node.json`'s
  `include`, or creating a third referenced project to cover them separately. Worth a permanent
  record since the coverage gap is real and the plan's claim is demonstrably false.

## Devcluster tooling (`test/e2e/internal`)

- [ ] **No `TraefikServiceName` exported constant.** `seed.go` and `up.go` both hardcode the
  literal `"traefik"` string independently. Cheap to fix whenever either file is next touched.

- [ ] **Seed retry budget is an untested heuristic.** `seedHTTPDoWithRetry`'s 6-attempt/2s-backoff
  budget was tuned against observed Traefik routing-sync delay, not derived from a proven bound.
  Constants are isolated at the top of the function for easy tuning if a slower environment ever
  needs more headroom.

