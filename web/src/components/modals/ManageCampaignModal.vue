<script setup lang="ts">
import { onMounted, ref } from 'vue'
import BaseModal from '@/components/common/BaseModal.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import UserPicker from '@/components/pickers/UserPicker.vue'
import { useCampaigns } from '@/composables/useCampaigns'
import { useUsers } from '@/composables/useUsers'

const props = defineProps<{ campaignId: string; campaignName: string }>()
const emit = defineEmits<{ close: []; renamed: [name: string]; archived: [] }>()

const { rename, archive, listGamemasters, addGamemaster, removeGamemaster } = useCampaigns()
const { users, list: listUsers } = useUsers()

const name = ref(props.campaignName)
const gamemasterIds = ref<string[]>([])
const error = ref<string | null>(null)
const showAddGamemaster = ref(false)
const showArchiveConfirm = ref(false)

async function load() {
  await listUsers()
  gamemasterIds.value = await listGamemasters(props.campaignId)
}
onMounted(load)

async function submitRename() {
  error.value = null
  try {
    await rename(props.campaignId, name.value.trim())
    emit('renamed', name.value.trim())
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to rename.'
  }
}

async function onAddGamemaster(userId: string) {
  showAddGamemaster.value = false
  error.value = null
  try {
    await addGamemaster(props.campaignId, userId)
    gamemasterIds.value = await listGamemasters(props.campaignId)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to add Gamemaster.'
  }
}

async function onRemoveGamemaster(userId: string) {
  error.value = null
  try {
    await removeGamemaster(props.campaignId, userId)
    gamemasterIds.value = await listGamemasters(props.campaignId)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to remove Gamemaster.'
  }
}

async function confirmArchive() {
  showArchiveConfirm.value = false
  error.value = null
  try {
    await archive(props.campaignId)
    emit('archived')
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  }
}
</script>

<template>
  <BaseModal title="Manage Campaign" @close="emit('close')">
    <ErrorBanner :message="error" @dismiss="error = null" />

    <div class="mb-4 flex gap-2">
      <input v-model="name" type="text" class="flex-1 rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
      <BaseButton @click="submitRename">Rename</BaseButton>
    </div>

    <div class="mb-4">
      <div class="mb-1 flex items-center justify-between">
        <label class="text-xs font-medium text-slate-600">Gamemasters</label>
        <button class="text-xs text-indigo-600 hover:underline" @click="showAddGamemaster = !showAddGamemaster">+ Add</button>
      </div>
      <UserPicker v-if="showAddGamemaster" :exclude-ids="gamemasterIds" @select="onAddGamemaster" />
      <ul class="mt-2 space-y-1">
        <li v-for="id in gamemasterIds" :key="id" class="flex items-center justify-between text-sm">
          <span>{{ users.find((u) => u.id === id)?.name ?? id }}</span>
          <button class="text-xs text-red-500 hover:underline" @click="onRemoveGamemaster(id)">Remove</button>
        </li>
      </ul>
    </div>

    <div class="border-t border-slate-100 pt-3">
      <BaseButton variant="danger" @click="showArchiveConfirm = true">Archive Campaign</BaseButton>
    </div>

    <ConfirmDialog
      v-if="showArchiveConfirm"
      title="Archive Campaign"
      message="This campaign will be hidden from pickers. This cannot be undone through this app."
      confirm-label="Archive"
      @confirm="confirmArchive"
      @cancel="showArchiveConfirm = false"
    />
  </BaseModal>
</template>
