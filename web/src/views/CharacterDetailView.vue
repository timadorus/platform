<script setup lang="ts">
import { computed, inject, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useCharacters, type CharacterSummary } from '@/composables/useCharacters'
import type { AggregateChange } from '@/composables/useChangeFeed'
import { useUsers } from '@/composables/useUsers'
import BaseButton from '@/components/common/BaseButton.vue'
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

const { get, rename, archive, setPlayer, waitForCharacter } = useCharacters()
const { users, list: listUsers } = useUsers()
const bumpSidebarRefresh = inject<() => void>('bumpSidebarRefresh')

const character = ref<CharacterSummary | null>(null)
const error = ref<string | null>(null)
const showArchiveConfirm = ref(false)
const loadTimedOut = ref(false)

const activeTab = ref('Stats')
const tabs = ['Stats', 'Skills', 'Equipment', 'Journal']

const playerName = computed(
  () => users.value.find((u) => u.id === character.value?.playerUserId)?.name ?? character.value?.playerUserId ?? '',
)

// loadController is aborted both on unmount and at the start of every new load() call — the
// latter matters because vue-router reuses this component instance across param-only route
// changes (see characterId's own comment above): switching to a different Character mid-poll
// must not let a slow response for the *previous* one land after navigation and overwrite the
// page with the wrong data.
let loadController: AbortController | null = null

async function load() {
  loadController?.abort()
  const controller = new AbortController()
  loadController = controller
  loadTimedOut.value = false
  character.value = null

  const found = await waitForCharacter(characterId.value, { signal: controller.signal })
  if (controller.signal.aborted) return
  await listUsers()
  if (controller.signal.aborted) return
  if (found) {
    character.value = found
  } else {
    loadTimedOut.value = true
  }
}
onMounted(load)
watch(characterId, load)
onUnmounted(() => loadController?.abort())

const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'character' && change.aggregateId === characterId.value) load()
  })
}

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
  <div v-else-if="loadTimedOut" class="p-6">
    <p class="mb-4 text-sm text-slate-600">
      Couldn't load this Character — it may not exist, or may still be taking longer than
      expected to appear.
    </p>
    <div class="flex gap-2">
      <BaseButton @click="load">Retry</BaseButton>
      <router-link
        :to="{ name: 'campaign-overview', params: { universeId: route.params.universeId, campaignId: route.params.campaignId } }"
        class="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50"
      >
        Back to Campaign
      </router-link>
    </div>
  </div>
  <div v-else class="p-6 text-sm text-slate-500">Loading…</div>
</template>
