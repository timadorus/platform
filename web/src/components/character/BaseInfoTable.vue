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
  if (!nameDraft.value.trim()) return
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
