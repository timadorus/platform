<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useUniverses, type UniverseSummary } from '@/composables/useUniverses'
import { useCampaigns, type CampaignSummary } from '@/composables/useCampaigns'
import AppHeader from '@/components/layout/AppHeader.vue'
import AppSidebar from '@/components/layout/AppSidebar.vue'
import ManageUniverseModal from '@/components/modals/ManageUniverseModal.vue'
import ManageCampaignModal from '@/components/modals/ManageCampaignModal.vue'
import CharactersPanel from '@/components/layout/CharactersPanel.vue'

const route = useRoute()
const router = useRouter()
// computed, not a plain const — see Task 8's identical comment: vue-router reuses this
// component instance across param-only route changes on the same route record.
const universeId = computed(() => route.params.universeId as string)
const campaignId = computed(() => route.params.campaignId as string)

const { get: getUniverse } = useUniverses()
const { get: getCampaign } = useCampaigns()

const universe = ref<UniverseSummary | null>(null)
const campaign = ref<CampaignSummary | null>(null)
const showManageUniverse = ref(false)
const showManageCampaign = ref(false)

async function load() {
  universe.value = await getUniverse(universeId.value)
  campaign.value = await getCampaign(campaignId.value)
}

onMounted(load)
watch([universeId, campaignId], load)

function onUniverseRenamed(newName: string) {
  if (universe.value) universe.value.name = newName
  showManageUniverse.value = false
}
function onUniverseArchived() {
  showManageUniverse.value = false
  router.push({ name: 'universe-picker' })
}
function onCampaignRenamed(newName: string) {
  if (campaign.value) campaign.value.name = newName
  showManageCampaign.value = false
}
function onCampaignArchived() {
  showManageCampaign.value = false
  router.push({ name: 'campaign-picker', params: { universeId: universeId.value } })
}
</script>

<template>
  <div class="flex h-screen flex-col">
    <AppHeader
      :universe-name="universe?.name ?? null"
      :campaign-name="campaign?.name ?? null"
      @click-universe-badge="showManageUniverse = true"
      @click-campaign-badge="showManageCampaign = true"
    />
    <div class="flex flex-1 overflow-hidden">
      <AppSidebar>
        <template #characters>
          <CharactersPanel :campaign-id="campaignId" />
        </template>
        <template #entities>
          <p class="text-xs text-slate-400">Entities panel — implemented in Task 12.</p>
        </template>
        <template #objects>
          <p class="text-xs text-slate-400">Objects panel — implemented in Task 13.</p>
        </template>
      </AppSidebar>
      <main class="flex-1 overflow-y-auto">
        <router-view />
      </main>
    </div>

    <ManageUniverseModal
      v-if="showManageUniverse && universe"
      :universe-id="universeId"
      :universe-name="universe.name"
      @close="showManageUniverse = false"
      @renamed="onUniverseRenamed"
      @archived="onUniverseArchived"
    />
    <ManageCampaignModal
      v-if="showManageCampaign && campaign"
      :campaign-id="campaignId"
      :campaign-name="campaign.name"
      @close="showManageCampaign = false"
      @renamed="onCampaignRenamed"
      @archived="onCampaignArchived"
    />
  </div>
</template>
