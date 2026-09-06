<script setup lang="ts">
import { computed, inject, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useCampaigns, type CampaignSummary } from '@/composables/useCampaigns'
import type { AggregateChange } from '@/composables/useChangeFeed'
import { useRulesets } from '@/composables/useRulesets'
import { useUsers } from '@/composables/useUsers'
import { useCharacters } from '@/composables/useCharacters'
import { useEntities } from '@/composables/useEntities'
import { useObjects } from '@/composables/useObjects'
import BaseButton from '@/components/common/BaseButton.vue'
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

const { waitForCampaign, rename, archive, listGamemasters, addGamemaster, removeGamemaster } = useCampaigns()
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
const loadTimedOut = ref(false)

const activeTab = ref('Manage')
const tabs = ['Manage', 'Configuration']

const gamemasters = computed(() =>
  gamemasterIds.value.map((id) => ({ id, name: users.value.find((u) => u.id === id)?.name ?? id })),
)

// silent: true for a background reload triggered by the change-feed (see the lastAggregateChange
// watch below) — must NOT toggle `loading`, since the template's `v-if="loading"` gates the
// entire subtree including <ConfigurationPanel>, and unmounting it on every unrelated background
// change would destroy its pending-save state and any unsaved draft. A genuine campaign switch
// (the watch further down) stays non-silent so it still shows the full "Loading…" state.
//
// loadController is aborted both on unmount and at the start of every new load() call — the
// latter matters because vue-router reuses this component instance across param-only route
// changes (see campaignId's own comment above): switching to a different Campaign mid-poll must
// not let a slow response for the *previous* one land after navigation and overwrite the page
// with the wrong data. Mirrors CharacterDetailView.vue's identical fix.
let loadController: AbortController | null = null
async function load(opts: { silent?: boolean } = {}) {
  loadController?.abort()
  const controller = new AbortController()
  loadController = controller
  loadTimedOut.value = false
  if (!opts.silent) loading.value = true

  const found = await waitForCampaign(campaignId.value, { signal: controller.signal })
  if (controller.signal.aborted) return
  campaign.value = found
  if (found) {
    await listUsers()
    if (controller.signal.aborted) return
    const [ruleset, ids] = await Promise.all([getRuleset(found.rulesetId), listGamemasters(campaignId.value)])
    if (controller.signal.aborted) return
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
    if (controller.signal.aborted) return
  } else if (!opts.silent) {
    loadTimedOut.value = true
  }
  if (!opts.silent) loading.value = false
}
// Both call sites are wrapped in arrow functions deliberately, not cosmetically: passed directly,
// onMounted's and watch's callback signatures would pass their own arguments (e.g. watch's
// `(newValue, oldValue, onCleanup)`) as load's first argument, silently shadowing `opts`.
onMounted(() => load())
watch([universeId, campaignId], () => load())
onUnmounted(() => loadController?.abort())

const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'campaign' && change.aggregateId.toLowerCase() === campaignId.value.toLowerCase()) {
      load({ silent: true })
    }
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
    <ConfigurationPanel
      v-else-if="activeTab === 'Configuration'"
      :key="campaignId"
      :campaign-id="campaignId"
      :configuration="campaign.configuration"
    />

    <ConfirmDialog
      v-if="showArchiveConfirm"
      title="Archive Campaign"
      message="This campaign will be hidden from pickers. This cannot be undone through this app."
      confirm-label="Archive"
      @confirm="confirmArchive"
      @cancel="showArchiveConfirm = false"
    />
  </div>
  <div v-else-if="loadTimedOut" class="p-6">
    <p class="mb-4 text-sm text-slate-600">
      Couldn't load this Campaign — it may not exist, or may still be taking longer than expected
      to appear.
    </p>
    <div class="flex gap-2">
      <BaseButton @click="load()">Retry</BaseButton>
      <router-link
        :to="{ name: 'universe-overview', params: { universeId } }"
        class="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50"
      >
        Back to Universe
      </router-link>
    </div>
  </div>
  <div v-else class="p-6 text-sm text-slate-500">Campaign not found.</div>
</template>
