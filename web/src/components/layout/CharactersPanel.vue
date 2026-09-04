<script setup lang="ts">
import { computed, inject, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { useRoute, useRouter } from 'vue-router'
import { useCharacters } from '@/composables/useCharacters'
import { useCampaigns } from '@/composables/useCampaigns'
import { useUsers } from '@/composables/useUsers'
import CharacterCard from './CharacterCard.vue'
import CreateCharacterModal from '@/components/modals/CreateCharacterModal.vue'
import type { AggregateChange } from '@/composables/useChangeFeed'

const props = defineProps<{ campaignId: string }>()
const route = useRoute()
const router = useRouter()

const { characters, list, waitForCharacterInList } = useCharacters()
const { listGamemasters } = useCampaigns()
const { users, list: listUsers } = useUsers()
const gamemasterIds = ref<string[]>([])
const showCreate = ref(false)
const scrollParent = ref<HTMLElement | null>(null)

const virtualizer = useVirtualizer(
  computed(() => ({
    count: characters.value.length,
    getScrollElement: () => scrollParent.value,
    estimateSize: () => 48,
    overscan: 8,
  })),
)

async function refresh() {
  await Promise.all([list(props.campaignId), listUsers()])
  gamemasterIds.value = await listGamemasters(props.campaignId)
}
onMounted(refresh)

const sidebarRefreshSignal = inject<Ref<number>>('sidebarRefreshSignal')
if (sidebarRefreshSignal) {
  watch(sidebarRefreshSignal, refresh)
}
const pendingEntityId = inject<Ref<string | null>>('pendingEntityId')

const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'character') refresh()
  })
}

// Holds the AbortController for the current onCreated background poll (if any), so it can be
// aborted on unmount — mirrors CreatingUserModal.vue's pattern for waitForUser.
let createdCharacterController: AbortController | null = null

// playerLabel implements the NPC display rule: a Character whose Player is one of the
// Campaign's Gamemasters is shown as "(NPC)" instead of a player name (design spec §7).
function playerLabel(playerUserId: string): string {
  if (gamemasterIds.value.includes(playerUserId)) return '(NPC)'
  return users.value.find((u) => u.id === playerUserId)?.name ?? playerUserId
}

function select(characterId: string) {
  router.push({ name: 'character-detail', params: { ...route.params, characterId } })
}

function onCreated(characterId: string, entityId: string) {
  showCreate.value = false
  select(characterId)
  // Poll this panel's own list in the background until the new Character actually appears —
  // list() reassigns `characters` reactively as a side effect, which the template already
  // renders, so nothing further needs to happen once this resolves. Not awaited: this is a
  // fire-and-forget background retry, not something the caller needs to wait on. The signal is
  // aborted on unmount (below) so the poll doesn't keep hitting the backend after the user
  // navigates away.
  createdCharacterController = new AbortController()
  waitForCharacterInList(props.campaignId, characterId, { signal: createdCharacterController.signal }).then((found) => {
    // Once the new Character is actually visible, do a full refresh() (not just another list())
    // so `users`/`gamemasterIds` — loaded once at mount — pick up a brand-new User this
    // Character might be assigned to. Without this, playerLabel() shows the raw playerUserId
    // UUID until some unrelated refresh happens to fire. Skipped on a timeout/abort (found ===
    // false): there's no new Character to correct the label for in that case.
    if (found) void refresh()
  })
  // The auto-created Entity lives in a sibling panel (EntitiesPanel) — tell it which id to wait
  // for via the shared pendingEntityId ref (WorkspaceView.vue), rather than the generic
  // sidebarRefreshSignal (which only fires once, with no retry).
  if (pendingEntityId) pendingEntityId.value = entityId
}

onUnmounted(() => createdCharacterController?.abort())
</script>

<template>
  <div>
    <div ref="scrollParent" class="max-h-96 overflow-y-auto">
      <div :style="{ height: `${virtualizer.getTotalSize()}px`, position: 'relative' }">
        <CharacterCard
          v-for="row in virtualizer.getVirtualItems()"
          :key="String(row.key)"
          :character="characters[row.index]"
          :player-label="playerLabel(characters[row.index].playerUserId)"
          :style="{ position: 'absolute', top: 0, left: 0, width: '100%', transform: `translateY(${row.start}px)` }"
          @click="select(characters[row.index].id)"
        />
      </div>
    </div>
    <button
      class="mt-2 w-full rounded-md border border-dashed border-slate-300 py-1.5 text-xs text-slate-500 hover:border-indigo-400 hover:text-indigo-600"
      @click="showCreate = true"
    >
      + Create Character
    </button>
    <CreateCharacterModal v-if="showCreate" :campaign-id="campaignId" @close="showCreate = false" @created="onCreated" />
  </div>
</template>
