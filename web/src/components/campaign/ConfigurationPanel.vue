<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import { useCampaigns } from '@/composables/useCampaigns'
import ErrorBanner from '@/components/common/ErrorBanner.vue'

const props = defineProps<{ campaignId: string; configuration: string }>()
const { requestConfiguration } = useCampaigns()

// The Configuration field is an opaque JSON string set via PUT /campaigns/{id}/configure — it
// starts unset/empty until that endpoint is called at least once, which is not valid JSON, so
// pretty-printing is attempted defensively rather than assumed to always succeed.
const parsedConfiguration = computed<Record<string, any> | null>(() => {
  if (!props.configuration) return null
  try {
    return JSON.parse(props.configuration)
  } catch {
    return null
  }
})

const prettyPrinted = computed(() => {
  if (!parsedConfiguration.value) return props.configuration || null
  return JSON.stringify(parsedConfiguration.value, null, 2)
})

const currentMaxStatBudget = computed<number | null>(
  () => parsedConfiguration.value?.characterCreation?.maxStatBudget ?? null,
)

// Initialized once from whatever the Campaign's configuration already says — deliberately not
// kept in sync with currentMaxStatBudget afterward (see the watch below and this component's own
// :key="campaignId" at its call site), so an unrelated background refresh never overwrites what
// the user is mid-typing. Mirrors the rename-draft-loss fix already applied elsewhere in this
// codebase (BaseInfoTable.vue/ManageCampaignPanel.vue).
const maxStatBudgetInput = ref<number | null>(currentMaxStatBudget.value)

type Status = 'idle' | 'pending' | 'error'
const status = ref<Status>('idle')
const errorMessage = ref<string | null>(null)
// The value most recently submitted, so the watch below can tell "the loaded configuration now
// reflects my own request" apart from "someone else changed something unrelated in configuration".
let pendingValue: number | null = null

// A non-Timadorus Campaign silently no-ops on `configure` (by design) — no ConfigurationChanged
// event is ever emitted, so the currentMaxStatBudget watch below never fires and `status` would
// stay 'pending' forever with Save disabled. This timeout guarantees the panel always resolves.
const PENDING_TIMEOUT_MS = 10000
let pendingTimeoutHandle: ReturnType<typeof setTimeout> | null = null

function clearPendingTimeout() {
  if (pendingTimeoutHandle) {
    clearTimeout(pendingTimeoutHandle)
    pendingTimeoutHandle = null
  }
}

async function save() {
  if (maxStatBudgetInput.value === null) return
  if (!Number.isInteger(maxStatBudgetInput.value)) {
    status.value = 'error'
    errorMessage.value = 'Max Stat Budget must be a whole number.'
    return
  }
  status.value = 'pending'
  errorMessage.value = null
  pendingValue = maxStatBudgetInput.value
  clearPendingTimeout()
  pendingTimeoutHandle = setTimeout(() => {
    if (status.value === 'pending') {
      status.value = 'error'
      errorMessage.value =
        'No confirmation received — this only takes effect on a Timadorus-ruleset Campaign. If this Campaign uses a different Ruleset, the change was not applied.'
      pendingValue = null
    }
  }, PENDING_TIMEOUT_MS)
  try {
    await requestConfiguration(props.campaignId, { action: 'setMaxStatBudget', value: maxStatBudgetInput.value })
  } catch (err) {
    clearPendingTimeout()
    status.value = 'error'
    errorMessage.value = err instanceof Error ? err.message : 'Failed to request configuration change.'
  }
}

// Fire-and-forget by design (see the design spec): a 204 from PUT .../configure only means the
// request was accepted, not that the value actually changed (e.g. a non-Timadorus Campaign
// silently no-ops). Only clear the pending status once the loaded configuration actually reflects
// what was submitted — driven by CampaignOverviewPanel.vue's existing lastAggregateChange watch
// reloading the Campaign, which is what updates this component's `configuration` prop.
watch(currentMaxStatBudget, (value) => {
  if (status.value === 'pending' && pendingValue !== null && value === pendingValue) {
    clearPendingTimeout()
    status.value = 'idle'
    pendingValue = null
  }
})

onUnmounted(clearPendingTimeout)
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4">
    <h2 class="mb-3 text-sm font-semibold text-slate-900">Configuration</h2>

    <div class="mb-4 rounded-md border border-slate-100 bg-slate-50 p-3">
      <label class="mb-1 block text-xs font-medium text-slate-600">Max Stat Budget</label>
      <div class="flex items-center gap-2">
        <input
          v-model.number="maxStatBudgetInput"
          type="number"
          step="1"
          min="0"
          class="w-24 rounded-md border border-slate-300 px-2 py-1 text-sm"
        />
        <button
          class="text-xs text-indigo-600 hover:underline disabled:cursor-not-allowed disabled:text-slate-300"
          :disabled="maxStatBudgetInput === null || status === 'pending'"
          @click="save"
        >
          Save
        </button>
        <span v-if="status === 'pending'" class="text-xs text-slate-400">Update requested — refreshing…</span>
      </div>
      <ErrorBanner v-if="status === 'error'" :message="errorMessage" @dismiss="status = 'idle'" />
    </div>

    <p v-if="!prettyPrinted" class="text-sm text-slate-500">No configuration set yet.</p>
    <pre v-else class="overflow-x-auto rounded-md bg-slate-50 p-3 text-xs text-slate-800">{{ prettyPrinted }}</pre>
  </div>
</template>
