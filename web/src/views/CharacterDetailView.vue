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
        :key="character.id"
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
