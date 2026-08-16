<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useUsers } from '@/composables/useUsers'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import CreateUserModal from '@/components/modals/CreateUserModal.vue'
import CreatingUserModal from '@/components/modals/CreatingUserModal.vue'
import AppHeader from '@/components/layout/AppHeader.vue'

const { users, list, rename, archive } = useUsers()
const error = ref<string | null>(null)
const showCreate = ref(false)
const creatingUser = ref<{ id: string; name: string } | null>(null)
const editingId = ref<string | null>(null)
const editingName = ref('')
const archiveTargetId = ref<string | null>(null)

onMounted(list)

function startEdit(id: string, currentName: string) {
  editingId.value = id
  editingName.value = currentName
}

async function saveEdit() {
  if (!editingId.value) return
  error.value = null
  try {
    await rename(editingId.value, editingName.value.trim())
    await list()
    editingId.value = null
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to rename.'
  }
}

async function confirmArchive() {
  if (!archiveTargetId.value) return
  error.value = null
  try {
    await archive(archiveTargetId.value)
    await list()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  } finally {
    archiveTargetId.value = null
  }
}

function onCreated(id: string, name: string) {
  showCreate.value = false
  creatingUser.value = { id, name }
}

async function onCreatingDone() {
  creatingUser.value = null
  await list()
}

async function onCreatingClose() {
  creatingUser.value = null
  await list()
}
</script>

<template>
  <AppHeader :universe-name="null" :campaign-name="null" />
  <div class="mx-auto max-w-xl p-6">
    <div class="mb-4 flex items-center justify-between">
      <h1 class="text-lg font-semibold text-slate-900">Users</h1>
      <BaseButton @click="showCreate = true">+ Create User</BaseButton>
    </div>
    <ErrorBanner :message="error" @dismiss="error = null" />
    <ul class="divide-y divide-slate-100 rounded-md border border-slate-200">
      <li v-for="user in users" :key="user.id" class="flex items-center justify-between gap-2 px-3 py-2">
        <template v-if="editingId === user.id">
          <input v-model="editingName" type="text" class="flex-1 rounded-md border border-slate-300 px-2 py-1 text-sm" />
          <button class="text-xs text-indigo-600 hover:underline" @click="saveEdit">Save</button>
          <button class="text-xs text-slate-400 hover:underline" @click="editingId = null">Cancel</button>
        </template>
        <template v-else>
          <span class="text-sm text-slate-900">{{ user.name }}</span>
          <span class="flex gap-3">
            <button class="text-xs text-indigo-600 hover:underline" @click="startEdit(user.id, user.name)">Rename</button>
            <button class="text-xs text-red-500 hover:underline" @click="archiveTargetId = user.id">Archive</button>
          </span>
        </template>
      </li>
      <li v-if="users.length === 0" class="px-3 py-2 text-xs text-slate-400">No Users yet.</li>
    </ul>

    <CreateUserModal v-if="showCreate" @close="showCreate = false" @created="onCreated" />
    <CreatingUserModal
      v-if="creatingUser"
      :user-id="creatingUser.id"
      :user-name="creatingUser.name"
      @done="onCreatingDone"
      @close="onCreatingClose"
    />
    <ConfirmDialog
      v-if="archiveTargetId"
      title="Archive User"
      message="This user will be hidden from pickers and lists. This cannot be undone through this app."
      confirm-label="Archive"
      @confirm="confirmArchive"
      @cancel="archiveTargetId = null"
    />
  </div>
</template>
