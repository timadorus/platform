# Character Creation: Tolerate Read-Model Lag Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** After creating a Character, the Characters/Entities sidebar lists retry until the new
records actually appear (instead of a single un-retried refresh), and `CharacterDetailView`'s main
pane retries with a bounded timeout instead of being stuck on "Loading…" forever — per
`docs/superpowers/specs/2026-08-30-character-creation-eventual-consistency-design.md`.

**Architecture:** Extends the poll-with-timeout pattern already proven for User creation
(`useUsers.ts`'s `waitForUser` / `CreatingUserModal.vue`) to three new call sites: two
list-membership polls (Characters, Entities sidebars) and one single-entity poll (the main pane).
A new, narrowly-scoped `pendingEntityId` provide/inject pair lets `CharactersPanel` tell the
sibling `EntitiesPanel` which specific Entity id to wait for.

**Tech Stack:** Vue 3 (`<script setup>`, TypeScript), `@playwright/test` (existing harness).

## Global Constraints

- No backend/API changes anywhere in this plan — entirely a frontend timing/retry fix.
- Poll interval/timeout defaults are 750ms / 15000ms everywhere, matching `waitForUser` exactly —
  no new tuning knobs beyond the existing `opts: { intervalMs?, timeoutMs?, signal? }` shape every
  new function takes.
- `waitForCharacter` (single-entity poll) cannot distinguish "still syncing" from "genuinely
  doesn't exist" — every failed attempt is retried until the deadline; the timeout UI shows one
  unified message, not a distinct error-vs-timeout split.
- `waitForCharacterInList`/`waitForEntityInList` (list-membership polls) mirror `waitForUser`
  exactly, including checking the composable's own `error.value` for an early bail on a real
  failure.
- Sidebar retries are silent — no loading indicator anywhere in `CharactersPanel.vue`/
  `EntitiesPanel.vue`.
- `CharacterDetailView`'s timeout state gets a Retry button (re-invokes `load()`) and a
  Back-to-Campaign link.
- `CharacterDetailView`'s poll must be aborted both on unmount and at the start of every new
  `load()` call — the component instance is reused across param-only route changes (switching
  Characters via the sidebar), so a stale poll for the *previous* Character must not overwrite the
  page after the user has navigated to a new one.
- `npm run typecheck`, `npm run build`, and `npm run test:e2e` must all stay clean after every
  task. The existing `character-creation.spec.ts` must keep passing unmodified — this plan does
  not change its assertions, only makes the underlying app more robust.
- File layout after this plan:
  - `web/src/composables/useCharacters.ts` (modified — two new exports)
  - `web/src/composables/useEntities.ts` (modified — one new export)
  - `web/src/components/modals/CreateCharacterModal.vue` (modified — emits `entityId` too)
  - `web/src/views/WorkspaceView.vue` (modified — new `pendingEntityId` provide)
  - `web/src/components/layout/CharactersPanel.vue` (modified)
  - `web/src/components/layout/EntitiesPanel.vue` (modified)
  - `web/src/views/CharacterDetailView.vue` (modified)
  - `web/e2e/support/mockBackend.ts` (modified — simulated read-model lag)
  - `web/e2e/character-creation-lag.spec.ts` (new)

---

### Task 1: Composable and component changes (the actual fix)

**Files:**
- Modify: `web/src/composables/useCharacters.ts`
- Modify: `web/src/composables/useEntities.ts`
- Modify: `web/src/components/modals/CreateCharacterModal.vue`
- Modify: `web/src/views/WorkspaceView.vue`
- Modify: `web/src/components/layout/CharactersPanel.vue`
- Modify: `web/src/components/layout/EntitiesPanel.vue`
- Modify: `web/src/views/CharacterDetailView.vue`

**Interfaces:**
- Produces: `useCharacters().waitForCharacter(id, opts?): Promise<CharacterSummary | null>`,
  `useCharacters().waitForCharacterInList(campaignId, characterId, opts?): Promise<boolean>`,
  `useEntities().waitForEntityInList(universeId, entityId, opts?): Promise<boolean>`, a
  `pendingEntityId: Ref<string | null>` provided by `WorkspaceView.vue` under the key
  `'pendingEntityId'`, and `CreateCharacterModal`'s `created` event now carrying
  `[characterId: string, entityId: string]` instead of `[characterId: string]`.
- Consumed by: Task 2's new test file (via the app's actual behavior, not by importing these
  functions directly).

- [ ] **Step 1: Add the two new Character polling helpers**

In `web/src/composables/useCharacters.ts`, change:

```ts
  async function setPlayer(id: string, userId: string): Promise<void> {
    const { error: apiError } = await getCommandClient().PUT('/characters/{characterId}/player', {
      params: { path: { characterId: id } },
      body: { userId },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to reassign Player.')
  }

  return { characters, loading, error, list, get, create, rename, archive, setPlayer }
}
```

to:

```ts
  async function setPlayer(id: string, userId: string): Promise<void> {
    const { error: apiError } = await getCommandClient().PUT('/characters/{characterId}/player', {
      params: { path: { characterId: id } },
      body: { userId },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to reassign Player.')
  }

  // waitForCharacter polls get(id) until it succeeds or timeoutMs elapses. Unlike waitForUser
  // (useUsers.ts), this cannot distinguish "the projector hasn't caught up yet" from "this id
  // doesn't exist" — get() 404s identically either way — so every failed attempt is treated the
  // same and retried until the deadline; callers should show one honest "couldn't load" message
  // on timeout, not a distinct error state.
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

  return {
    characters,
    loading,
    error,
    list,
    get,
    create,
    rename,
    archive,
    setPlayer,
    waitForCharacter,
    waitForCharacterInList,
  }
}
```

- [ ] **Step 2: Add the Entity list polling helper**

In `web/src/composables/useEntities.ts`, change:

```ts
  async function archive(id: string): Promise<void> {
    const { error: apiError } = await getCommandClient().POST('/entities/{entityId}/archive', {
      params: { path: { entityId: id } },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to archive Entity.')
  }

  return { entities, loading, error, search, get, create, rename, archive }
}
```

to:

```ts
  async function archive(id: string): Promise<void> {
    const { error: apiError } = await getCommandClient().POST('/entities/{entityId}/archive', {
      params: { path: { entityId: id } },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to archive Entity.')
  }

  // waitForEntityInList polls search(universeId, '') — deliberately unfiltered, regardless of
  // any search term the caller might otherwise be using, so a new Entity is guaranteed findable
  // rather than potentially excluded by an unrelated in-progress filter — until entityId appears
  // or timeoutMs elapses. Mirrors useUsers.ts's waitForUser / useCharacters.ts's
  // waitForCharacterInList.
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

  return { entities, loading, error, search, get, create, rename, archive, waitForEntityInList }
}
```

- [ ] **Step 3: Emit `entityId` from the create modal**

In `web/src/components/modals/CreateCharacterModal.vue`, change:

```ts
const emit = defineEmits<{ close: []; created: [characterId: string] }>()
```

to:

```ts
const emit = defineEmits<{ close: []; created: [characterId: string, entityId: string] }>()
```

and change:

```ts
    const { characterId } = await create(props.campaignId, name.value.trim(), playerUserId.value)
    emit('created', characterId)
```

to:

```ts
    const { characterId, entityId } = await create(props.campaignId, name.value.trim(), playerUserId.value)
    emit('created', characterId, entityId)
```

- [ ] **Step 4: Add the `pendingEntityId` shared ref**

In `web/src/views/WorkspaceView.vue`, change:

```ts
const sidebarRefreshSignal = ref(0)
provide('sidebarRefreshSignal', sidebarRefreshSignal)
provide('bumpSidebarRefresh', () => {
  sidebarRefreshSignal.value++
})
```

to:

```ts
const sidebarRefreshSignal = ref(0)
provide('sidebarRefreshSignal', sidebarRefreshSignal)
provide('bumpSidebarRefresh', () => {
  sidebarRefreshSignal.value++
})

// pendingEntityId lets CharactersPanel tell EntitiesPanel "a new Entity with this id was just
// auto-created (via Character creation) — poll for it specifically" without overloading the
// generic sidebarRefreshSignal above (which fires for rename/archive/reassign flows that don't
// need retrying). CharactersPanel sets it; EntitiesPanel watches it, polls, and clears it.
const pendingEntityId = ref<string | null>(null)
provide('pendingEntityId', pendingEntityId)
```

- [ ] **Step 5: Update `CharactersPanel.vue`**

Change:

```ts
const { characters, list } = useCharacters()
```

to:

```ts
const { characters, list, waitForCharacterInList } = useCharacters()
```

Change:

```ts
const bumpSidebarRefresh = inject<() => void>('bumpSidebarRefresh')
```

to:

```ts
const pendingEntityId = inject<Ref<string | null>>('pendingEntityId')
```

Change:

```ts
function onCreated(characterId: string) {
  showCreate.value = false
  // Creating a Character also auto-creates its paired Entity (plan §4.4) — bumping the shared
  // signal (rather than calling refresh() directly) updates this panel's own list via its own
  // watcher above *and* EntitiesPanel's, so the new Entity shows up in the sidebar too.
  bumpSidebarRefresh?.()
  select(characterId)
}
```

to:

```ts
function onCreated(characterId: string, entityId: string) {
  showCreate.value = false
  select(characterId)
  // Poll this panel's own list in the background until the new Character actually appears —
  // list() reassigns `characters` reactively as a side effect, which the template already
  // renders, so nothing further needs to happen once this resolves. Not awaited: this is a
  // fire-and-forget background retry, not something the caller needs to wait on.
  waitForCharacterInList(props.campaignId, characterId)
  // The auto-created Entity lives in a sibling panel (EntitiesPanel) — tell it which id to wait
  // for via the shared pendingEntityId ref (WorkspaceView.vue), rather than the generic
  // sidebarRefreshSignal (which only fires once, with no retry).
  if (pendingEntityId) pendingEntityId.value = entityId
}
```

No template change is needed — `@created="onCreated"` already forwards every emitted argument
positionally.

- [ ] **Step 6: Update `EntitiesPanel.vue`**

Change:

```ts
const { entities, loading, search } = useEntities()
```

to:

```ts
const { entities, loading, search, waitForEntityInList } = useEntities()
```

Change:

```ts
const sidebarRefreshSignal = inject<Ref<number>>('sidebarRefreshSignal')
if (sidebarRefreshSignal) {
  watch(sidebarRefreshSignal, () => {
    search(props.universeId, currentQuery.value)
  })
}
```

to:

```ts
const sidebarRefreshSignal = inject<Ref<number>>('sidebarRefreshSignal')
if (sidebarRefreshSignal) {
  watch(sidebarRefreshSignal, () => {
    search(props.universeId, currentQuery.value)
  })
}

// pendingEntityId (WorkspaceView.vue) names an Entity that was just auto-created elsewhere (via
// Character creation) and might not be visible yet — poll for it specifically, rather than
// relying on the generic sidebarRefreshSignal above, which only re-searches once with no retry.
const pendingEntityId = inject<Ref<string | null>>('pendingEntityId')
if (pendingEntityId) {
  watch(pendingEntityId, async (id) => {
    if (!id) return
    await waitForEntityInList(props.universeId, id)
    pendingEntityId.value = null
  })
}
```

- [ ] **Step 7: Update `CharacterDetailView.vue`**

Change the import line:

```ts
import { computed, inject, onMounted, ref, watch } from 'vue'
```

to:

```ts
import { computed, inject, onMounted, onUnmounted, ref, watch } from 'vue'
```

Add `BaseButton`'s import alongside the other component imports — change:

```ts
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
```

to:

```ts
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
```

Change:

```ts
const { get, rename, archive, setPlayer } = useCharacters()
const { users, list: listUsers } = useUsers()
const bumpSidebarRefresh = inject<() => void>('bumpSidebarRefresh')

const character = ref<CharacterSummary | null>(null)
const error = ref<string | null>(null)
const showArchiveConfirm = ref(false)

const activeTab = ref('Stats')
const tabs = ['Stats', 'Skills', 'Equipment', 'Journal']

const playerName = computed(
  () => users.value.find((u) => u.id === character.value?.playerUserId)?.name ?? character.value?.playerUserId ?? '',
)

async function load() {
  character.value = await get(characterId.value)
  await listUsers()
}
onMounted(load)
watch(characterId, load)
```

to:

```ts
const { get, rename, archive, setPlayer, waitForCharacter } = useCharacters()
const { users, list: listUsers } = useUsers()
const bumpSidebarRefresh = inject<() => void>('bumpSidebarRefresh')

const character = ref<CharacterSummary | null>(null)
const error = ref<string | null>(null)
const showArchiveConfirm = ref(false)
const loadTimedOut = ref(false)

const activeTab = ref('Stats')
const tabs = ['Stats', 'Skills', 'Equipment', 'Journal']

const playerName = computed(
  () => users.value.find((u) => u.id === character.value?.playerUserId)?.name ?? character.value?.playerUserId ?? '',
)

// loadController is aborted both on unmount and at the start of every new load() call — the
// latter matters because vue-router reuses this component instance across param-only route
// changes (see characterId's own comment above): switching to a different Character mid-poll
// must not let a slow response for the *previous* one land after navigation and overwrite the
// page with the wrong data.
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

Change the template's final `v-else` branch — from:

```vue
  <div v-else class="p-6 text-sm text-slate-500">Loading…</div>
</template>
```

to:

```vue
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
</template>
```

- [ ] **Step 8: Typecheck, build, and re-run the existing e2e suite**

Run (from `web/`): `npm run typecheck && npm run build && npm run test:e2e`
Expected: all clean; the existing `character-creation.spec.ts` test must still pass unmodified —
this task changes no user-visible happy-path behavior, only adds retry/timeout handling around it.
If it fails, read the failure carefully before assuming it's a pre-existing flake — this task
touches every file that test exercises.

- [ ] **Step 9: Commit**

```bash
git add web/src/composables/useCharacters.ts web/src/composables/useEntities.ts \
  web/src/components/modals/CreateCharacterModal.vue web/src/views/WorkspaceView.vue \
  web/src/components/layout/CharactersPanel.vue web/src/components/layout/EntitiesPanel.vue \
  web/src/views/CharacterDetailView.vue
git commit -m "web: retry sidebar refresh and main-pane load after Character creation, with a bounded timeout on the main pane"
```

---

### Task 2: Regression tests for read-model lag

**Files:**
- Modify: `web/e2e/support/mockBackend.ts`
- Create: `web/e2e/character-creation-lag.spec.ts`

**Interfaces:**
- Consumes: Task 1's actual app behavior (not imported directly — exercised through the browser).
- Produces: `MockState.createVisibilityDelayMs?: number`, `MockCharacter.visibleAt?: number`,
  `MockEntity.visibleAt?: number` — all optional, defaulting to "always visible" so the existing
  `character-creation.spec.ts` (which never sets these) is completely unaffected.

- [ ] **Step 1: Add `visibleAt` to the mock's Character/Entity records**

In `web/e2e/support/mockBackend.ts`, change:

```ts
export interface MockCharacter {
  id: string
  name: string
  campaignId: string
  entityId: string
  playerUserId: string
  isArchived: boolean
}

export interface MockEntity {
  id: string
  name: string
  universeId: string
  isArchived: boolean
}
```

to:

```ts
export interface MockCharacter {
  id: string
  name: string
  campaignId: string
  entityId: string
  playerUserId: string
  isArchived: boolean
  // visibleAt (epoch ms) simulates read-model lag: unset means "always visible" (the default,
  // matching every existing test's expectations); set means query routes hide this record until
  // Date.now() reaches it, even though it already exists in `state`.
  visibleAt?: number
}

export interface MockEntity {
  id: string
  name: string
  universeId: string
  isArchived: boolean
  visibleAt?: number
}
```

- [ ] **Step 2: Add `createVisibilityDelayMs` to `MockState`**

Change:

```ts
export interface MockState {
  universes: MockUniverse[]
  campaigns: MockCampaign[]
  users: MockUser[]
  characters: MockCharacter[]
  entities: MockEntity[]
  rulesets: MockRuleset[]
  gamemasterIds: string[]
  nextId: number
}
```

to:

```ts
export interface MockState {
  universes: MockUniverse[]
  campaigns: MockCampaign[]
  users: MockUser[]
  characters: MockCharacter[]
  entities: MockEntity[]
  rulesets: MockRuleset[]
  gamemasterIds: string[]
  nextId: number
  // createVisibilityDelayMs, when set, makes the create-Character command's new Character and
  // Entity invisible to every query route for this many milliseconds after creation — simulating
  // an async projector that hasn't caught up yet. Unset (the default) means immediately visible,
  // matching every existing test's expectations.
  createVisibilityDelayMs?: number
}
```

No change is needed to `createMockState`'s base object — `createVisibilityDelayMs` is optional and
simply absent (`undefined`) unless a test's `overrides` sets it.

- [ ] **Step 3: Make the query routes respect `visibleAt`**

Change:

```ts
    if (method === 'GET' && (m = matchPath('/api/query/campaigns/:campaignId/characters', p))) {
      return json(route, state.characters.filter((c) => c.campaignId === m!.params.campaignId && !c.isArchived))
    }
    if (method === 'GET' && (m = matchPath('/api/query/characters/:characterId', p))) {
      const character = state.characters.find((c) => c.id === m!.params.characterId)
      return character ? json(route, character) : json(route, { title: 'not found' }, 404)
    }
    if (method === 'GET' && (m = matchPath('/api/query/universes/:universeId/entities', p))) {
      const name = query.get('name')?.toLowerCase() ?? ''
      const matches = state.entities.filter(
        (e) => e.universeId === m!.params.universeId && !e.isArchived && (!name || e.name.toLowerCase().includes(name)),
      )
      return json(route, matches)
    }
```

to:

```ts
    if (method === 'GET' && (m = matchPath('/api/query/campaigns/:campaignId/characters', p))) {
      const visible = state.characters.filter(
        (c) =>
          c.campaignId === m!.params.campaignId &&
          !c.isArchived &&
          (c.visibleAt === undefined || c.visibleAt <= Date.now()),
      )
      return json(route, visible)
    }
    if (method === 'GET' && (m = matchPath('/api/query/characters/:characterId', p))) {
      const character = state.characters.find((c) => c.id === m!.params.characterId)
      const visible = character && (character.visibleAt === undefined || character.visibleAt <= Date.now())
      return visible ? json(route, character) : json(route, { title: 'not found' }, 404)
    }
    if (method === 'GET' && (m = matchPath('/api/query/universes/:universeId/entities', p))) {
      const name = query.get('name')?.toLowerCase() ?? ''
      const matches = state.entities.filter(
        (e) =>
          e.universeId === m!.params.universeId &&
          !e.isArchived &&
          (!name || e.name.toLowerCase().includes(name)) &&
          (e.visibleAt === undefined || e.visibleAt <= Date.now()),
      )
      return json(route, matches)
    }
```

- [ ] **Step 4: Set `visibleAt` when creating a Character**

Change:

```ts
    if (method === 'POST' && (m = matchPath('/api/command/campaigns/:campaignId/characters', p))) {
      const body = JSON.parse(req.postData() || '{}') as { name: string; playerUserId: string }
      const characterId = newId(state, 'character')
      const entityId = newId(state, 'entity')
      const campaign = state.campaigns.find((c) => c.id === m!.params.campaignId)
      state.characters.push({
        id: characterId,
        name: body.name,
        campaignId: m!.params.campaignId,
        entityId,
        playerUserId: body.playerUserId,
        isArchived: false,
      })
      // Mirrors internal/command/character/service.go's real cross-aggregate CreateCharacter:
      // the auto-created Entity gets the same name as the Character.
      state.entities.push({
        id: entityId,
        name: body.name,
        universeId: campaign?.universeId ?? '',
        isArchived: false,
      })
      return json(route, { characterId, entityId }, 201)
    }
```

to:

```ts
    if (method === 'POST' && (m = matchPath('/api/command/campaigns/:campaignId/characters', p))) {
      const body = JSON.parse(req.postData() || '{}') as { name: string; playerUserId: string }
      const characterId = newId(state, 'character')
      const entityId = newId(state, 'entity')
      const campaign = state.campaigns.find((c) => c.id === m!.params.campaignId)
      // visibleAt simulates real read-model lag: when createVisibilityDelayMs is configured, the
      // new Character/Entity exist in `state` immediately (matching the real backend's write-side
      // truth) but don't appear through any query route until that many milliseconds have passed
      // — exactly what a slow projector looks like from the SPA's perspective.
      const visibleAt = state.createVisibilityDelayMs !== undefined ? Date.now() + state.createVisibilityDelayMs : undefined
      state.characters.push({
        id: characterId,
        name: body.name,
        campaignId: m!.params.campaignId,
        entityId,
        playerUserId: body.playerUserId,
        isArchived: false,
        visibleAt,
      })
      // Mirrors internal/command/character/service.go's real cross-aggregate CreateCharacter:
      // the auto-created Entity gets the same name as the Character.
      state.entities.push({
        id: entityId,
        name: body.name,
        universeId: campaign?.universeId ?? '',
        isArchived: false,
        visibleAt,
      })
      return json(route, { characterId, entityId }, 201)
    }
```

- [ ] **Step 5: Write the two new tests**

Create `web/e2e/character-creation-lag.spec.ts`:

```ts
import { test, expect, type Page } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(overrides: Partial<MockState> = {}): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [{ id: 'c1', universeId: 'u1', name: 'Test Campaign', rulesetId: 'r1', isArchived: false }],
    rulesets: [{ id: 'r1', name: 'Test Ruleset' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    gamemasterIds: ['user-1'],
    ...overrides,
  })
}

async function createCharacter(page: Page, name: string) {
  await page.getByRole('button', { name: '+ Create Character' }).click()
  await expect(page.getByRole('heading', { name: 'Create Character' })).toBeVisible()
  const modalForm = page.locator('form')
  await modalForm.locator('input[type="text"]').first().fill(name)
  await page.getByPlaceholder('Search users…').fill('devuser')
  await page.getByRole('button', { name: 'devuser@timadorus.local' }).click()
  await modalForm.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Create Character' })).toHaveCount(0)
}

test('sidebar lists and the main pane eventually reflect a newly created Character despite read-model lag', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  // ~2 poll intervals (waitForCharacter/waitForCharacterInList/waitForEntityInList all default
  // to a 750ms interval) — long enough to prove the retry actually happens, short enough to keep
  // this test fast.
  const state = seedState({ createVisibilityDelayMs: 1600 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')
  await createCharacter(page, 'Samwise Gamgee')

  const charactersSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Characters' }) })
  await expect(charactersSection.getByText('Samwise Gamgee')).toBeVisible()

  const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
  await expect(entitiesSection.getByRole('button', { name: 'Samwise Gamgee' })).toBeVisible()

  const baseInfo = page.locator('div.rounded-md').filter({ hasText: 'Base Info' })
  await expect(baseInfo.getByText('Samwise Gamgee')).toBeVisible()
})

test('the main pane shows a Retry/Back-to-Campaign timeout state if the Character never becomes visible', async ({
  page,
  context,
  baseURL,
}) => {
  test.setTimeout(45_000)
  const base = baseURL!
  const authority = `${base}/oidc`
  // Far beyond waitForCharacter's own 15s timeout — the Character never becomes visible within
  // this test's lifetime, exercising the "give up and show an error" path.
  const state = seedState({ createVisibilityDelayMs: 999_999_999 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')
  await createCharacter(page, 'Peregrin Took')

  await expect(page.getByText("Couldn't load this Character", { exact: false })).toBeVisible({ timeout: 20_000 })
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()

  const backLink = page.getByRole('link', { name: 'Back to Campaign' })
  await expect(backLink).toBeVisible()
  await backLink.click()
  await expect(page).toHaveURL(/\/universes\/u1\/campaigns\/c1$/)
})
```

- [ ] **Step 6: Run the full suite**

Run (from `web/`): `npm run test:e2e`
Expected: 3 passed (the existing happy-path test plus these two). The lag test should take
roughly 2-3 seconds; the timeout test roughly 15-20 seconds (it's genuinely waiting out
`waitForCharacter`'s real 15-second timeout — this is a deliberate, acceptable cost for exercising
the actual timeout path, not a flake). Run it twice to confirm the timing isn't borderline/flaky.

- [ ] **Step 7: Typecheck and build**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean.

- [ ] **Step 8: Commit**

```bash
git add web/e2e/support/mockBackend.ts web/e2e/character-creation-lag.spec.ts
git commit -m "web/e2e: simulate read-model lag and test the new retry/timeout behavior"
```

---

## Final Verification

- `npm run typecheck && npm run build && npm run test:e2e` (from `web/`) all clean — 3 tests
  passing, no flakes across at least two full runs.
- `go build ./... && go vet ./... && go test ./...` unaffected (this plan touches no Go code) —
  re-confirm anyway.
- Manually skim the final `CharacterDetailView.vue` and `mockBackend.ts` as whole files (not just
  the diffs) to confirm the edits compose cleanly with everything already in each file from prior
  branches (e.g. the `:key="character.id"` on `BaseInfoTable` from the Character-detail-redesign
  branch's fix wave — a different mechanism solving a different problem, both should coexist
  without conflict).
