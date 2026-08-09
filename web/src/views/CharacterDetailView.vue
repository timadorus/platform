<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useCharacters, type CharacterSummary } from '@/composables/useCharacters'
import { useUsers } from '@/composables/useUsers'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import UserPicker from '@/components/pickers/UserPicker.vue'

const route = useRoute()
const router = useRouter()
// computed, not a plain const: vue-router reuses this component instance across param-only
// route changes on the same route record (e.g. clicking a different character card in the
// sidebar while already viewing one) — a plain const captured once at setup would go stale.
const characterId = computed(() => route.params.characterId as string)

const { get, rename, archive, setPlayer } = useCharacters()
const { users, list: listUsers } = useUsers()

const character = ref<CharacterSummary | null>(null)
const name = ref('')
const error = ref<string | null>(null)
const showArchiveConfirm = ref(false)
const showReassignPlayer = ref(false)

const playerName = computed(
  () => users.value.find((u) => u.id === character.value?.playerUserId)?.name ?? character.value?.playerUserId ?? '',
)

async function load() {
  character.value = await get(characterId.value)
  if (character.value) name.value = character.value.name
  await listUsers()
}
onMounted(load)
watch(characterId, load)

async function submitRename() {
  if (!character.value) return
  error.value = null
  try {
    await rename(character.value.id, name.value.trim())
    character.value.name = name.value.trim()
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
    router.push({ name: 'campaign-overview', params: { universeId: route.params.universeId, campaignId: route.params.campaignId } })
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  }
}

async function onReassignPlayer(userId: string) {
  if (!character.value) return
  showReassignPlayer.value = false
  error.value = null
  try {
    await setPlayer(character.value.id, userId)
    character.value.playerUserId = userId
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to reassign Player.'
  }
}
</script>

<template>
  <div v-if="character" class="mx-auto max-w-lg p-6">
    <ErrorBanner :message="error" @dismiss="error = null" />
    <div class="mb-4 flex gap-2">
      <input v-model="name" type="text" class="flex-1 rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
      <BaseButton @click="submitRename">Rename</BaseButton>
    </div>

    <div class="mb-4">
      <p class="mb-1 text-xs font-medium text-slate-600">Player</p>
      <p class="mb-2 text-sm text-slate-900">{{ playerName }}</p>
      <button class="text-xs text-indigo-600 hover:underline" @click="showReassignPlayer = !showReassignPlayer">
        Reassign Player
      </button>
      <UserPicker v-if="showReassignPlayer" @select="onReassignPlayer" />
    </div>

    <div class="border-t border-slate-100 pt-3">
      <BaseButton variant="danger" @click="showArchiveConfirm = true">Archive Character</BaseButton>
    </div>

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
