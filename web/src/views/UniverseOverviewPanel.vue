<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUniverses, type UniverseSummary } from '@/composables/useUniverses'
import { useUsers } from '@/composables/useUsers'
import { useCampaigns } from '@/composables/useCampaigns'
import AppHeader from '@/components/layout/AppHeader.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import UserPicker from '@/components/pickers/UserPicker.vue'

const route = useRoute()
const router = useRouter()
const universeId = computed(() => route.params.universeId as string)

const { get: getUniverse, rename, archive, listCreators, addCreator, removeCreator } = useUniverses()
const { users, list: listUsers } = useUsers()
const { campaigns, listByUniverse } = useCampaigns()

const universe = ref<UniverseSummary | null>(null)
const creatorIds = ref<string[]>([])
const error = ref<string | null>(null)
const loading = ref(true)

const editingName = ref(false)
const nameDraft = ref('')
const showAddCreator = ref(false)
const showArchiveConfirm = ref(false)

const creators = computed(() =>
  creatorIds.value.map((id) => ({ id, name: users.value.find((u) => u.id === id)?.name ?? id })),
)

async function load() {
  loading.value = true
  universe.value = await getUniverse(universeId.value)
  await listUsers()
  creatorIds.value = await listCreators(universeId.value)
  await listByUniverse(universeId.value)
  loading.value = false
}
onMounted(load)

function startEditName() {
  if (!universe.value) return
  nameDraft.value = universe.value.name
  editingName.value = true
}
async function saveName() {
  if (!universe.value) return
  error.value = null
  try {
    await rename(universeId.value, nameDraft.value.trim())
    universe.value.name = nameDraft.value.trim()
    editingName.value = false
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to rename.'
  }
}

async function onAddCreator(userId: string) {
  showAddCreator.value = false
  error.value = null
  try {
    await addCreator(universeId.value, userId)
    creatorIds.value = await listCreators(universeId.value)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to add Creator.'
  }
}

async function onRemoveCreator(userId: string) {
  error.value = null
  try {
    await removeCreator(universeId.value, userId)
    creatorIds.value = await listCreators(universeId.value)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to remove Creator.'
  }
}

async function confirmArchive() {
  showArchiveConfirm.value = false
  error.value = null
  try {
    await archive(universeId.value)
    router.push({ name: 'universe-picker' })
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  }
}

function goToCampaign(campaignId: string) {
  router.push({ name: 'workspace', params: { universeId: universeId.value, campaignId } })
}
</script>

<template>
  <AppHeader :universe-name="universe?.name ?? null" :campaign-name="null" />
  <div v-if="loading" class="p-6 text-sm text-slate-500">Loading…</div>
  <div v-else-if="universe" class="mx-auto max-w-2xl p-6">
    <ErrorBanner :message="error" @dismiss="error = null" />

    <div class="mb-4 flex items-center gap-2">
      <template v-if="editingName">
        <input v-model="nameDraft" type="text" class="flex-1 rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
        <button class="text-xs text-indigo-600 hover:underline" @click="saveName">Save</button>
        <button class="text-xs text-slate-400 hover:underline" @click="editingName = false">Cancel</button>
      </template>
      <template v-else>
        <h1 class="flex-1 text-lg font-semibold text-slate-900">{{ universe.name }}</h1>
        <button class="text-xs text-indigo-600 hover:underline" @click="startEditName">Rename</button>
      </template>
    </div>

    <div class="mb-4">
      <div class="mb-1 flex items-center justify-between">
        <label class="text-xs font-medium text-slate-600">Creators</label>
        <button class="text-xs text-indigo-600 hover:underline" @click="showAddCreator = !showAddCreator">+ Add</button>
      </div>
      <UserPicker v-if="showAddCreator" :exclude-ids="creatorIds" @select="onAddCreator" />
      <ul class="mt-2 space-y-1">
        <li v-for="c in creators" :key="c.id" class="flex items-center justify-between text-sm">
          <span>{{ c.name }}</span>
          <button class="text-xs text-red-500 hover:underline" @click="onRemoveCreator(c.id)">Remove</button>
        </li>
      </ul>
    </div>

    <div class="mb-4">
      <label class="mb-1 block text-xs font-medium text-slate-600">Campaigns</label>
      <ul v-if="campaigns.length" class="space-y-1">
        <li v-for="c in campaigns" :key="c.id">
          <button class="text-sm text-indigo-600 hover:underline" @click="goToCampaign(c.id)">{{ c.name }}</button>
        </li>
      </ul>
      <p v-else class="text-sm text-slate-500">No Campaigns yet.</p>
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
  </div>
  <div v-else class="p-6 text-sm text-slate-500">Universe not found.</div>
</template>
