<script setup lang="ts">
import { onUnmounted, ref, watch } from 'vue'
import { ATTRIBUTES } from '@/lib/attributes'
import { useCharacters } from '@/composables/useCharacters'
import AssignStatsBudgetModal from './AssignStatsBudgetModal.vue'

const props = defineProps<{
  attributes: Record<string, { temp: number; pot: number; bonus: number }>
  characterId: string
  statBudget: number
}>()
const { requestAction } = useCharacters()

// '—' distinguishes "not yet seeded" (non-Timadorus Character, or the engine hasn't caught up
// right after creation — the same async-settling window traits/traitPoints already tolerate)
// from a genuinely-zero value.
function cell(abbr: string, field: 'temp' | 'pot'): string {
  const value = props.attributes[abbr]?.[field]
  return value === undefined ? '—' : String(value)
}

function bonusCell(abbr: string): string {
  const value = props.attributes[abbr]?.bonus
  if (value === undefined) return '—'
  return value >= 0 ? `+${value}` : String(value)
}

const showAssignModal = ref(false)
function openAssignModal() {
  // Reset any stale error/pending state from a previous attempt before showing a fresh modal —
  // otherwise reopening after a timed-out submit would show its old error banner immediately.
  submitPotStatus.value = 'idle'
  submitPotError.value = null
  showAssignModal.value = true
}

type SubmitPotStatus = 'idle' | 'pending' | 'error'
const submitPotStatus = ref<SubmitPotStatus>('idle')
const submitPotError = ref<string | null>(null)
// The batch most recently submitted, so the watch below can tell "the loaded attributes now
// reflect my own request" apart from "someone else changed something unrelated".
let pendingPot: Record<string, number> | null = null

// A rejected submitPot (insufficient budget, an out-of-range target, an unknown abbreviation, or
// an archived Character) is a clean, logged engine-side no-op — no event distinguishes "rejected"
// from "still processing," so this timeout guarantees the modal always resolves, exactly like
// BaseInfoTable.vue's ADD_TRAIT_TIMEOUT_MS for Add Trait.
const SUBMIT_POT_TIMEOUT_MS = 10000
let submitPotTimeoutHandle: ReturnType<typeof setTimeout> | null = null
function clearSubmitPotTimeout() {
  if (submitPotTimeoutHandle) {
    clearTimeout(submitPotTimeoutHandle)
    submitPotTimeoutHandle = null
  }
}

async function onSubmitPot(pot: Record<string, number>) {
  submitPotStatus.value = 'pending'
  submitPotError.value = null
  pendingPot = pot
  clearSubmitPotTimeout()
  submitPotTimeoutHandle = setTimeout(() => {
    if (submitPotStatus.value === 'pending') {
      submitPotStatus.value = 'error'
      submitPotError.value = 'No confirmation received — this change may not have been applied.'
      pendingPot = null
    }
  }, SUBMIT_POT_TIMEOUT_MS)
  try {
    await requestAction(props.characterId, { action: 'submitPot', pot })
  } catch (err) {
    clearSubmitPotTimeout()
    submitPotStatus.value = 'error'
    submitPotError.value = err instanceof Error ? err.message : 'Failed to request the Pot change.'
  }
}

// Closes the modal only once the loaded attributes actually reflect every submitted target — a
// batch submitPot rejected by the engine leaves attributes unchanged, so this deliberately never
// fires for a rejection (the timeout above handles that case instead).
watch(
  () => props.attributes,
  (attributes) => {
    if (
      submitPotStatus.value === 'pending' &&
      pendingPot !== null &&
      Object.entries(pendingPot).every(([abbr, target]) => attributes[abbr]?.pot === target)
    ) {
      clearSubmitPotTimeout()
      submitPotStatus.value = 'idle'
      pendingPot = null
      showAssignModal.value = false
    }
  },
)

onUnmounted(clearSubmitPotTimeout)
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4" data-testid="attributes-card">
    <div class="mb-3 flex items-center justify-between">
      <h2 class="text-sm font-semibold text-slate-900">Attributes</h2>
      <button v-if="statBudget > 0" class="text-xs text-indigo-600 hover:underline" @click="openAssignModal">
        Assign Stats Budget
      </button>
    </div>
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b border-slate-100 text-left text-xs font-medium text-slate-500">
          <th class="pb-1.5">Attribute</th>
          <th class="pb-1.5">Abbr</th>
          <th class="pb-1.5">Temp</th>
          <th class="pb-1.5">Pot</th>
          <th class="pb-1.5">Bonus</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="a in ATTRIBUTES" :key="a.abbr" class="border-b border-slate-50 last:border-0">
          <td class="py-1.5 text-slate-900">{{ a.name }}</td>
          <td class="py-1.5 text-slate-500">{{ a.abbr }}</td>
          <td class="py-1.5 text-slate-900" :data-testid="`attribute-${a.abbr}-temp`">{{ cell(a.abbr, 'temp') }}</td>
          <td class="py-1.5 text-slate-900" :data-testid="`attribute-${a.abbr}-pot`">{{ cell(a.abbr, 'pot') }}</td>
          <td class="py-1.5 text-slate-900" :data-testid="`attribute-${a.abbr}-bonus`">{{ bonusCell(a.abbr) }}</td>
        </tr>
      </tbody>
    </table>
    <AssignStatsBudgetModal
      v-if="showAssignModal"
      :attributes="attributes"
      :stat-budget="statBudget"
      :status="submitPotStatus"
      :error-message="submitPotError"
      @cancel="showAssignModal = false"
      @submit="onSubmitPot"
      @dismiss-error="submitPotStatus = 'idle'"
    />
  </div>
</template>
