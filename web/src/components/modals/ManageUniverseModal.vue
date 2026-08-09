<script setup lang="ts">
import { onMounted, ref } from 'vue'
import BaseModal from '@/components/common/BaseModal.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import UserPicker from '@/components/pickers/UserPicker.vue'
import { useUniverses } from '@/composables/useUniverses'
import { useUsers } from '@/composables/useUsers'

const props = defineProps<{ universeId: string; universeName: string }>()
const emit = defineEmits<{ close: []; renamed: [name: string]; archived: [] }>()

const { rename, archive, listCreators, addCreator, removeCreator } = useUniverses()
const { users, list: listUsers } = useUsers()

const name = ref(props.universeName)
const creatorIds = ref<string[]>([])
const error = ref<string | null>(null)
const showAddCreator = ref(false)
const showArchiveConfirm = ref(false)

async function load() {
  await listUsers()
  creatorIds.value = await listCreators(props.universeId)
}
onMounted(load)

async function submitRename() {
  error.value = null
  try {
    await rename(props.universeId, name.value.trim())
    emit('renamed', name.value.trim())
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to rename.'
  }
}

async function onAddCreator(userId: string) {
  showAddCreator.value = false
  error.value = null
  try {
    await addCreator(props.universeId, userId)
    creatorIds.value = await listCreators(props.universeId)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to add Creator.'
  }
}

async function onRemoveCreator(userId: string) {
  error.value = null
  try {
    await removeCreator(props.universeId, userId)
    creatorIds.value = await listCreators(props.universeId)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to remove Creator.'
  }
}

async function confirmArchive() {
  showArchiveConfirm.value = false
  error.value = null
  try {
    await archive(props.universeId)
    emit('archived')
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  }
}
</script>

<template>
  <BaseModal title="Manage Universe" @close="emit('close')">
    <ErrorBanner :message="error" @dismiss="error = null" />

    <div class="mb-4 flex gap-2">
      <input v-model="name" type="text" class="flex-1 rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
      <BaseButton @click="submitRename">Rename</BaseButton>
    </div>

    <div class="mb-4">
      <div class="mb-1 flex items-center justify-between">
        <label class="text-xs font-medium text-slate-600">Creators</label>
        <button class="text-xs text-indigo-600 hover:underline" @click="showAddCreator = !showAddCreator">+ Add</button>
      </div>
      <UserPicker v-if="showAddCreator" :exclude-ids="creatorIds" @select="onAddCreator" />
      <ul class="mt-2 space-y-1">
        <li v-for="id in creatorIds" :key="id" class="flex items-center justify-between text-sm">
          <span>{{ users.find((u) => u.id === id)?.name ?? id }}</span>
          <button class="text-xs text-red-500 hover:underline" @click="onRemoveCreator(id)">Remove</button>
        </li>
      </ul>
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
  </BaseModal>
</template>
