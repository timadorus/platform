# Universe Overview Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace `ManageUniverseModal.vue` with a routed `UniverseOverviewPanel.vue` (no sidebar,
own `AppHeader`), reachable by clicking the 🌍 universe badge from both `WorkspaceView` and
`CampaignPickerView`, showing the same Rename/Creators/Archive elements the modal has today plus a
new "Campaigns" list group for the active Universe.

**Architecture:** A new standalone top-level route (`/universes/:universeId/manage`, sibling of
`campaign-picker`/`universe-picker`, not nested in `WorkspaceView`) hosting a new self-contained
view component that owns its own load/rename/archive/creator API calls (mirroring
`CampaignPickerView.vue`'s and `CampaignOverviewPanel.vue`'s existing shape), plus a new "Campaigns"
list section built on `useCampaigns().listByUniverse` (already used elsewhere, unmodified).

**Tech Stack:** Vue 3 `<script setup>`, Vue Router 4, the existing `useUniverses`/`useUsers`/
`useCampaigns` composables (no composable changes needed), `@playwright/test` for the e2e coverage.

## Global Constraints

- Single flat panel, no tabs — there is no second content area to tab into (unlike the Campaign
  panel's Manage/Configuration split).
- Click-to-reveal Rename (plain text + "Rename" button → input + Save/Cancel), matching
  `ManageCampaignPanel.vue`'s established convention — not the old modal's always-editable input.
- The Campaigns list shows only non-archived Campaigns (the query endpoint already excludes
  archived by default — no client-side filtering needed) and is a simple clickable list, NOT the
  `AggregatePickerGrid` widget (no "+ Create Campaign" affordance in scope).
- No backend/API change. No changes to `useUniverses.ts`/`useCampaigns.ts`/`useUsers.ts` — all
  three composables' existing exports are sufficient as-is.
- No "back to Universe picker" link — matches `CampaignPickerView`'s own existing precedent.
- No retry/timeout polling for eventual consistency on this view (matches the Campaign panel's own
  explicit scope decision).

---

### Task 1: `UniverseOverviewPanel.vue` + its route

**Files:**
- Create: `web/src/views/UniverseOverviewPanel.vue`
- Modify: `web/src/router/index.ts`

**Interfaces:**
- Consumes: `useUniverses()`'s `get`, `rename`, `archive`, `listCreators`, `addCreator`,
  `removeCreator` (all unchanged, already used by `ManageUniverseModal.vue`); `useUsers()`'s
  `users`/`list` (unchanged); `useCampaigns()`'s `campaigns`/`listByUniverse` (unchanged, already
  used by `CampaignPickerView.vue`); `AppHeader.vue`'s `universe-name`/`campaign-name` props
  (unchanged); `BaseButton.vue`'s `variant="danger"`; `ConfirmDialog.vue`'s
  `title`/`message`/`confirm-label`/`@confirm`/`@cancel`; `UserPicker.vue`'s `exclude-ids`/
  `@select`.
- Produces: route name `universe-overview`, path `/universes/:universeId/manage`, no props beyond
  the route's own `universeId` param — Task 2 depends on this route name to wire the two badges.

This task is purely additive: no existing file's *behavior* changes (only `router/index.ts` gains
one new route entry), so the app's current modal-based flow keeps working unchanged until Task 2
wires the badges to the new route instead.

- [ ] **Step 1: Add the route**

In `web/src/router/index.ts`, add this entry to the `routes` array, immediately after the
`campaign-picker` entry (i.e. before the `workspace` entry):

```ts
    {
      path: '/universes/:universeId/manage',
      name: 'universe-overview',
      component: () => import('@/views/UniverseOverviewPanel.vue'),
      props: true,
    },
```

- [ ] **Step 2: Create `UniverseOverviewPanel.vue`**

Create `web/src/views/UniverseOverviewPanel.vue`:

```vue
<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUniverses, type UniverseSummary } from '@/composables/useUniverses'
import { useUsers } from '@/composables/useUsers'
import { useCampaigns } from '@/composables/useCampaigns'
import AppHeader from '@/components/layout/AppHeader.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import UserPicker from '@/components/pickers/UserPicker.vue'

const route = useRoute()
const router = useRouter()
const universeId = computed(() => route.params.universeId as string)

const { get: getUniverse, rename, archive, listCreators, addCreator, removeCreator } = useUniverses()
const { users, list: listUsers } = useUsers()
const { campaigns, listByUniverse } = useCampaigns()

const universe = ref<UniverseSummary | null>(null)
const creatorIds = ref<string[]>([])
const error = ref<string | null>(null)
const loading = ref(true)

const editingName = ref(false)
const nameDraft = ref('')
const showAddCreator = ref(false)
const showArchiveConfirm = ref(false)

const creators = computed(() =>
  creatorIds.value.map((id) => ({ id, name: users.value.find((u) => u.id === id)?.name ?? id })),
)

async function load() {
  loading.value = true
  universe.value = await getUniverse(universeId.value)
  await listUsers()
  creatorIds.value = await listCreators(universeId.value)
  await listByUniverse(universeId.value)
  loading.value = false
}
onMounted(load)

function startEditName() {
  if (!universe.value) return
  nameDraft.value = universe.value.name
  editingName.value = true
}
async function saveName() {
  if (!universe.value) return
  error.value = null
  try {
    await rename(universeId.value, nameDraft.value.trim())
    universe.value.name = nameDraft.value.trim()
    editingName.value = false
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to rename.'
  }
}

async function onAddCreator(userId: string) {
  showAddCreator.value = false
  error.value = null
  try {
    await addCreator(universeId.value, userId)
    creatorIds.value = await listCreators(universeId.value)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to add Creator.'
  }
}

async function onRemoveCreator(userId: string) {
  error.value = null
  try {
    await removeCreator(universeId.value, userId)
    creatorIds.value = await listCreators(universeId.value)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to remove Creator.'
  }
}

async function confirmArchive() {
  showArchiveConfirm.value = false
  error.value = null
  try {
    await archive(universeId.value)
    router.push({ name: 'universe-picker' })
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  }
}

function goToCampaign(campaignId: string) {
  router.push({ name: 'workspace', params: { universeId: universeId.value, campaignId } })
}
</script>

<template>
  <AppHeader :universe-name="universe?.name ?? null" :campaign-name="null" />
  <div v-if="loading" class="p-6 text-sm text-slate-500">Loading…</div>
  <div v-else-if="universe" class="mx-auto max-w-2xl p-6">
    <ErrorBanner :message="error" @dismiss="error = null" />

    <div class="mb-4 flex items-center gap-2">
      <template v-if="editingName">
        <input v-model="nameDraft" type="text" class="flex-1 rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
        <button class="text-xs text-indigo-600 hover:underline" @click="saveName">Save</button>
        <button class="text-xs text-slate-400 hover:underline" @click="editingName = false">Cancel</button>
      </template>
      <template v-else>
        <h1 class="flex-1 text-lg font-semibold text-slate-900">{{ universe.name }}</h1>
        <button class="text-xs text-indigo-600 hover:underline" @click="startEditName">Rename</button>
      </template>
    </div>

    <div class="mb-4">
      <div class="mb-1 flex items-center justify-between">
        <label class="text-xs font-medium text-slate-600">Creators</label>
        <button class="text-xs text-indigo-600 hover:underline" @click="showAddCreator = !showAddCreator">+ Add</button>
      </div>
      <UserPicker v-if="showAddCreator" :exclude-ids="creatorIds" @select="onAddCreator" />
      <ul class="mt-2 space-y-1">
        <li v-for="c in creators" :key="c.id" class="flex items-center justify-between text-sm">
          <span>{{ c.name }}</span>
          <button class="text-xs text-red-500 hover:underline" @click="onRemoveCreator(c.id)">Remove</button>
        </li>
      </ul>
    </div>

    <div class="mb-4">
      <label class="mb-1 block text-xs font-medium text-slate-600">Campaigns</label>
      <ul v-if="campaigns.length" class="space-y-1">
        <li v-for="c in campaigns" :key="c.id">
          <button class="text-sm text-indigo-600 hover:underline" @click="goToCampaign(c.id)">{{ c.name }}</button>
        </li>
      </ul>
      <p v-else class="text-sm text-slate-500">No Campaigns yet.</p>
    </div>

    <div class="border-t border-slate-100 pt-3">
      <BaseButton variant="danger" @click="showArchiveConfirm = true">Archive Universe</BaseButton>
    </div>

    <ConfirmDialog
      v-if="showArchiveConfirm"
      title="Archive Universe"
      message="This universe will be hidden from pickers. This cannot be undone through this app."
      confirm-label="Archive"
      @confirm="confirmArchive"
      @cancel="showArchiveConfirm = false"
    />
  </div>
  <div v-else class="p-6 text-sm text-slate-500">Universe not found.</div>
</template>
```

- [ ] **Step 3: Typecheck and build**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean. This route is unreferenced by any existing link/button yet, so nothing else
should change behavior.

- [ ] **Step 4: Run the existing e2e suite**

Run (from `web/`): `npm run test:e2e`
Expected: the 5 pre-existing tests still pass unchanged (this task adds a new, currently-unreached
route — it cannot affect them).

- [ ] **Step 5: Commit**

```bash
git add web/src/router/index.ts web/src/views/UniverseOverviewPanel.vue
git commit -m "web: add UniverseOverviewPanel.vue and its route (not yet wired to the universe badge)"
```

---

### Task 2: Wire the universe badge, delete the modal

**Files:**
- Modify: `web/src/views/WorkspaceView.vue`
- Modify: `web/src/views/CampaignPickerView.vue`
- Delete: `web/src/components/modals/ManageUniverseModal.vue`

**Interfaces:**
- Consumes: Task 1's `universe-overview` route name.
- Produces: nothing new for later tasks — this task's own e2e coverage is Task 3.

- [ ] **Step 1: Rewrite `WorkspaceView.vue`'s universe-badge handling**

In `web/src/views/WorkspaceView.vue`, remove the `import ManageUniverseModal from
'@/components/modals/ManageUniverseModal.vue'` line, remove the `const showManageUniverse =
ref(false)` line, remove the `onUniverseRenamed`/`onUniverseArchived` functions entirely, and
remove the `<ManageUniverseModal ... />` block from the template (lines 91-98 in the current
file). Add, near the existing `goToCampaignOverview` function:

```ts
function goToUniverseOverview() {
  router.push({ name: 'universe-overview', params: { universeId: universeId.value } })
}
```

Change the `AppHeader`'s binding from `@click-universe-badge="showManageUniverse = true"` to
`@click-universe-badge="goToUniverseOverview"`.

The final `<script setup>` block's relevant portion should read:

```ts
const universe = ref<UniverseSummary | null>(null)
const campaign = ref<CampaignSummary | null>(null)

const sidebarRefreshSignal = ref(0)
provide('sidebarRefreshSignal', sidebarRefreshSignal)
provide('bumpSidebarRefresh', () => {
  sidebarRefreshSignal.value++
})

const pendingEntityId = ref<string | null>(null)
provide('pendingEntityId', pendingEntityId)

async function load() {
  universe.value = await getUniverse(universeId.value)
  campaign.value = await getCampaign(campaignId.value)
}

onMounted(load)
watch([universeId, campaignId], load)
watch(sidebarRefreshSignal, load)

function goToCampaignOverview() {
  router.push({ name: 'campaign-overview', params: { universeId: universeId.value, campaignId: campaignId.value } })
}
function goToUniverseOverview() {
  router.push({ name: 'universe-overview', params: { universeId: universeId.value } })
}
```

And the template's header + closing:

```vue
    <AppHeader
      :universe-name="universe?.name ?? null"
      :campaign-name="campaign?.name ?? null"
      @click-universe-badge="goToUniverseOverview"
      @click-campaign-badge="goToCampaignOverview"
    />
```
(no `<ManageUniverseModal>` block at the end of the template — the component's root `<div>` closes
right after the `<div class="flex flex-1 overflow-hidden">` block).

- [ ] **Step 2: Wire `CampaignPickerView.vue`'s universe badge**

In `web/src/views/CampaignPickerView.vue`, add this function alongside the existing `goTo`
function:

```ts
function goToUniverseOverview() {
  router.push({ name: 'universe-overview', params: { universeId: universeId.value } })
}
```

Change the template's `AppHeader` line from:
```vue
  <AppHeader :universe-name="universe?.name ?? null" :campaign-name="null" />
```
to:
```vue
  <AppHeader :universe-name="universe?.name ?? null" :campaign-name="null" @click-universe-badge="goToUniverseOverview" />
```

- [ ] **Step 3: Delete the modal**

```bash
git rm web/src/components/modals/ManageUniverseModal.vue
```

- [ ] **Step 4: Grep for dangling references**

Run: `grep -rn "ManageUniverseModal" web/src`
Expected: no output.

- [ ] **Step 5: Typecheck and build**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean.

- [ ] **Step 6: Run the existing e2e suite**

Run (from `web/`): `npm run test:e2e`
Expected: the 5 pre-existing tests still pass. None of them click the universe badge or reference
`ManageUniverseModal`, so none should be affected — but confirm this is actually true by reading
`character-creation.spec.ts`, `character-creation-lag.spec.ts`, and `campaign-manage.spec.ts`
before running, and note in your report if any of them turn out to touch this area.

- [ ] **Step 7: Commit**

```bash
git add web/src/views/WorkspaceView.vue web/src/views/CampaignPickerView.vue
git rm web/src/components/modals/ManageUniverseModal.vue
git commit -m "web: navigate to the Universe overview panel from both universe badges instead of opening a modal"
```

---

### Task 3: Mock backend support and regression tests

**Files:**
- Modify: `web/e2e/support/mockBackend.ts`
- Create: `web/e2e/universe-manage.spec.ts`

**Interfaces:**
- Consumes: Task 1's `UniverseOverviewPanel.vue` (exercised through the browser), Task 2's badge
  wiring.
- Produces: `MockState.creatorIds: string[]` (mirroring the existing `gamemasterIds` shape,
  defaulting to `[]`); three new mock routes: `GET /api/query/universes/:universeId/creators`,
  `GET /api/query/universes/:universeId/campaigns`, `PATCH /api/command/universes/:universeId`
  (rename).

- [ ] **Step 1: Add `creatorIds` to `MockState`**

In `web/e2e/support/mockBackend.ts`, add `creatorIds: string[]` to the `MockState` interface
(placed next to the existing `gamemasterIds: string[]`), and `creatorIds: []` to
`createMockState`'s default object (next to the existing `gamemasterIds: []`):

```ts
export interface MockState {
  universes: MockUniverse[]
  campaigns: MockCampaign[]
  users: MockUser[]
  characters: MockCharacter[]
  entities: MockEntity[]
  rulesets: MockRuleset[]
  gamemasterIds: string[]
  creatorIds: string[]
  nextId: number
  createVisibilityDelayMs?: number
}
```

```ts
export function createMockState(overrides: Partial<MockState> = {}): MockState {
  return {
    universes: [],
    campaigns: [],
    users: [],
    characters: [],
    entities: [],
    rulesets: [],
    gamemasterIds: [],
    creatorIds: [],
    nextId: 1,
    ...overrides,
  }
}
```

- [ ] **Step 2: Add the three new routes**

In `web/e2e/support/mockBackend.ts`, add these two GET routes to the `// ---- query API ----`
section, immediately after the existing `/api/query/universes/:universeId` route:

```ts
    if (method === 'GET' && matchPath('/api/query/universes/:universeId/creators', p)) {
      return json(route, state.creatorIds)
    }
    if (method === 'GET' && (m = matchPath('/api/query/universes/:universeId/campaigns', p))) {
      const matches = state.campaigns.filter((c) => c.universeId === m!.params.universeId && !c.isArchived)
      return json(route, matches)
    }
```

Add this command route to the `// ---- command API ----` section, immediately after the existing
`PATCH /api/command/campaigns/:campaignId` route:

```ts
    if (method === 'PATCH' && (m = matchPath('/api/command/universes/:universeId', p))) {
      const body = JSON.parse(req.postData() || '{}') as { name: string }
      const universe = state.universes.find((u) => u.id === m!.params.universeId)
      if (universe) universe.name = body.name
      return route.fulfill({ status: 204, body: '' })
    }
```

- [ ] **Step 3: Write the tests**

Create `web/e2e/universe-manage.spec.ts`:

```ts
import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(overrides: Partial<MockState> = {}): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [
      { id: 'c1', universeId: 'u1', name: 'Test Campaign', rulesetId: 'r1', isArchived: false },
    ],
    rulesets: [{ id: 'r1', name: 'Test Ruleset' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    creatorIds: ['user-1'],
    characters: [
      { id: 'ch1', name: 'Aragorn', campaignId: 'c1', entityId: 'e1', playerUserId: 'user-1', isArchived: false },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('the Universe panel shows the Creators list and the Campaigns list for the active universe', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/manage')

  await expect(page.getByRole('heading', { name: 'Test Universe' })).toBeVisible()
  await expect(page.getByText('devuser@timadorus.local')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Test Campaign' })).toBeVisible()
})

test('the universe badge navigates to the Universe panel from the workspace, and renaming there updates the header', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await expect(page.getByRole('button', { name: '🌍 Test Universe' })).toBeVisible()

  await page.getByRole('button', { name: '🌍 Test Universe' }).click()
  await expect(page).toHaveURL(/\/universes\/u1\/manage$/)
  await expect(page.getByRole('heading', { name: 'Test Universe' })).toBeVisible()

  await page.getByRole('button', { name: 'Rename', exact: true }).click()
  const nameInput = page.locator('input[type="text"]').first()
  await nameInput.fill('Renamed Universe')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Renamed Universe' })).toBeVisible()

  await expect(page.getByRole('button', { name: '🌍 Renamed Universe' })).toBeVisible()
})

test('clicking a Campaign in the Universe panel navigates into its workspace', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/manage')
  await page.getByRole('button', { name: 'Test Campaign' }).click()

  await expect(page).toHaveURL(/\/universes\/u1\/campaigns\/c1$/)
})
```

Note on the second test's `nameInput` locator: this page has NO sidebar (unlike
`campaign-manage.spec.ts`'s equivalent test, which needed `page.locator('main')` to avoid
colliding with `AppSidebar`'s search boxes) — `UniverseOverviewPanel.vue` renders no `<main>`
sidebar-bearing layout at all, so an unscoped `page.locator('input[type="text"]').first()` is safe
here. Verify this assumption is actually true by running the test; if it turns out there IS some
other `type="text"` input earlier in the DOM on this page, apply the same `page.locator('main')`-
style scoping fix `campaign-manage.spec.ts` needed, and note it in your report rather than
silently weakening the assertion.

- [ ] **Step 4: Run the full suite**

Run (from `web/`): `npm run test:e2e`
Expected: 8 passed (the 5 existing tests plus these 3 new ones). Run it twice to confirm no flakes.

- [ ] **Step 5: Typecheck and build**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add web/e2e/support/mockBackend.ts web/e2e/universe-manage.spec.ts
git commit -m "web/e2e: test the Universe overview panel (Creators/Campaigns list, badge navigation, rename freshness, Campaign navigation)"
```

---

## Final Verification

- `npm run typecheck && npm run build && npm run test:e2e` (from `web/`) all clean — 8 tests
  passing, no flakes across at least two full runs.
- `go build ./... && go vet ./... && go test ./...` unaffected (this plan touches no Go code) —
  confirm anyway.
- Grep the whole `web/src` tree for `ManageUniverseModal` to confirm no dangling reference
  survives its deletion.
- Grep `web/src` for `showManageUniverse` to confirm it's fully gone from `WorkspaceView.vue`.
