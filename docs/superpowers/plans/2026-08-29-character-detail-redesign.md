# Character Detail Page Redesign Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Redesign `web/src/views/CharacterDetailView.vue` into a tabbed page (Stats/Skills/
Equipment/Journal, Stats default), with Stats showing side-by-side Attributes and Base Info cards,
per `docs/superpowers/specs/2026-08-29-character-detail-redesign-design.md`.

**Architecture:** Three new presentational Vue components (a generic `BaseTabs`, and two
Character-specific tables) plus a rewrite of the existing view to compose them. Frontend-only —
no API/domain/backend changes anywhere in this plan.

**Tech Stack:** Vue 3 (`<script setup>`, TypeScript), Tailwind CSS v4.

## Global Constraints

- No backend/API/domain changes anywhere in this plan — `internal/`, `api/`, and the generated
  `web/src/api/*.types.ts` clients are untouched. `CharacterSummary` (in
  `web/src/composables/useCharacters.ts`) is not modified.
- Attributes and Traits are hardcoded placeholder data, each with a code comment explaining why
  (no backend field exists yet) — not props, not composable state.
- Skills/Equipment/Journal tabs render static "coming soon" text only — no data model.
- Rename and Reassign Player use click-to-reveal inline editing (text/picker shown only after
  clicking the action button, collapsing back to display mode on save/cancel) — not an
  always-editable input.
- The Archive row in the Base Info table has no label in its first column — just the "Archive
  Character" button, right-aligned, as the table's last row.
- No URL/query-param persistence of the active tab — a plain local `ref`, defaulting to `'Stats'`.
- `npm run typecheck` (`vue-tsc --noEmit`) and `npm run build` must stay clean after every task
  (run from the `web/` directory).
- File layout after this plan:
  - `web/src/components/common/BaseTabs.vue` (new)
  - `web/src/components/character/AttributesTable.vue` (new)
  - `web/src/components/character/BaseInfoTable.vue` (new)
  - `web/src/views/CharacterDetailView.vue` (rewritten)

---

### Task 1: New presentational components (BaseTabs, AttributesTable, BaseInfoTable)

**Files:**
- Create: `web/src/components/common/BaseTabs.vue`
- Create: `web/src/components/character/AttributesTable.vue`
- Create: `web/src/components/character/BaseInfoTable.vue`

**Interfaces:**
- Produces: `BaseTabs` — props `{ tabs: string[]; modelValue: string }`, emits `update:modelValue`
  (standard `v-model` component). `AttributesTable` — no props, no emits. `BaseInfoTable` — props
  `{ name: string; playerName: string }`, emits `submit-rename: [name: string]`,
  `submit-reassign-player: [userId: string]`, `archive: []`.
- Consumed by: Task 2's rewritten `CharacterDetailView.vue`.

These three files have no dependency on each other or on Task 2 — they're independently
compilable leaf components.

- [ ] **Step 1: Create the tab bar component**

Create `web/src/components/common/BaseTabs.vue`:

```vue
<script setup lang="ts">
defineProps<{ tabs: string[]; modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [tab: string] }>()
</script>

<template>
  <div class="border-b border-slate-200">
    <nav class="-mb-px flex gap-4">
      <button
        v-for="tab in tabs"
        :key="tab"
        class="border-b-2 px-1 py-2 text-sm font-medium"
        :class="
          tab === modelValue
            ? 'border-indigo-600 text-indigo-600'
            : 'border-transparent text-slate-500 hover:border-slate-300 hover:text-slate-700'
        "
        @click="emit('update:modelValue', tab)"
      >
        {{ tab }}
      </button>
    </nav>
  </div>
</template>
```

- [ ] **Step 2: Create the Attributes table**

Create `web/src/components/character/AttributesTable.vue`:

```vue
<script setup lang="ts">
// Attribute values/bonuses are placeholders: no backend field exists yet for Character
// attributes. Every attribute reads 75/+0 until a real data source is wired up.
const attributes = [
  { name: 'Strength', abbr: 'ST' },
  { name: 'Agility', abbr: 'AG' },
  { name: 'Constitution', abbr: 'CO' },
  { name: 'Quickness', abbr: 'QU' },
  { name: 'Self Discipline', abbr: 'SD' },
  { name: 'Memory', abbr: 'ME' },
  { name: 'Reasoning', abbr: 'RE' },
  { name: 'Empathy', abbr: 'EM' },
  { name: 'Presence', abbr: 'PR' },
  { name: 'Intuition', abbr: 'IN' },
].map((a) => ({ ...a, value: 75, bonus: '+0' }))
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4">
    <h2 class="mb-3 text-sm font-semibold text-slate-900">Attributes</h2>
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b border-slate-100 text-left text-xs font-medium text-slate-500">
          <th class="pb-1.5">Attribute</th>
          <th class="pb-1.5">Abbr</th>
          <th class="pb-1.5">Value</th>
          <th class="pb-1.5">Bonus</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="a in attributes" :key="a.abbr" class="border-b border-slate-50 last:border-0">
          <td class="py-1.5 text-slate-900">{{ a.name }}</td>
          <td class="py-1.5 text-slate-500">{{ a.abbr }}</td>
          <td class="py-1.5 text-slate-900">{{ a.value }}</td>
          <td class="py-1.5 text-slate-900">{{ a.bonus }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
```

- [ ] **Step 3: Create the Base Info table**

Create `web/src/components/character/BaseInfoTable.vue`:

```vue
<script setup lang="ts">
import { ref } from 'vue'
import BaseButton from '@/components/common/BaseButton.vue'
import UserPicker from '@/components/pickers/UserPicker.vue'

const props = defineProps<{ name: string; playerName: string }>()
const emit = defineEmits<{
  'submit-rename': [name: string]
  'submit-reassign-player': [userId: string]
  archive: []
}>()

// Placeholder — no backend field exists yet for Traits.
const traits = 'Brave, Cunning, Loyal'

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

const editingPlayer = ref(false)
function onSelectPlayer(userId: string) {
  emit('submit-reassign-player', userId)
  editingPlayer.value = false
}
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4">
    <h2 class="mb-3 text-sm font-semibold text-slate-900">Base Info</h2>
    <table class="w-full text-sm">
      <tbody>
        <tr class="border-b border-slate-50">
          <td class="py-1.5 pr-3 font-medium text-slate-500">Character Name</td>
          <td class="py-1.5 pr-3">
            <template v-if="editingName">
              <input v-model="nameDraft" type="text" class="w-full rounded-md border border-slate-300 px-2 py-1 text-sm" />
            </template>
            <template v-else>{{ name }}</template>
          </td>
          <td class="py-1.5 text-right">
            <template v-if="editingName">
              <button class="text-xs text-indigo-600 hover:underline" @click="saveName">Save</button>
              <button class="ml-3 text-xs text-slate-400 hover:underline" @click="editingName = false">Cancel</button>
            </template>
            <button v-else class="text-xs text-indigo-600 hover:underline" @click="startEditName">Rename</button>
          </td>
        </tr>
        <tr class="border-b border-slate-50">
          <td class="py-1.5 pr-3 font-medium text-slate-500">Player</td>
          <td class="py-1.5 pr-3">
            <template v-if="!editingPlayer">{{ playerName }}</template>
          </td>
          <td class="py-1.5 text-right align-top">
            <button v-if="!editingPlayer" class="text-xs text-indigo-600 hover:underline" @click="editingPlayer = true">
              Reassign Player
            </button>
            <button v-else class="text-xs text-slate-400 hover:underline" @click="editingPlayer = false">Cancel</button>
          </td>
        </tr>
        <tr v-if="editingPlayer" class="border-b border-slate-50">
          <td></td>
          <td colspan="2" class="py-1.5"><UserPicker @select="onSelectPlayer" /></td>
        </tr>
        <tr class="border-b border-slate-50">
          <td class="py-1.5 pr-3 font-medium text-slate-500">Traits</td>
          <td class="py-1.5" colspan="2">{{ traits }}</td>
        </tr>
        <tr>
          <td></td>
          <td></td>
          <td class="py-1.5 text-right">
            <BaseButton variant="danger" @click="emit('archive')">Archive Character</BaseButton>
          </td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
```

- [ ] **Step 4: Typecheck and build**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean. (These three files aren't imported anywhere yet, so this only proves they're each
independently well-typed — Task 2 proves they compose correctly.)

- [ ] **Step 5: Commit**

```bash
git add web/src/components/common/BaseTabs.vue web/src/components/character/AttributesTable.vue web/src/components/character/BaseInfoTable.vue
git commit -m "web: add BaseTabs, AttributesTable, BaseInfoTable components"
```

---

### Task 2: Rewrite CharacterDetailView.vue to use the new components

**Files:**
- Modify: `web/src/views/CharacterDetailView.vue`

**Interfaces:**
- Consumes: `BaseTabs`, `AttributesTable`, `BaseInfoTable` (Task 1) — exact props/emits as given
  above.

- [ ] **Step 1: Replace the file**

Replace the full content of `web/src/views/CharacterDetailView.vue` with:

```vue
<script setup lang="ts">
import { computed, inject, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useCharacters, type CharacterSummary } from '@/composables/useCharacters'
import { useUsers } from '@/composables/useUsers'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import BaseTabs from '@/components/common/BaseTabs.vue'
import AttributesTable from '@/components/character/AttributesTable.vue'
import BaseInfoTable from '@/components/character/BaseInfoTable.vue'

const route = useRoute()
const router = useRouter()
// computed, not a plain const: vue-router reuses this component instance across param-only
// route changes on the same route record (e.g. clicking a different character card in the
// sidebar while already viewing one) — a plain const captured once at setup would go stale.
const characterId = computed(() => route.params.characterId as string)

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

async function onSubmitRename(newName: string) {
  if (!character.value) return
  error.value = null
  try {
    await rename(character.value.id, newName)
    character.value.name = newName
    bumpSidebarRefresh?.()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to rename.'
  }
}

async function confirmArchive() {
  if (!character.value) return
  showArchiveConfirm.value = false
  error.value = null
  try {
    await archive(character.value.id)
    bumpSidebarRefresh?.()
    router.push({ name: 'campaign-overview', params: { universeId: route.params.universeId, campaignId: route.params.campaignId } })
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  }
}

async function onSubmitReassignPlayer(userId: string) {
  if (!character.value) return
  error.value = null
  try {
    await setPlayer(character.value.id, userId)
    character.value.playerUserId = userId
    bumpSidebarRefresh?.()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to reassign Player.'
  }
}
</script>

<template>
  <div v-if="character" class="mx-auto max-w-4xl p-6">
    <ErrorBanner :message="error" @dismiss="error = null" />
    <BaseTabs :tabs="tabs" v-model="activeTab" class="mb-4" />

    <div v-if="activeTab === 'Stats'" class="flex gap-4">
      <AttributesTable class="flex-1" />
      <BaseInfoTable
        class="flex-1"
        :name="character.name"
        :player-name="playerName"
        @submit-rename="onSubmitRename"
        @submit-reassign-player="onSubmitReassignPlayer"
        @archive="showArchiveConfirm = true"
      />
    </div>
    <div v-else-if="activeTab === 'Skills'" class="text-sm text-slate-500">Skills coming soon.</div>
    <div v-else-if="activeTab === 'Equipment'" class="text-sm text-slate-500">Equipment coming soon.</div>
    <div v-else-if="activeTab === 'Journal'" class="text-sm text-slate-500">Journal coming soon.</div>

    <ConfirmDialog
      v-if="showArchiveConfirm"
      title="Archive Character"
      message="This character will be hidden from the sidebar. This cannot be undone through this app."
      confirm-label="Archive"
      @confirm="confirmArchive"
      @cancel="showArchiveConfirm = false"
    />
  </div>
  <div v-else class="p-6 text-sm text-slate-500">Loading…</div>
</template>
```

This is a full-file replacement: the old always-editable `name` ref/input and the old
`showReassignPlayer`-gated inline `UserPicker` are gone (that UI now lives inside
`BaseInfoTable`), and the old page-level Archive `BaseButton` is gone (replaced by
`BaseInfoTable`'s `@archive` emit, still driving the same `ConfirmDialog`/`confirmArchive` flow
unchanged).

- [ ] **Step 2: Typecheck and build**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean — no unused-import errors (the old `BaseButton`/`UserPicker` imports are removed
from this file since `BaseInfoTable` now owns them), no missing-prop/emit-type errors on the three
new components.

- [ ] **Step 3: Commit**

```bash
git add web/src/views/CharacterDetailView.vue
git commit -m "web: redesign Character detail page into tabbed Stats/Skills/Equipment/Journal layout"
```

---

## Final Verification

- `npm run typecheck && npm run build` (from `web/`) clean — already true after Task 2, re-confirm
  once more at the end of the branch.
- **Live UI verification is the controller's responsibility, not a task deliverable.** This
  codebase's web SPA has no automated test suite; its established verification method (used on the
  two most recent web SPA branches) is a live headless-Chromium session against a real running dev
  cluster. A dev cluster was confirmed running and healthy at plan-writing time
  (`kubectl get pods -n timadorus-dev`, all Ready). After the final whole-branch review, or as part
  of it, drive a real browser against that cluster and confirm: the Stats tab is selected by
  default and shows both cards (Attributes at 75/+0 for all ten rows, Base Info with the real
  name/player and the placeholder Traits string); clicking Skills/Equipment/Journal shows the
  placeholder text and switches away from Stats's content, and clicking back to Stats re-renders it
  correctly; Rename (edit, Save, and separately edit-then-Cancel) and Reassign Player both work and
  update the displayed value; clicking Archive Character shows the `ConfirmDialog` (cancel it —
  do not actually archive a real Character). If browser automation isn't available in whatever
  environment performs this check, say so plainly rather than skipping it silently or asserting
  success without having run it.
