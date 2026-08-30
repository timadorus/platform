# Character Creation: Tolerate Read-Model Lag — Design

## Context

`POST /campaigns/{id}/characters` returns as soon as the command is accepted, but the Character
and its auto-created Entity only become visible through the query API once the async projector
catches up. Today, nothing in the SPA tolerates that gap:

- `CharactersPanel`/`EntitiesPanel` re-fetch their lists exactly once after creation (a single
  `bumpSidebarRefresh()` fan-out) — if the projector hasn't caught up yet, the new row silently
  never appears until some unrelated refresh happens to fire again.
- `CharacterDetailView`'s `load()` calls `get(characterId)` exactly once. If that 404s because the
  projector is behind, `character.value` stays `null` forever — the page is stuck on "Loading…"
  with no retry and no timeout.

This is the same class of problem this codebase already solved once, for User creation:
`useUsers.ts`'s `waitForUser` (poll with interval/timeout/`AbortSignal`) plus
`CreatingUserModal.vue` (distinct "still waiting" vs. hard-error messaging). This spec extends that
proven pattern to Character creation's two remaining gaps: the sidebar lists, and the main pane.

## Decisions

- **Same interval/timeout defaults as `waitForUser`**: 750ms poll interval, 15000ms timeout. No
  new tuning knobs — reuse what's already proven.
- **The single-entity `waitForCharacter` cannot distinguish "still syncing" from "genuinely doesn't
  exist"** — `get(id)` 404s identically either way, unlike `waitForUser`'s list-based check (which
  gets a clean `200 + array` even when the target isn't in it yet). So `waitForCharacter` retries
  through every failure until the deadline, and the timeout UI shows one honest, unified message
  rather than a distinct "error" vs. "timeout" split.
- **The two list-based helpers (`waitForCharacterInList`, `waitForEntityInList`) do get the clean
  distinction**, since they're built on `list()`/`search()`, which behave like `waitForUser`'s
  `list()` — mirror `waitForUser`'s structure exactly, including its `error.value` check.
- **Sidebar retries are fully silent** — no loading indicator, matching how `CharactersPanel`/
  `EntitiesPanel` already show no loading state today. The list simply updates itself once the
  data catches up.
- **`CharacterDetailView`'s timeout state gets a Retry button and a Back-to-Campaign link**,
  alongside the message. Retry re-invokes the same `load()` used on mount.
- **Cross-panel notification for the auto-created Entity**: `CharactersPanel` (which receives the
  new `characterId`/`entityId` from the modal) can poll its own list directly, but the Entity lives
  in a sibling panel (`EntitiesPanel`). Rather than overload the existing generic
  `sidebarRefreshSignal`/`bumpSidebarRefresh` pair (used by rename/archive/reassign flows
  elsewhere, which don't need retrying), add one new, narrowly-scoped `pendingEntityId`
  provide/inject ref: `CharactersPanel` sets it after creation, `EntitiesPanel` watches it, polls,
  and clears it. This is additive — the existing generic bump signal is untouched and still used by
  everything that already relies on it.
- **`EntitiesPanel`'s poll always searches unfiltered (`''`)**, not whatever search term the user
  currently has typed — the goal is guaranteeing the new Entity becomes visible, and polling under
  an active filter could time out harmlessly forever if the new name doesn't match it. This
  overrides any in-progress search filter once, when a Character is created — noted here as a
  deliberate, minor UX trade-off, not an oversight.
- **`CharacterDetailView`'s poll is abort-aware across navigation, not just unmount**: the
  component instance is reused across param-only route changes (already noted in its existing
  `characterId` `computed` comment), so switching to a different Character mid-poll must abort the
  stale poll — otherwise a slow response for the *previous* Character could land after the user has
  already navigated to a new one and overwrite the page with the wrong data.

## Changes

### `web/src/composables/useCharacters.ts`

Two new exported functions, added alongside the existing ones (`get`, `list`, etc. unchanged):

```ts
// waitForCharacter polls get(id) until it succeeds or timeoutMs elapses. Unlike waitForUser,
// this cannot distinguish "the projector hasn't caught up yet" from "this id doesn't exist" —
// get() 404s identically either way — so every failed attempt is treated the same and retried
// until the deadline; callers should show one honest "couldn't load" message on timeout, not a
// distinct error state.
async function waitForCharacter(
  id: string,
  opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
): Promise<CharacterSummary | null> {
  const intervalMs = opts.intervalMs ?? 750
  const timeoutMs = opts.timeoutMs ?? 15000
  const deadline = Date.now() + timeoutMs
  for (;;) {
    if (opts.signal?.aborted) return null
    const found = await get(id)
    if (opts.signal?.aborted) return null
    if (found) return found
    if (Date.now() >= deadline) return null
    await new Promise((resolve) => setTimeout(resolve, intervalMs))
  }
}

// waitForCharacterInList polls list(campaignId) until characterId appears in the result or
// timeoutMs elapses — mirrors waitForUser exactly (list() sets error.value on a real failure,
// distinct from "not in the list yet", so a hard error bails out immediately instead of
// retrying pointlessly for the full timeout).
async function waitForCharacterInList(
  campaignId: string,
  characterId: string,
  opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
): Promise<boolean> {
  const intervalMs = opts.intervalMs ?? 750
  const timeoutMs = opts.timeoutMs ?? 15000
  const deadline = Date.now() + timeoutMs
  for (;;) {
    if (opts.signal?.aborted) return false
    await list(campaignId)
    if (opts.signal?.aborted) return false
    if (error.value) return false
    if (characters.value.some((c) => c.id === characterId)) return true
    if (Date.now() >= deadline) return false
    await new Promise((resolve) => setTimeout(resolve, intervalMs))
  }
}
```

Both returned from the composable's final object, alongside the existing exports.

### `web/src/composables/useEntities.ts`

One new function, same shape as `waitForCharacterInList`, built on `search`:

```ts
// waitForEntityInList polls search(universeId, '') — deliberately unfiltered, regardless of
// any search term the caller might otherwise be using, so a new Entity is guaranteed findable
// rather than potentially excluded by an unrelated in-progress filter — until entityId appears
// or timeoutMs elapses.
async function waitForEntityInList(
  universeId: string,
  entityId: string,
  opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
): Promise<boolean> {
  const intervalMs = opts.intervalMs ?? 750
  const timeoutMs = opts.timeoutMs ?? 15000
  const deadline = Date.now() + timeoutMs
  for (;;) {
    if (opts.signal?.aborted) return false
    await search(universeId, '')
    if (opts.signal?.aborted) return false
    if (error.value) return false
    if (entities.value.some((e) => e.id === entityId)) return true
    if (Date.now() >= deadline) return false
    await new Promise((resolve) => setTimeout(resolve, intervalMs))
  }
}
```

### `web/src/components/modals/CreateCharacterModal.vue`

`emit('created', characterId)` → `emit('created', characterId, entityId)`, using the `entityId`
already returned by `create()` (currently destructured and discarded). `defineEmits` type updated
to `created: [characterId: string, entityId: string]`.

### `web/src/views/WorkspaceView.vue`

One new provide, alongside the existing `sidebarRefreshSignal`/`bumpSidebarRefresh`:

```ts
const pendingEntityId = ref<string | null>(null)
provide('pendingEntityId', pendingEntityId)
```

### `web/src/components/layout/CharactersPanel.vue`

`onCreated` gains the `entityId` parameter, polls its own list in the background instead of the
old single-shot `bumpSidebarRefresh()` call, and sets `pendingEntityId` for `EntitiesPanel` to
notice:

```ts
const pendingEntityId = inject<Ref<string | null>>('pendingEntityId')

function onCreated(characterId: string, entityId: string) {
  showCreate.value = false
  select(characterId)
  waitForCharacterInList(props.campaignId, characterId)
  if (pendingEntityId) pendingEntityId.value = entityId
}
```

(`waitForCharacterInList`'s returned promise is intentionally not awaited here — it updates
`characters.value` as a side effect of its internal `list()` calls, which the template already
renders reactively; nothing further needs to happen once it resolves.)

### `web/src/components/layout/EntitiesPanel.vue`

Injects and watches the new `pendingEntityId`:

```ts
const pendingEntityId = inject<Ref<string | null>>('pendingEntityId')
if (pendingEntityId) {
  watch(pendingEntityId, async (id) => {
    if (!id) return
    await waitForEntityInList(props.universeId, id)
    pendingEntityId.value = null
  })
}
```

### `web/src/views/CharacterDetailView.vue`

`load()` is rewritten to poll via `waitForCharacter`, with an `AbortController` that's aborted on
both unmount and the *start* of the next `load()` call (covering the same-instance-reused-across-
navigation case):

```ts
const character = ref<CharacterSummary | null>(null)
const error = ref<string | null>(null)
const loadTimedOut = ref(false)
let loadController: AbortController | null = null

async function load() {
  loadController?.abort()
  const controller = new AbortController()
  loadController = controller
  loadTimedOut.value = false
  character.value = null

  const found = await waitForCharacter(characterId.value, { signal: controller.signal })
  if (controller.signal.aborted) return
  await listUsers()
  if (found) {
    character.value = found
  } else {
    loadTimedOut.value = true
  }
}
onMounted(load)
watch(characterId, load)
onUnmounted(() => loadController?.abort())
```

Template: the existing `v-else` "Loading…" branch splits into a genuine loading state and a
timeout state:

```vue
<div v-if="character" class="mx-auto max-w-4xl p-6">
  <!-- ...unchanged existing content... -->
</div>
<div v-else-if="loadTimedOut" class="p-6">
  <p class="mb-4 text-sm text-slate-600">
    Couldn't load this Character — it may not exist, or may still be taking longer than
    expected to appear.
  </p>
  <div class="flex gap-2">
    <BaseButton @click="load">Retry</BaseButton>
    <router-link
      :to="{ name: 'campaign-overview', params: { universeId: route.params.universeId, campaignId: route.params.campaignId } }"
      class="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50"
    >
      Back to Campaign
    </router-link>
  </div>
</div>
<div v-else class="p-6 text-sm text-slate-500">Loading…</div>
```

(`BaseButton` is already imported in this file for the Archive button — no new import needed
there; `router-link` is a globally-registered Vue Router component, already used elsewhere in this
codebase, e.g. `AppHeader.vue`.)

## Testing

Extend the existing Playwright harness (`web/e2e/`) rather than relying only on manual
verification, since this is exactly the kind of async-timing bug that's easy to regress silently.
One new test, reusing `mockBackend.ts`/`auth.ts` as-is:

- Seed the mock so the create-Character command succeeds but the new Character/Entity are
  deliberately **not yet visible** through the list/get routes for the first ~2 poll intervals
  (simulated read-model lag), then become visible. Assert: the Characters and Entities sidebar
  lists eventually show the new records (not immediately, but within the harness's own polling
  window), and the main pane eventually renders the new Character instead of staying on
  "Loading…" indefinitely.
- A second, smaller test (or an extension of the above): if the Character/Entity are *never* made
  visible (simulate a permanent gap), the main pane reaches the timeout state within a bounded
  time and shows the Retry/Back-to-Campaign UI — this is the regression test for the original bug
  report (infinite "Loading…").

Given the default 750ms/15000ms timeout would make a real-time "never becomes visible" test slow
(~15s), the test should pass a shorter `timeoutMs`/`intervalMs` — but `waitForCharacter`'s options
are internal to the composable, not exposed as component props. Resolve this in the implementation
plan (e.g., a test-only override, or accept the ~15s wall-clock cost for that one test — a decision
left concrete in the plan, not hand-waved here).

## Explicitly Out of Scope

- Any backend/API change — this is entirely a frontend timing/retry fix.
- Generalizing this retry pattern to other creation flows (Universe, Campaign, Entity, Object)
  that have the same theoretical read-model-lag exposure but weren't reported as broken. Follow-up,
  not this branch.
- Making the sidebar retry visible/indicated — deliberately silent per the Decisions section.
