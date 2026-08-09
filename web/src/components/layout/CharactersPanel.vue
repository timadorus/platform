<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useVirtualizer } from '@tanstack/vue-virtual'
import { useRoute, useRouter } from 'vue-router'
import { useCharacters } from '@/composables/useCharacters'
import { useCampaigns } from '@/composables/useCampaigns'
import { useUsers } from '@/composables/useUsers'
import CharacterCard from './CharacterCard.vue'
import CreateCharacterModal from '@/components/modals/CreateCharacterModal.vue'

const props = defineProps<{ campaignId: string }>()
const route = useRoute()
const router = useRouter()

const { characters, list } = useCharacters()
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

// playerLabel implements the NPC display rule: a Character whose Player is one of the
// Campaign's Gamemasters is shown as "(NPC)" instead of a player name (design spec §7).
function playerLabel(playerUserId: string): string {
  if (gamemasterIds.value.includes(playerUserId)) return '(NPC)'
  return users.value.find((u) => u.id === playerUserId)?.name ?? playerUserId
}

function select(characterId: string) {
  router.push({ name: 'character-detail', params: { ...route.params, characterId } })
}

function onCreated() {
  showCreate.value = false
  refresh()
}
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
