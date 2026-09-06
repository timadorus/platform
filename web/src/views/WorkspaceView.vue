<script setup lang="ts">
import { computed, onMounted, onUnmounted, provide, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUniverses, type UniverseSummary } from '@/composables/useUniverses'
import { useCampaigns, type CampaignSummary } from '@/composables/useCampaigns'
import { useChangeFeed } from '@/composables/useChangeFeed'
import AppHeader from '@/components/layout/AppHeader.vue'
import AppSidebar from '@/components/layout/AppSidebar.vue'
import CharactersPanel from '@/components/layout/CharactersPanel.vue'
import EntitiesPanel from '@/components/layout/EntitiesPanel.vue'
import ObjectsPanel from '@/components/layout/ObjectsPanel.vue'

const route = useRoute()
const router = useRouter()
// computed, not a plain const — see Task 8's identical comment: vue-router reuses this
// component instance across param-only route changes on the same route record.
const universeId = computed(() => route.params.universeId as string)
const campaignId = computed(() => route.params.campaignId as string)

const { get: getUniverse } = useUniverses()
const { waitForCampaign } = useCampaigns()

const universe = ref<UniverseSummary | null>(null)
const campaign = ref<CampaignSummary | null>(null)

const sidebarRefreshSignal = ref(0)
provide('sidebarRefreshSignal', sidebarRefreshSignal)
provide('bumpSidebarRefresh', () => {
  sidebarRefreshSignal.value++
})

// pendingEntityId lets CharactersPanel tell EntitiesPanel "a new Entity with this id was just
// auto-created (via Character creation) — poll for it specifically" without overloading the
// generic sidebarRefreshSignal above (which fires for rename/archive/reassign flows that don't
// need retrying). CharactersPanel sets it; EntitiesPanel watches it, polls, and clears it.
const pendingEntityId = ref<string | null>(null)
provide('pendingEntityId', pendingEntityId)

const { lastChange: lastAggregateChange, start: startChangeFeed, stop: stopChangeFeed } = useChangeFeed()
provide('lastAggregateChange', lastAggregateChange)

async function load() {
  universe.value = await getUniverse(universeId.value)
  campaign.value = await waitForCampaign(campaignId.value)
}

onMounted(load)
watch([universeId, campaignId], load)
onMounted(() => startChangeFeed(universeId.value))
watch(universeId, startChangeFeed)
onUnmounted(stopChangeFeed)
// Campaign rename/archive now happen inside CampaignOverviewPanel.vue (the workspace route's
// default child), not a modal owned here — it bumps the same shared signal
// CharactersPanel/EntitiesPanel already react to, so re-running load() here keeps the header's
// campaign name in sync the same way.
watch(sidebarRefreshSignal, load)

function goToCampaignOverview() {
  router.push({ name: 'campaign-overview', params: { universeId: universeId.value, campaignId: campaignId.value } })
}
function goToUniverseOverview() {
  router.push({ name: 'universe-overview', params: { universeId: universeId.value } })
}
</script>

<template>
  <div class="flex h-screen flex-col">
    <AppHeader
      :universe-name="universe?.name ?? null"
      :campaign-name="campaign?.name ?? null"
      @click-universe-badge="goToUniverseOverview"
      @click-campaign-badge="goToCampaignOverview"
    />
    <div class="flex flex-1 overflow-hidden">
      <AppSidebar>
        <template #characters>
          <CharactersPanel :campaign-id="campaignId" />
        </template>
        <template #entities>
          <EntitiesPanel :universe-id="universeId" />
        </template>
        <template #objects>
          <ObjectsPanel :universe-id="universeId" />
        </template>
      </AppSidebar>
      <main class="flex-1 overflow-y-auto">
        <router-view />
      </main>
    </div>
  </div>
</template>
