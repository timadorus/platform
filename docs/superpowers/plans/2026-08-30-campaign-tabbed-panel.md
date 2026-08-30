# Campaign Detail: Tabbed Manage/Configuration Panel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Selecting a Campaign shows one tabbed main-area panel (Manage / Configuration) instead of
a read-only summary plus a separate "Manage Campaign" modal, per
`docs/superpowers/specs/2026-08-30-campaign-tabbed-panel-design.md`.

**Architecture:** Two new presentational components (`ManageCampaignPanel`, `ConfigurationPanel`)
hosted by a rewritten `CampaignOverviewPanel.vue` (the routed view, which now owns the actual
rename/archive/gamemaster API calls that `ManageCampaignModal.vue` used to own, before it's
deleted). `WorkspaceView.vue`'s campaign badge navigates to this panel instead of opening the
modal, and reuses the existing `sidebarRefreshSignal` to keep the header's campaign name fresh
after a rename.

**Tech Stack:** Vue 3 (`<script setup>`, TypeScript), Tailwind CSS v4, `@playwright/test`.

## Global Constraints

- No backend/API changes — the `configuration` field and its endpoints already exist server-side.
- Rename in the Manage tab uses click-to-reveal inline editing (matching `BaseInfoTable.vue`'s
  established pattern) — not an always-editable input.
- `MockCampaign.configuration` in the test harness must be **optional**, so neither existing
  Playwright test needs to change.
- No retry/timeout polling is added anywhere in this plan — `CampaignOverviewPanel`'s `load()`
  keeps its existing single-attempt behavior, unchanged from before this plan.
- `npm run typecheck`, `npm run build`, and `npm run test:e2e` must all stay clean after every
  task. Both existing e2e test files must keep passing unmodified.
- File layout after this plan:
  - `web/src/components/campaign/ManageCampaignPanel.vue` (new)
  - `web/src/components/campaign/ConfigurationPanel.vue` (new)
  - `web/src/composables/useCampaigns.ts` (modified — `configuration` field)
  - `web/src/views/CampaignOverviewPanel.vue` (rewritten)
  - `web/src/components/modals/ManageCampaignModal.vue` (deleted)
  - `web/src/views/WorkspaceView.vue` (modified)
  - `web/e2e/support/mockBackend.ts` (modified)
  - `web/e2e/campaign-manage.spec.ts` (new)

---

### Task 1: New presentational components

**Files:**
- Create: `web/src/components/campaign/ManageCampaignPanel.vue`
- Create: `web/src/components/campaign/ConfigurationPanel.vue`
- Modify: `web/src/composables/useCampaigns.ts`

**Interfaces:**
- Produces: `ManageCampaignPanel` — props `{ name: string; rulesetName: string; gamemasters:
  {id: string; name: string}[]; characterCount: number; entityCount: number; objectCount: number
  }`, emits `submit-rename: [name: string]`, `add-gamemaster: [userId: string]`,
  `remove-gamemaster: [userId: string]`, `archive: []`. `ConfigurationPanel` — props `{
  configuration: string }`, no emits. `CampaignSummary` gains `configuration: string`.
- Consumed by: Task 2's rewritten `CampaignOverviewPanel.vue`.

- [ ] **Step 1: Add `configuration` to `CampaignSummary`**

In `web/src/composables/useCampaigns.ts`, change:

```ts
export interface CampaignSummary {
  id: string
  name: string
  universeId: string
  rulesetId: string
  isArchived: boolean
}
```

to:

```ts
export interface CampaignSummary {
  id: string
  name: string
  universeId: string
  rulesetId: string
  configuration: string
  isArchived: boolean
}
```

No other change to this file — `get()` already returns the full backend response, which has
always included `configuration` server-side.

- [ ] **Step 2: Create the Manage panel**

Create `web/src/components/campaign/ManageCampaignPanel.vue`:

```vue
<script setup lang="ts">
import { ref } from 'vue'
import BaseButton from '@/components/common/BaseButton.vue'
import UserPicker from '@/components/pickers/UserPicker.vue'

const props = defineProps<{
  name: string
  rulesetName: string
  gamemasters: { id: string; name: string }[]
  characterCount: number
  entityCount: number
  objectCount: number
}>()
const emit = defineEmits<{
  'submit-rename': [name: string]
  'add-gamemaster': [userId: string]
  'remove-gamemaster': [userId: string]
  archive: []
}>()

const editingName = ref(false)
const nameDraft = ref('')
function startEditName() {
  nameDraft.value = props.name
  editingName.value = true
}
function saveName() {
  emit('submit-rename', nameDraft.value.trim())
  editingName.value = false
}

const showAddGamemaster = ref(false)
function onSelectGamemaster(userId: string) {
  showAddGamemaster.value = false
  emit('add-gamemaster', userId)
}
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4">
    <div class="mb-4 flex items-center gap-2">
      <template v-if="editingName">
        <input v-model="nameDraft" type="text" class="flex-1 rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
        <button class="text-xs text-indigo-600 hover:underline" @click="saveName">Save</button>
        <button class="text-xs text-slate-400 hover:underline" @click="editingName = false">Cancel</button>
      </template>
      <template v-else>
        <h1 class="flex-1 text-lg font-semibold text-slate-900">{{ name }}</h1>
        <button class="text-xs text-indigo-600 hover:underline" @click="startEditName">Rename</button>
      </template>
    </div>

    <p class="mb-4 text-sm text-slate-500">Ruleset: {{ rulesetName }}</p>

    <div class="mb-4">
      <div class="mb-1 flex items-center justify-between">
        <label class="text-xs font-medium text-slate-600">Gamemasters</label>
        <button class="text-xs text-indigo-600 hover:underline" @click="showAddGamemaster = !showAddGamemaster">+ Add</button>
      </div>
      <UserPicker v-if="showAddGamemaster" :exclude-ids="gamemasters.map((g) => g.id)" @select="onSelectGamemaster" />
      <ul class="mt-2 space-y-1">
        <li v-for="gm in gamemasters" :key="gm.id" class="flex items-center justify-between text-sm">
          <span>{{ gm.name }}</span>
          <button class="text-xs text-red-500 hover:underline" @click="emit('remove-gamemaster', gm.id)">Remove</button>
        </li>
      </ul>
    </div>

    <div class="mb-4 grid grid-cols-3 gap-3">
      <div class="rounded-md border border-slate-200 p-3 text-center">
        <p class="text-xl font-semibold text-slate-900">{{ characterCount }}</p>
        <p class="text-xs text-slate-500">Characters</p>
      </div>
      <div class="rounded-md border border-slate-200 p-3 text-center">
        <p class="text-xl font-semibold text-slate-900">{{ entityCount }}</p>
        <p class="text-xs text-slate-500">Entities</p>
      </div>
      <div class="rounded-md border border-slate-200 p-3 text-center">
        <p class="text-xl font-semibold text-slate-900">{{ objectCount }}</p>
        <p class="text-xs text-slate-500">Objects</p>
      </div>
    </div>

    <div class="border-t border-slate-100 pt-3">
      <BaseButton variant="danger" @click="emit('archive')">Archive Campaign</BaseButton>
    </div>
  </div>
</template>
```

- [ ] **Step 3: Create the Configuration panel**

Create `web/src/components/campaign/ConfigurationPanel.vue`:

```vue
<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ configuration: string }>()

// The Configuration field is an opaque JSON string set via PUT /campaigns/{id}/configure — it
// starts unset/empty until that endpoint is called at least once, which is not valid JSON, so
// pretty-printing is attempted defensively rather than assumed to always succeed.
const prettyPrinted = computed(() => {
  if (!props.configuration) return null
  try {
    return JSON.stringify(JSON.parse(props.configuration), null, 2)
  } catch {
    return props.configuration
  }
})
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4">
    <h2 class="mb-3 text-sm font-semibold text-slate-900">Configuration</h2>
    <p v-if="!prettyPrinted" class="text-sm text-slate-500">No configuration set yet.</p>
    <pre v-else class="overflow-x-auto rounded-md bg-slate-50 p-3 text-xs text-slate-800">{{ prettyPrinted }}</pre>
  </div>
</template>
```

- [ ] **Step 4: Typecheck and build**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean. (Neither new component is imported anywhere yet — this only proves each file is
independently well-typed. `CampaignSummary`'s widened interface may show a type error at its one
existing call site if something destructures it exhaustively — check `npm run build`'s output
carefully; if it fails, fix the actual call site, don't weaken the interface.)

- [ ] **Step 5: Commit**

```bash
git add web/src/components/campaign/ManageCampaignPanel.vue web/src/components/campaign/ConfigurationPanel.vue web/src/composables/useCampaigns.ts
git commit -m "web: add ManageCampaignPanel, ConfigurationPanel components; expose Campaign.configuration"
```

---

### Task 2: Rewrite CampaignOverviewPanel, retire the modal

**Files:**
- Modify: `web/src/views/CampaignOverviewPanel.vue`
- Delete: `web/src/components/modals/ManageCampaignModal.vue`
- Modify: `web/src/views/WorkspaceView.vue`

**Interfaces:**
- Consumes: `ManageCampaignPanel`, `ConfigurationPanel` (Task 1) — exact props/emits as given
  above.

- [ ] **Step 1: Replace `CampaignOverviewPanel.vue`**

Replace the full content of `web/src/views/CampaignOverviewPanel.vue` with:

```vue
<script setup lang="ts">
import { computed, inject, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useCampaigns, type CampaignSummary } from '@/composables/useCampaigns'
import { useRulesets } from '@/composables/useRulesets'
import { useUsers } from '@/composables/useUsers'
import { useCharacters } from '@/composables/useCharacters'
import { useEntities } from '@/composables/useEntities'
import { useObjects } from '@/composables/useObjects'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import BaseTabs from '@/components/common/BaseTabs.vue'
import ManageCampaignPanel from '@/components/campaign/ManageCampaignPanel.vue'
import ConfigurationPanel from '@/components/campaign/ConfigurationPanel.vue'

const route = useRoute()
const router = useRouter()
// computed, not a plain const: this panel is the `workspace` route's default child (path
// `''`), so vue-router reuses this component instance whenever the parent's :campaignId
// changes without leaving the workspace — a plain const captured once at setup would go stale.
const universeId = computed(() => route.params.universeId as string)
const campaignId = computed(() => route.params.campaignId as string)

const { get: getCampaign, rename, archive, listGamemasters, addGamemaster, removeGamemaster } = useCampaigns()
const { get: getRuleset } = useRulesets()
const { users, list: listUsers } = useUsers()
const { characters, list: listCharacters } = useCharacters()
const { entities, search: searchEntities } = useEntities()
const { objects, search: searchObjects } = useObjects()
const bumpSidebarRefresh = inject<() => void>('bumpSidebarRefresh')

const campaign = ref<CampaignSummary | null>(null)
const rulesetName = ref('')
const gamemasterIds = ref<string[]>([])
const error = ref<string | null>(null)
const loading = ref(true)
const showArchiveConfirm = ref(false)

const activeTab = ref('Manage')
const tabs = ['Manage', 'Configuration']

const gamemasters = computed(() =>
  gamemasterIds.value.map((id) => ({ id, name: users.value.find((u) => u.id === id)?.name ?? id })),
)

async function load() {
  loading.value = true
  campaign.value = await getCampaign(campaignId.value)
  await listUsers()
  const [ruleset, ids] = await Promise.all([
    campaign.value ? getRuleset(campaign.value.rulesetId) : Promise.resolve(null),
    listGamemasters(campaignId.value),
  ])
  rulesetName.value = ruleset?.name ?? '(unknown)'
  gamemasterIds.value = ids

  // Entities/Objects belong to the Universe, not the Campaign (docs/PLAN.md §2) — this count
  // is Universe-wide, matching exactly what the sidebar's own Entities/Objects panels show
  // for this Universe, not a Campaign-scoped subset that doesn't exist in the domain model.
  await Promise.all([
    listCharacters(campaignId.value),
    searchEntities(universeId.value, ''),
    searchObjects(universeId.value, ''),
  ])
  loading.value = false
}
onMounted(load)
watch([universeId, campaignId], load)

async function onSubmitRename(newName: string) {
  if (!campaign.value) return
  error.value = null
  try {
    await rename(campaign.value.id, newName)
    campaign.value.name = newName
    bumpSidebarRefresh?.()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to rename.'
  }
}

async function onAddGamemaster(userId: string) {
  error.value = null
  try {
    await addGamemaster(campaignId.value, userId)
    gamemasterIds.value = await listGamemasters(campaignId.value)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to add Gamemaster.'
  }
}

async function onRemoveGamemaster(userId: string) {
  error.value = null
  try {
    await removeGamemaster(campaignId.value, userId)
    gamemasterIds.value = await listGamemasters(campaignId.value)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to remove Gamemaster.'
  }
}

async function confirmArchive() {
  showArchiveConfirm.value = false
  error.value = null
  try {
    await archive(campaignId.value)
    bumpSidebarRefresh?.()
    router.push({ name: 'campaign-picker', params: { universeId: universeId.value } })
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  }
}
</script>

<template>
  <div v-if="loading" class="p-6 text-sm text-slate-500">Loading…</div>
  <div v-else-if="campaign" class="mx-auto max-w-2xl p-6">
    <ErrorBanner :message="error" @dismiss="error = null" />
    <BaseTabs :tabs="tabs" v-model="activeTab" class="mb-4" />

    <ManageCampaignPanel
      v-if="activeTab === 'Manage'"
      :name="campaign.name"
      :ruleset-name="rulesetName"
      :gamemasters="gamemasters"
      :character-count="characters.length"
      :entity-count="entities.length"
      :object-count="objects.length"
      @submit-rename="onSubmitRename"
      @add-gamemaster="onAddGamemaster"
      @remove-gamemaster="onRemoveGamemaster"
      @archive="showArchiveConfirm = true"
    />
    <ConfigurationPanel v-else-if="activeTab === 'Configuration'" :configuration="campaign.configuration" />

    <ConfirmDialog
      v-if="showArchiveConfirm"
      title="Archive Campaign"
      message="This campaign will be hidden from pickers. This cannot be undone through this app."
      confirm-label="Archive"
      @confirm="confirmArchive"
      @cancel="showArchiveConfirm = false"
    />
  </div>
  <div v-else class="p-6 text-sm text-slate-500">Campaign not found.</div>
</template>
```

- [ ] **Step 2: Delete the modal**

```bash
rm web/src/components/modals/ManageCampaignModal.vue
```

- [ ] **Step 3: Update `WorkspaceView.vue`**

Change:

```ts
import { computed, onMounted, provide, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUniverses, type UniverseSummary } from '@/composables/useUniverses'
import { useCampaigns, type CampaignSummary } from '@/composables/useCampaigns'
import AppHeader from '@/components/layout/AppHeader.vue'
import AppSidebar from '@/components/layout/AppSidebar.vue'
import ManageUniverseModal from '@/components/modals/ManageUniverseModal.vue'
import ManageCampaignModal from '@/components/modals/ManageCampaignModal.vue'
import CharactersPanel from '@/components/layout/CharactersPanel.vue'
import EntitiesPanel from '@/components/layout/EntitiesPanel.vue'
import ObjectsPanel from '@/components/layout/ObjectsPanel.vue'
```

to:

```ts
import { computed, onMounted, provide, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUniverses, type UniverseSummary } from '@/composables/useUniverses'
import { useCampaigns, type CampaignSummary } from '@/composables/useCampaigns'
import AppHeader from '@/components/layout/AppHeader.vue'
import AppSidebar from '@/components/layout/AppSidebar.vue'
import ManageUniverseModal from '@/components/modals/ManageUniverseModal.vue'
import CharactersPanel from '@/components/layout/CharactersPanel.vue'
import EntitiesPanel from '@/components/layout/EntitiesPanel.vue'
import ObjectsPanel from '@/components/layout/ObjectsPanel.vue'
```

Change:

```ts
const universe = ref<UniverseSummary | null>(null)
const campaign = ref<CampaignSummary | null>(null)
const showManageUniverse = ref(false)
const showManageCampaign = ref(false)

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

function onUniverseRenamed(newName: string) {
  if (universe.value) universe.value.name = newName
  showManageUniverse.value = false
}
function onUniverseArchived() {
  showManageUniverse.value = false
  router.push({ name: 'universe-picker' })
}
function onCampaignRenamed(newName: string) {
  if (campaign.value) campaign.value.name = newName
  showManageCampaign.value = false
}
function onCampaignArchived() {
  showManageCampaign.value = false
  router.push({ name: 'campaign-picker', params: { universeId: universeId.value } })
}
```

to:

```ts
const universe = ref<UniverseSummary | null>(null)
const campaign = ref<CampaignSummary | null>(null)
const showManageUniverse = ref(false)

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
// Campaign rename/archive now happen inside CampaignOverviewPanel.vue (the workspace route's
// default child), not a modal owned here — it bumps the same shared signal
// CharactersPanel/EntitiesPanel already react to, so re-running load() here keeps the header's
// campaign name in sync the same way.
watch(sidebarRefreshSignal, load)

function onUniverseRenamed(newName: string) {
  if (universe.value) universe.value.name = newName
  showManageUniverse.value = false
}
function onUniverseArchived() {
  showManageUniverse.value = false
  router.push({ name: 'universe-picker' })
}
function goToCampaignOverview() {
  router.push({ name: 'campaign-overview', params: { universeId: universeId.value, campaignId: campaignId.value } })
}
```

Change the template:

```vue
    <AppHeader
      :universe-name="universe?.name ?? null"
      :campaign-name="campaign?.name ?? null"
      @click-universe-badge="showManageUniverse = true"
      @click-campaign-badge="showManageCampaign = true"
    />
```

to:

```vue
    <AppHeader
      :universe-name="universe?.name ?? null"
      :campaign-name="campaign?.name ?? null"
      @click-universe-badge="showManageUniverse = true"
      @click-campaign-badge="goToCampaignOverview"
    />
```

And remove the `ManageCampaignModal` block entirely — change:

```vue
    <ManageUniverseModal
      v-if="showManageUniverse && universe"
      :universe-id="universeId"
      :universe-name="universe.name"
      @close="showManageUniverse = false"
      @renamed="onUniverseRenamed"
      @archived="onUniverseArchived"
    />
    <ManageCampaignModal
      v-if="showManageCampaign && campaign"
      :campaign-id="campaignId"
      :campaign-name="campaign.name"
      @close="showManageCampaign = false"
      @renamed="onCampaignRenamed"
      @archived="onCampaignArchived"
    />
  </div>
</template>
```

to:

```vue
    <ManageUniverseModal
      v-if="showManageUniverse && universe"
      :universe-id="universeId"
      :universe-name="universe.name"
      @close="showManageUniverse = false"
      @renamed="onUniverseRenamed"
      @archived="onUniverseArchived"
    />
  </div>
</template>
```

- [ ] **Step 4: Typecheck, build, and re-run the existing e2e suite**

Run (from `web/`): `npm run typecheck && npm run build && npm run test:e2e`
Expected: all clean; both existing tests (`character-creation.spec.ts`,
`character-creation-lag.spec.ts`) must still pass unmodified — they navigate through the
`campaign-overview` route as part of their setup, so this rewrite must not break page load for
them, even though neither test asserts on this panel's own content.

- [ ] **Step 5: Commit**

```bash
git add web/src/views/CampaignOverviewPanel.vue web/src/views/WorkspaceView.vue
git rm web/src/components/modals/ManageCampaignModal.vue
git commit -m "web: replace CampaignOverviewPanel/ManageCampaignModal with a tabbed Manage/Configuration panel"
```

---

### Task 3: Mock support and regression tests

**Files:**
- Modify: `web/e2e/support/mockBackend.ts`
- Create: `web/e2e/campaign-manage.spec.ts`

**Interfaces:**
- Consumes: Task 2's actual app behavior (exercised through the browser, not imported).
- Produces: `MockCampaign.configuration?: string` (optional — existing seeds are unaffected); a
  new `PATCH /api/command/campaigns/:campaignId` mock route (rename).

- [ ] **Step 1: Make `configuration` optional on the mock's Campaign type**

In `web/e2e/support/mockBackend.ts`, change:

```ts
export interface MockCampaign {
  id: string
  universeId: string
  name: string
  rulesetId: string
  isArchived: boolean
}
```

to:

```ts
export interface MockCampaign {
  id: string
  universeId: string
  name: string
  rulesetId: string
  isArchived: boolean
  // Optional so existing seeds (character-creation.spec.ts, character-creation-lag.spec.ts)
  // don't need to change — ConfigurationPanel.vue already renders "No configuration set yet"
  // when this is absent.
  configuration?: string
}
```

- [ ] **Step 2: Add the rename command route**

In `web/e2e/support/mockBackend.ts`, inside the `// ---- command API ----` section, add a new
route immediately before the existing create-Character command handler:

```ts
    if (method === 'PATCH' && (m = matchPath('/api/command/campaigns/:campaignId', p))) {
      const body = JSON.parse(req.postData() || '{}') as { name: string }
      const campaign = state.campaigns.find((c) => c.id === m!.params.campaignId)
      if (campaign) campaign.name = body.name
      return route.fulfill({ status: 204, body: '' })
    }
```

- [ ] **Step 3: Write the tests**

Create `web/e2e/campaign-manage.spec.ts`:

```ts
import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(overrides: Partial<MockState> = {}): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [
      {
        id: 'c1',
        universeId: 'u1',
        name: 'Test Campaign',
        rulesetId: 'r1',
        isArchived: false,
        configuration: JSON.stringify({ difficulty: 'hard', maxPlayers: 5 }),
      },
    ],
    rulesets: [{ id: 'r1', name: 'Test Ruleset' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    gamemasterIds: ['user-1'],
    characters: [
      { id: 'ch1', name: 'Aragorn', campaignId: 'c1', entityId: 'e1', playerUserId: 'user-1', isArchived: false },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('the Manage tab shows the folded-in overview content, and Configuration shows pretty-printed JSON', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')

  await expect(page.getByRole('heading', { name: 'Test Campaign' })).toBeVisible()
  await expect(page.getByText('Ruleset: Test Ruleset')).toBeVisible()
  await expect(page.getByText('devuser@timadorus.local')).toBeVisible()

  // The Characters/Entities/Objects counts (folded in from the old CampaignOverviewPanel) render
  // as a labeled grid — checking the labels is enough to prove the fold-in worked, without a
  // fragile exact-number-anywhere-on-the-page assertion.
  const countsGrid = page.locator('div.grid.grid-cols-3')
  await expect(countsGrid).toContainText('Characters')
  await expect(countsGrid).toContainText('Entities')
  await expect(countsGrid).toContainText('Objects')

  // BaseTabs.vue's tab buttons carry an explicit role="tab" (added in an earlier accessibility
  // fix), which overrides the implicit "button" role — getByRole('button', ...) would not match
  // them.
  await page.getByRole('tab', { name: 'Configuration', exact: true }).click()
  const configPanel = page.locator('div.rounded-md').filter({ hasText: 'Configuration' })
  await expect(configPanel.locator('pre')).toContainText('"difficulty": "hard"')
  await expect(configPanel.locator('pre')).toContainText('"maxPlayers": 5')
})

test('the campaign badge navigates to the Manage tab, and renaming there updates the header', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  // Start on a different route within the workspace, where the header still shows the campaign
  // badge.
  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await expect(page.getByRole('button', { name: '🎲 Test Campaign' })).toBeVisible()

  await page.getByRole('button', { name: '🎲 Test Campaign' }).click()
  await expect(page).toHaveURL(/\/universes\/u1\/campaigns\/c1$/)
  await expect(page.getByRole('heading', { name: 'Test Campaign' })).toBeVisible()

  await page.getByRole('button', { name: 'Rename', exact: true }).click()
  const nameInput = page.locator('input[type="text"]').first()
  await nameInput.fill('Renamed Campaign')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Renamed Campaign' })).toBeVisible()

  // The header badge (driven by WorkspaceView's own campaign fetch) picks up the rename too.
  await expect(page.getByRole('button', { name: '🎲 Renamed Campaign' })).toBeVisible()
})
```

- [ ] **Step 4: Run the full suite**

Run (from `web/`): `npm run test:e2e`
Expected: 5 passed (the 3 existing tests plus these 2 new ones). Run it twice to confirm no
flakes.

- [ ] **Step 5: Typecheck and build**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add web/e2e/support/mockBackend.ts web/e2e/campaign-manage.spec.ts
git commit -m "web/e2e: test the Campaign tabbed panel (folded-in overview, Configuration JSON, badge navigation, rename freshness)"
```

---

## Final Verification

- `npm run typecheck && npm run build && npm run test:e2e` (from `web/`) all clean — 5 tests
  passing, no flakes across at least two full runs.
- `go build ./... && go vet ./... && go test ./...` unaffected (this plan touches no Go code) —
  re-confirm anyway.
- Grep the whole `web/src` tree for `ManageCampaignModal` to confirm no dangling reference
  (import or otherwise) survives its deletion.
