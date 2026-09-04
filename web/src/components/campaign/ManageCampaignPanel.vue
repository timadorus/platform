<script setup lang="ts">
import { ref, watch } from 'vue'
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
  if (!nameDraft.value.trim()) return
  emit('submit-rename', nameDraft.value.trim())
  // editingName is deliberately NOT closed here — see the watch below. Closing immediately
  // (the old behavior) collapsed the editor before the parent's PATCH resolved, so a rejected
  // rename discarded the typed draft with no way to recover it without retyping from scratch.
}

// Close the editor only once the parent's own `name` actually changes to match what was
// submitted — i.e. only on a successful rename. A rejected rename never touches `name`, so the
// editor (and the user's typed draft) stays exactly as they left it, ready to retry immediately
// alongside the error banner the parent already shows.
watch(
  () => props.name,
  (newName) => {
    if (editingName.value && newName === nameDraft.value) {
      editingName.value = false
    }
  },
)

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
