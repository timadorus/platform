<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useCampaigns, type CampaignSummary } from '@/composables/useCampaigns'
import { useRulesets } from '@/composables/useRulesets'
import { useUsers } from '@/composables/useUsers'
import { useCharacters } from '@/composables/useCharacters'
import { useEntities } from '@/composables/useEntities'
import { useObjects } from '@/composables/useObjects'

const route = useRoute()
// computed, not a plain const: this panel is the `workspace` route's default child (path
// `''`), so vue-router reuses this component instance whenever the parent's :campaignId
// changes without leaving the workspace (e.g. via ManageCampaignModal or a future
// switch-campaign link) — a plain const captured once at setup would go stale.
const universeId = computed(() => route.params.universeId as string)
const campaignId = computed(() => route.params.campaignId as string)

const { get: getCampaign, listGamemasters } = useCampaigns()
const { get: getRuleset } = useRulesets()
const { users, list: listUsers } = useUsers()
const { characters, list: listCharacters } = useCharacters()
const { entities, search: searchEntities } = useEntities()
const { objects, search: searchObjects } = useObjects()

const campaign = ref<CampaignSummary | null>(null)
const rulesetName = ref('')
const gamemasterNames = ref<string[]>([])
const loading = ref(true)

async function load() {
  loading.value = true
  campaign.value = await getCampaign(campaignId.value)
  await listUsers()
  const [ruleset, gamemasterIds] = await Promise.all([
    campaign.value ? getRuleset(campaign.value.rulesetId) : Promise.resolve(null),
    listGamemasters(campaignId.value),
  ])
  rulesetName.value = ruleset?.name ?? '(unknown)'
  gamemasterNames.value = gamemasterIds.map((id) => users.value.find((u) => u.id === id)?.name ?? id)

  // Entities/Objects belong to the Universe, not the Campaign (docs/PLAN.md §2) — this count
  // is Universe-wide, matching exactly what the sidebar's own Entities/Objects panels show
  // for this Universe, not a Campaign-scoped subset that doesn't exist in the domain model.
  await Promise.all([
    listCharacters(campaignId.value),
    searchEntities(universeId.value, ''),
    searchObjects(universeId.value, ''),
  ])
  loading.value = false
}
onMounted(load)
watch([universeId, campaignId], load)
</script>

<template>
  <div v-if="loading" class="p-6 text-sm text-slate-500">Loading…</div>
  <div v-else-if="campaign" class="mx-auto max-w-lg p-6">
    <h1 class="mb-1 text-lg font-semibold text-slate-900">{{ campaign.name }}</h1>
    <p class="mb-4 text-sm text-slate-500">Ruleset: {{ rulesetName }}</p>

    <div class="mb-4">
      <p class="mb-1 text-xs font-medium uppercase tracking-wide text-slate-400">Gamemasters</p>
      <p class="text-sm text-slate-700">{{ gamemasterNames.join(', ') }}</p>
    </div>

    <div class="grid grid-cols-3 gap-3">
      <div class="rounded-md border border-slate-200 p-3 text-center">
        <p class="text-xl font-semibold text-slate-900">{{ characters.length }}</p>
        <p class="text-xs text-slate-500">Characters</p>
      </div>
      <div class="rounded-md border border-slate-200 p-3 text-center">
        <p class="text-xl font-semibold text-slate-900">{{ entities.length }}</p>
        <p class="text-xs text-slate-500">Entities</p>
      </div>
      <div class="rounded-md border border-slate-200 p-3 text-center">
        <p class="text-xl font-semibold text-slate-900">{{ objects.length }}</p>
        <p class="text-xs text-slate-500">Objects</p>
      </div>
    </div>
  </div>
  <div v-else class="p-6 text-sm text-slate-500">Campaign not found.</div>
</template>
