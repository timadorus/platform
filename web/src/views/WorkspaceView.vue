<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useUniverses, type UniverseSummary } from '@/composables/useUniverses'
import { useCampaigns, type CampaignSummary } from '@/composables/useCampaigns'
import AppHeader from '@/components/layout/AppHeader.vue'
import AppSidebar from '@/components/layout/AppSidebar.vue'
import BaseModal from '@/components/common/BaseModal.vue'

const route = useRoute()
// computed, not a plain const: vue-router reuses this component instance across param-only
// route changes on the same route record (e.g. navigating from one Campaign to another
// without leaving the workspace), so a plain `const` captured once at setup would silently
// go stale — computed() stays in sync, and the watch below re-triggers load() on change.
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
          <p class="text-xs text-slate-400">Characters panel — implemented in Task 10.</p>
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

    <BaseModal v-if="showManageUniverse" title="Manage Universe" @close="showManageUniverse = false">
      <p class="text-sm text-slate-500">Universe management — implemented in Task 9.</p>
    </BaseModal>
    <BaseModal v-if="showManageCampaign" title="Manage Campaign" @close="showManageCampaign = false">
      <p class="text-sm text-slate-500">Campaign management — implemented in Task 9.</p>
    </BaseModal>
  </div>
</template>
