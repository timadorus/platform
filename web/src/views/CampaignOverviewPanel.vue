<script setup lang="ts">
import { computed, inject, onMounted, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useCampaigns, type CampaignSummary } from '@/composables/useCampaigns'
import type { AggregateChange } from '@/composables/useChangeFeed'
import { useRulesets } from '@/composables/useRulesets'
import { useUsers } from '@/composables/useUsers'
import { useCharacters } from '@/composables/useCharacters'
import { useEntities } from '@/composables/useEntities'
import { useObjects } from '@/composables/useObjects'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import BaseTabs from '@/components/common/BaseTabs.vue'
import ManageCampaignPanel from '@/components/campaign/ManageCampaignPanel.vue'
import ConfigurationPanel from '@/components/campaign/ConfigurationPanel.vue'

const route = useRoute()
const router = useRouter()
// computed, not a plain const: this panel is the `workspace` route's default child (path
// `''`), so vue-router reuses this component instance whenever the parent's :campaignId
// changes without leaving the workspace — a plain const captured once at setup would go stale.
const universeId = computed(() => route.params.universeId as string)
const campaignId = computed(() => route.params.campaignId as string)

const { get: getCampaign, rename, archive, listGamemasters, addGamemaster, removeGamemaster } = useCampaigns()
const { get: getRuleset } = useRulesets()
const { users, list: listUsers } = useUsers()
const { characters, list: listCharacters } = useCharacters()
const { entities, search: searchEntities } = useEntities()
const { objects, search: searchObjects } = useObjects()
const bumpSidebarRefresh = inject<() => void>('bumpSidebarRefresh')

const campaign = ref<CampaignSummary | null>(null)
const rulesetName = ref('')
const gamemasterIds = ref<string[]>([])
const error = ref<string | null>(null)
const loading = ref(true)
const showArchiveConfirm = ref(false)

const activeTab = ref('Manage')
const tabs = ['Manage', 'Configuration']

const gamemasters = computed(() =>
  gamemasterIds.value.map((id) => ({ id, name: users.value.find((u) => u.id === id)?.name ?? id })),
)

async function load() {
  loading.value = true
  campaign.value = await getCampaign(campaignId.value)
  await listUsers()
  const [ruleset, ids] = await Promise.all([
    campaign.value ? getRuleset(campaign.value.rulesetId) : Promise.resolve(null),
    listGamemasters(campaignId.value),
  ])
  rulesetName.value = ruleset?.name ?? '(unknown)'
  gamemasterIds.value = ids

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

const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'campaign' && change.aggregateId === campaignId.value) load()
  })
}

async function onSubmitRename(newName: string) {
  if (!campaign.value) return
  error.value = null
  try {
    await rename(campaign.value.id, newName)
    campaign.value.name = newName
    bumpSidebarRefresh?.()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to rename.'
  }
}

async function onAddGamemaster(userId: string) {
  error.value = null
  try {
    await addGamemaster(campaignId.value, userId)
    gamemasterIds.value = await listGamemasters(campaignId.value)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to add Gamemaster.'
  }
}

async function onRemoveGamemaster(userId: string) {
  error.value = null
  try {
    await removeGamemaster(campaignId.value, userId)
    gamemasterIds.value = await listGamemasters(campaignId.value)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to remove Gamemaster.'
  }
}

async function confirmArchive() {
  showArchiveConfirm.value = false
  error.value = null
  try {
    await archive(campaignId.value)
    bumpSidebarRefresh?.()
    router.push({ name: 'campaign-picker', params: { universeId: universeId.value } })
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  }
}
</script>

<template>
  <div v-if="loading" class="p-6 text-sm text-slate-500">Loading…</div>
  <div v-else-if="campaign" class="mx-auto max-w-2xl p-6">
    <ErrorBanner :message="error" @dismiss="error = null" />
    <BaseTabs :tabs="tabs" v-model="activeTab" class="mb-4" />

    <ManageCampaignPanel
      v-if="activeTab === 'Manage'"
      :name="campaign.name"
      :ruleset-name="rulesetName"
      :gamemasters="gamemasters"
      :character-count="characters.length"
      :entity-count="entities.length"
      :object-count="objects.length"
      @submit-rename="onSubmitRename"
      @add-gamemaster="onAddGamemaster"
      @remove-gamemaster="onRemoveGamemaster"
      @archive="showArchiveConfirm = true"
    />
    <ConfigurationPanel v-else-if="activeTab === 'Configuration'" :configuration="campaign.configuration" />

    <ConfirmDialog
      v-if="showArchiveConfirm"
      title="Archive Campaign"
      message="This campaign will be hidden from pickers. This cannot be undone through this app."
      confirm-label="Archive"
      @confirm="confirmArchive"
      @cancel="showArchiveConfirm = false"
    />
  </div>
  <div v-else class="p-6 text-sm text-slate-500">Campaign not found.</div>
</template>
