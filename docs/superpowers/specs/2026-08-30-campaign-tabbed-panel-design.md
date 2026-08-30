# Campaign Detail: Tabbed Manage/Configuration Panel — Design

## Context

Today, selecting a Campaign navigates to the `campaign-overview` route, whose default child
(`CampaignOverviewPanel.vue`) shows a read-only summary (Gamemasters list, Ruleset name,
Characters/Entities/Objects counts). Renaming, adding/removing Gamemasters, and archiving the
Campaign instead happen in a separate `ManageCampaignModal.vue`, opened by clicking the campaign
badge in `AppHeader`. This spec merges both into one tabbed main-area panel, mirroring the
Character detail page's own tabbed redesign.

## Decisions

- **Two tabs: Manage (default), Configuration.** Manage folds in everything the current
  overview panel and the modal show today: rename, a `Ruleset:` label (new), the interactive
  Gamemasters list, the Characters/Entities/Objects counts, and Archive. Configuration shows the
  Campaign's `configuration` field (a JSON string) pretty-printed.
- **`ManageCampaignModal.vue` is deleted.** Its logic moves into a new presentational component,
  `ManageCampaignPanel.vue`, hosted by a rewritten `CampaignOverviewPanel.vue` — the same
  division of labor already established by `CharacterDetailView.vue`/`BaseInfoTable.vue`: the
  routed view owns data-loading and the real API calls; the presentational child emits events.
- **The campaign badge in `AppHeader` is unchanged** (still emits `click-campaign-badge`) — only
  `WorkspaceView.vue`'s handler changes, from opening the modal to navigating to
  `campaign-overview`.
- **Rename in the Manage tab uses click-to-reveal inline editing**, matching the convention
  already established by `UsersAdminView.vue` and `BaseInfoTable.vue` — not the old modal's
  always-editable input. This is a small, deliberate consistency improvement over what
  `ManageCampaignModal` did, not a new open question.
- **Keeping the header's Campaign name fresh after a rename**: since rename no longer bubbles an
  event up to `WorkspaceView` (there's no modal in between anymore), `CampaignOverviewPanel`
  instead calls the already-injected `bumpSidebarRefresh()` after a successful rename, and
  `WorkspaceView` adds `watch(sidebarRefreshSignal, load)` — reusing existing shared
  infrastructure rather than adding new plumbing.
- **`useCampaigns.ts`'s `CampaignSummary` gains a `configuration: string` field** — the backend
  query schema already returns it (added by the Campaign-configuration branch); the frontend type
  just hadn't caught up.
- **No retry/timeout polling is added to `CampaignOverviewPanel`'s own `load()`.** This view has
  the same theoretical read-model-lag exposure the Character-creation branch fixed, but it wasn't
  reported broken here, and that branch's own spec explicitly deferred generalizing the fix to
  Campaign/Universe/Object creation as follow-up work — not silently bundled into this unrelated
  UI change.
- **`MockCampaign.configuration` is optional** in the Playwright harness, defaulting to absent
  (which the Configuration tab already renders gracefully as "no configuration set") — so neither
  existing test needs to change.

## Component Structure

```
web/src/components/campaign/ManageCampaignPanel.vue    (new)
web/src/components/campaign/ConfigurationPanel.vue      (new)
web/src/views/CampaignOverviewPanel.vue                 (rewritten)
web/src/components/modals/ManageCampaignModal.vue       (deleted)
web/src/views/WorkspaceView.vue                          (modified)
web/src/composables/useCampaigns.ts                      (modified)
web/e2e/support/mockBackend.ts                           (modified)
web/e2e/campaign-manage.spec.ts                          (new)
```

### `ManageCampaignPanel.vue`

Presentational — props in, events out, exactly like `BaseInfoTable.vue`:

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

### `ConfigurationPanel.vue`

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

### `CampaignOverviewPanel.vue` (rewritten)

Keeps the existing data it already loads (campaign, ruleset name, gamemaster ids, character/
entity/object counts — via `docs/PLAN.md §2`'s Entity/Object-belongs-to-Universe rule, unchanged),
adds `BaseTabs` + the two new panels, and takes over `rename`/`archive`/`addGamemaster`/
`removeGamemaster` from the deleted modal:

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

### `WorkspaceView.vue`

Removes `ManageCampaignModal` entirely (import, `showManageCampaign`, `onCampaignRenamed`,
`onCampaignArchived`, the template block); the campaign badge now navigates:

```ts
function goToCampaignOverview() {
  router.push({ name: 'campaign-overview', params: { universeId: universeId.value, campaignId: campaignId.value } })
}
```

bound as `@click-campaign-badge="goToCampaignOverview"`. Adds `watch(sidebarRefreshSignal, load)`
alongside the existing `onMounted(load)`/`watch([universeId, campaignId], load)`, so a rename or
archive happening in `CampaignOverviewPanel` (which bumps the same shared signal
`CharactersPanel`/`EntitiesPanel` already react to) also refreshes the header's own data.

### `useCampaigns.ts`

`CampaignSummary` gains one field:

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

## Testing

Extend the Playwright harness: `MockCampaign.configuration?: string` (optional, so neither
existing test needs to change), plus one new test file exercising the core new behaviors —
detailed concretely in the implementation plan, covering at minimum: the Manage tab renders as
default with the folded-in content (name, `Ruleset:`, Gamemasters, counts), switching to
Configuration shows pretty-printed JSON for a seeded campaign, and clicking the campaign badge
from a different route (e.g. a Character's detail page) navigates to `campaign-overview` rather
than opening a modal.

## Explicitly Out of Scope

- Editing the Configuration field from this UI (`PUT /campaigns/{id}/configure` already exists on
  the backend; this spec only displays the field).
- Retry/timeout polling for `CampaignOverviewPanel`'s own data loading (see Decisions).
- Any change to Universe management (`ManageUniverseModal` stays exactly as it is).
