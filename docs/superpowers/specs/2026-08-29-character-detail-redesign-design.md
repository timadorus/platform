# Character Detail Page Redesign — Design

## Context

`web/src/views/CharacterDetailView.vue` currently renders a single narrow column: an always-editable
name field + Rename button, a Player line with a click-to-reveal `UserPicker` for reassignment, and
an Archive button. This spec redesigns it into a tabbed page (Stats / Skills / Equipment / Journal,
"Stats" selected by default), with the Stats tab showing two side-by-side card panels: a table of ten
RPG attributes (frontend-placeholder data — no backend field exists yet) and a "Base Info" table
holding the character's name, player, traits, and the archive action.

This is a frontend-only change. No API, domain, or backend code is touched — `CharacterSummary`
already carries everything the redesigned page needs (`name`, `playerUserId`) except Traits and the
Attributes, both of which are placeholder data per the decisions below.

## Decisions

- **Attributes are 100% placeholder data.** All ten attributes render `value: 75, bonus: '+0'`,
  hardcoded in the component. A code comment marks this as pending a real backend field. No API call,
  no composable.
- **Traits are also 100% placeholder data**, for the same reason (no backend field exists — `Character`
  has no `traits` property). Rendered as a fixed example comma-separated string, read-only, no edit
  action.
- **Skills / Equipment / Journal tabs are empty placeholder panels** ("Coming soon" text) — no data
  model, no composable, just present and clickable so the tab bar is fully functional today.
- **Rename / Reassign Player use click-to-reveal inline editing**, matching
  `UsersAdminView.vue`'s existing pattern (not the current `CharacterDetailView`'s always-editable
  input): the row shows plain text by default; clicking the action button reveals an inline edit
  control (a text input for name, the existing `UserPicker` for player) with Save/Cancel, collapsing
  back to text on either.
- **Archive moves into the Base Info table** as its own row: empty left/middle columns, just the
  "Archive Character" button on the right (per the follow-up correction — no "Archive" label).
- **No URL/query-param persistence of the active tab** — plain local component state, defaulting to
  `'Stats'`.

## Component Structure

```
web/src/components/common/BaseTabs.vue           (new)
web/src/components/character/AttributesTable.vue (new)
web/src/components/character/BaseInfoTable.vue   (new)
web/src/views/CharacterDetailView.vue             (rewritten)
```

`BaseTabs.vue` lives in `common/` since it's a generic, application-agnostic primitive (like
`BaseButton`/`BaseModal`) — nothing about it is Character-specific. `AttributesTable`/`BaseInfoTable`
are Character-specific presentational components, in a new `components/character/` directory
(mirroring the existing `pickers/`/`modals/`/`layout/` organization).

### `BaseTabs.vue`

A `v-model`-driven horizontal tab bar. No API calls, no app-specific knowledge.

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

### `AttributesTable.vue`

Self-contained — no props, no emits. Owns its own static data.

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

### `BaseInfoTable.vue`

Presentational: owns its own "which row is being edited" UI state, emits events for the parent to
actually call the API. Traits stay a hardcoded placeholder string inside this component (same
reasoning as Attributes) since no prop/data source exists for it yet.

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

### `CharacterDetailView.vue`

Keeps all existing composable/data-loading logic (`useCharacters`, `useUsers`,
`bumpSidebarRefresh`, the archive `ConfirmDialog`), but the template changes: the narrow
single-column layout is replaced by a wider container hosting `BaseTabs` and the four tab panels.
`AttributesTable`/`BaseInfoTable` replace the old inline markup on the Stats tab.

```vue
<script setup lang="ts">
// ...existing imports...
import BaseTabs from '@/components/common/BaseTabs.vue'
import AttributesTable from '@/components/character/AttributesTable.vue'
import BaseInfoTable from '@/components/character/BaseInfoTable.vue'

// ...existing refs/computed (character, name, error, showArchiveConfirm, playerName)...
const activeTab = ref('Stats')
const tabs = ['Stats', 'Skills', 'Equipment', 'Journal']

// existing submitRename/confirmArchive/onReassignPlayer stay, adapted to the new
// component's event names (submitRename(name) instead of using the `name` ref directly, etc.)
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
        @submit-reassign-player="onReassignPlayer"
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

`onSubmitRename(name: string)` replaces the old `submitRename()` (which read from a template-bound
`name` ref) — the new version receives the name directly from `BaseInfoTable`'s emit, otherwise
identical logic (call `rename(character.id, name)`, update `character.value.name`, bump sidebar
refresh, catch/report errors into the existing `error` ref).

## Testing

This codebase's web SPA has no unit-test suite (verified: no `*.test.ts`/`*.spec.ts` files, no test
runner configured in `web/package.json`) — its established verification method is live
Playwright-driven checks against a real headless-Chromium session (used in the two most recent web
SPA branches). This redesign will be verified the same way:

- Navigate to a Character's detail page; confirm the Stats tab is selected by default and both cards
  render (Attributes with all ten rows at 75/+0, Base Info with the real name/player and the
  placeholder Traits string).
- Click each of Skills/Equipment/Journal; confirm the placeholder text renders and Stats's content
  disappears; click back to Stats; confirm it re-renders correctly (no stale state).
- Click Rename, edit the name, Save; confirm the displayed name updates and the sidebar (via
  `bumpSidebarRefresh`) reflects it — then Cancel a second edit attempt and confirm no change.
- Click Reassign Player, pick a different user; confirm the displayed player name updates.
- Click Archive Character, confirm the `ConfirmDialog` appears (existing behavior, unchanged code
  path) — do not actually archive a real Character during verification; cancel the dialog instead.

## Explicitly Out of Scope

- Any backend/API change (no new fields, no new endpoints).
- Real data for Attributes, Traits, Skills, Equipment, or Journal.
- Editing Traits.
- URL/query-param persistence of the active tab.
