<script setup lang="ts">
import { computed, inject, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useCampaigns } from '@/composables/useCampaigns'
import { useUniverses, type UniverseSummary } from '@/composables/useUniverses'
import { useSelectionStore } from '@/stores/selection'
import { watchAggregate } from '@/composables/useAggregateWatch'
import type { AggregateChange } from '@/composables/useChangeFeed'
import AggregatePickerGrid from '@/components/pickers/AggregatePickerGrid.vue'
import CreateCampaignModal from '@/components/modals/CreateCampaignModal.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import AppHeader from '@/components/layout/AppHeader.vue'

const route = useRoute()
const router = useRouter()
const selection = useSelectionStore()
const universeId = computed(() => route.params.universeId as string)

const { campaigns, error, listByUniverse, get: getCampaign } = useCampaigns()
const { get: getUniverse } = useUniverses()

const universe = ref<UniverseSummary | null>(null)
const showCreate = ref(false)
const checkingStoredSelection = ref(true)

function goTo(campaignId: string) {
  selection.setUniverse(universeId.value)
  selection.setCampaign(campaignId)
  // Push the *child* route ('campaign-overview'), not its parent ('workspace') — see
  // UniverseOverviewPanel.vue's goToCampaign for why: a named push to the parent never descends
  // into its default-path ('') child, leaving the workspace's nested <router-view> empty.
  router.push({ name: 'campaign-overview', params: { universeId: universeId.value, campaignId } })
}

function goToUniverseOverview() {
  router.push({ name: 'universe-overview', params: { universeId: universeId.value } })
}

onMounted(async () => {
  universe.value = await getUniverse(universeId.value)
  selection.load()
  if (selection.selectedUniverseId === universeId.value && selection.selectedCampaignId) {
    const existing = await getCampaign(selection.selectedCampaignId)
    if (existing && !existing.isArchived && existing.universeId === universeId.value) {
      // Deliberately do NOT set checkingStoredSelection = false here — same reasoning as
      // UniversePickerView.vue (Task 6): this component is about to unmount as router.push
      // navigates away, and flipping it first would flash the still-empty picker grid.
      goTo(existing.id)
      return
    }
    selection.clearCampaign()
  }
  checkingStoredSelection.value = false
  await listByUniverse(universeId.value)
})

let unwatchCampaigns: (() => void) | null = null
watch(
  universeId,
  (id) => {
    unwatchCampaigns?.()
    unwatchCampaigns = watchAggregate({ type: 'campaign', universeId: id })
  },
  { immediate: true },
)
// vue-router reuses this component instance across a universeId-only change on the
// campaign-picker route (no :key on App.vue's <router-view>) — same mechanics as
// UniverseOverviewPanel.vue's (Task 9) universeId watch. Registrations made from inside a
// watch callback that fires after setup has finished run outside the component's active
// effect scope, so watchAggregate's own onScopeDispose guard does not catch them; only the
// very first (immediate) call is scope-tracked. This onUnmounted is required to release
// whichever registration is currently active when the instance is actually torn down.
onUnmounted(() => unwatchCampaigns?.())

const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'campaign') listByUniverse(universeId.value)
  })
}

function onCreated(id: string) {
  showCreate.value = false
  goTo(id)
}
</script>

<template>
  <AppHeader :universe-name="universe?.name ?? null" :campaign-name="null" @click-universe-badge="goToUniverseOverview" />
  <div v-if="checkingStoredSelection" class="p-6 text-sm text-slate-500">Loading…</div>
  <div v-else class="mx-auto max-w-2xl p-6">
    <p class="mb-1 text-xs uppercase tracking-wide text-slate-400">🌍 {{ universe?.name }}</p>
    <h1 class="mb-4 text-lg font-semibold text-slate-900">Choose a Campaign</h1>
    <ErrorBanner :message="error" @dismiss="error = null" />
    <AggregatePickerGrid
      :items="campaigns.map((c) => ({ id: c.id, name: c.name }))"
      create-label="Create Campaign"
      @select="goTo"
      @create="showCreate = true"
    />
    <CreateCampaignModal v-if="showCreate" :universe-id="universeId" @close="showCreate = false" @created="onCreated" />
  </div>
</template>
