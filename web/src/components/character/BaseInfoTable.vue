<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import BaseButton from '@/components/common/BaseButton.vue'
import UserPicker from '@/components/pickers/UserPicker.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import { useCharacters } from '@/composables/useCharacters'

const props = defineProps<{
  characterId: string
  name: string
  playerName: string
  traits: string[]
  traitPoints: number
  availableTraits: string[]
}>()
const emit = defineEmits<{
  'submit-rename': [name: string]
  'submit-reassign-player': [userId: string]
  archive: []
}>()
const { requestAction } = useCharacters()

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

const editingPlayer = ref(false)
function onSelectPlayer(userId: string) {
  emit('submit-reassign-player', userId)
  editingPlayer.value = false
}

const showAddTrait = ref(false)
const traitToAdd = ref('')
type AddTraitStatus = 'idle' | 'pending' | 'error'
const addTraitStatus = ref<AddTraitStatus>('idle')
const addTraitError = ref<string | null>(null)
// The trait most recently submitted, so the watch below can tell "the loaded traits now include
// my own request" apart from "someone else changed something unrelated".
let pendingTrait: string | null = null

// A rejected addTrait (not a Campaign trait / no points left / already has it) is a clean, logged
// engine-side no-op — no ActionChanged-equivalent event distinguishes "rejected" from "still
// processing," so this timeout is what guarantees the control always resolves, exactly like
// ConfigurationPanel.vue's identical PENDING_TIMEOUT_MS for Max Stat Budget.
const ADD_TRAIT_TIMEOUT_MS = 10000
let addTraitTimeoutHandle: ReturnType<typeof setTimeout> | null = null
function clearAddTraitTimeout() {
  if (addTraitTimeoutHandle) {
    clearTimeout(addTraitTimeoutHandle)
    addTraitTimeoutHandle = null
  }
}

async function submitAddTrait() {
  if (!traitToAdd.value) return
  const trait = traitToAdd.value
  addTraitStatus.value = 'pending'
  addTraitError.value = null
  pendingTrait = trait
  showAddTrait.value = false
  traitToAdd.value = ''
  clearAddTraitTimeout()
  addTraitTimeoutHandle = setTimeout(() => {
    if (addTraitStatus.value === 'pending') {
      addTraitStatus.value = 'error'
      addTraitError.value = 'No confirmation received — this trait may not have been added.'
      pendingTrait = null
    }
  }, ADD_TRAIT_TIMEOUT_MS)
  try {
    await requestAction(props.characterId, { action: 'addTrait', trait })
  } catch (err) {
    clearAddTraitTimeout()
    addTraitStatus.value = 'error'
    addTraitError.value = err instanceof Error ? err.message : 'Failed to request adding this trait.'
  }
}

watch(
  () => props.traits,
  (traits) => {
    if (addTraitStatus.value === 'pending' && pendingTrait !== null && traits.includes(pendingTrait)) {
      clearAddTraitTimeout()
      addTraitStatus.value = 'idle'
      pendingTrait = null
    }
  },
)

onUnmounted(clearAddTraitTimeout)
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4" data-testid="base-info-card">
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
          <td class="py-1.5 pr-3">{{ playerName }}</td>
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
          <td class="py-1.5" colspan="2">
            <div class="flex flex-wrap items-center gap-2">
              <span>{{ traits.length > 0 ? traits.join(', ') : '(no traits selected)' }}</span>
              <template v-if="traitPoints > 0">
                <button v-if="!showAddTrait" class="text-xs text-indigo-600 hover:underline" @click="showAddTrait = true">
                  Add Trait
                </button>
                <template v-else>
                  <select v-model="traitToAdd" class="rounded-md border border-slate-300 px-2 py-1 text-xs">
                    <option value="" disabled>Select a trait…</option>
                    <option v-for="t in availableTraits" :key="t" :value="t">{{ t }}</option>
                  </select>
                  <button class="text-xs text-indigo-600 hover:underline" :disabled="!traitToAdd" @click="submitAddTrait">Add</button>
                  <button class="text-xs text-slate-400 hover:underline" @click="showAddTrait = false">Cancel</button>
                </template>
              </template>
              <span v-if="addTraitStatus === 'pending'" class="text-xs text-slate-400">Update requested — refreshing…</span>
            </div>
            <ErrorBanner v-if="addTraitStatus === 'error'" :message="addTraitError" @dismiss="addTraitStatus = 'idle'" />
          </td>
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
