<script setup lang="ts">
import { computed, ref } from 'vue'
import BaseModal from '@/components/common/BaseModal.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import { ATTRIBUTES, potCost } from '@/lib/attributes'

const props = defineProps<{
  attributes: Record<string, { temp: number; pot: number; bonus: number }>
  statBudget: number
  status: 'idle' | 'pending' | 'error'
  errorMessage: string | null
}>()
const emit = defineEmits<{
  cancel: []
  submit: [pot: Record<string, number>]
  dismissError: []
}>()

// Snapshot taken once, when this component is created — AttributesTable.vue's own `v-if` means a
// fresh instance is created every time the modal opens, and this deliberately does NOT resync if
// `attributes` changes again while the modal stays open (a short-lived draft, like
// `nameDraft`/`traitToAdd` elsewhere in this codebase, not a persistent field needing the fuller
// resync ConfigurationPanel.vue's Max Stat Budget input needed). `initialPot[abbr]` is both the
// floor a target can't drop below and the baseline potCost measures increases from — see
// docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md Decision 2.
const initialPot: Record<string, number> = {}
for (const { abbr } of ATTRIBUTES) {
  initialPot[abbr] = props.attributes[abbr]?.pot ?? 0
}

// The current draft value per attribute, and the last value known valid — reverted to on an
// invalid blur (see onBlur), not always back to initialPot, so a run of valid edits followed by
// one invalid one only reverts the invalid one.
const draft = ref<Record<string, number>>({ ...initialPot })
const lastValid = ref<Record<string, number>>({ ...initialPot })

const totalSpent = computed(() =>
  ATTRIBUTES.reduce((sum, { abbr }) => sum + potCost(initialPot[abbr], draft.value[abbr]), 0),
)
const budgetRemaining = computed(() => props.statBudget - totalSpent.value)

// Runs when a Pot input loses focus. By this point v-model.number has already written the
// just-typed value into draft.value[abbr], so totalSpent (above) already reflects it — a budget
// violation is exactly totalSpent exceeding statBudget, no separate "cost of just this field"
// computation needed.
function onBlur(abbr: string) {
  const value = draft.value[abbr]
  const floor = initialPot[abbr]
  const valid = Number.isInteger(value) && value >= floor && value <= 100 && totalSpent.value <= props.statBudget
  if (!valid) {
    draft.value[abbr] = lastValid.value[abbr]
  } else {
    lastValid.value[abbr] = value
  }
}

function onSubmit() {
  emit('submit', { ...draft.value })
}
</script>

<template>
  <BaseModal title="Assign Stats Budget" @close="emit('cancel')">
    <table class="mb-4 w-full text-sm">
      <thead>
        <tr class="border-b border-slate-100 text-left text-xs font-medium text-slate-500">
          <th class="pb-1.5">Attribute</th>
          <th class="pb-1.5">Abbr</th>
          <th class="pb-1.5">Pot</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="a in ATTRIBUTES" :key="a.abbr" class="border-b border-slate-50 last:border-0">
          <td class="py-1.5 text-slate-900">{{ a.name }}</td>
          <td class="py-1.5 text-slate-500">{{ a.abbr }}</td>
          <td class="py-1.5">
            <input
              v-model.number="draft[a.abbr]"
              type="number"
              step="1"
              class="w-20 rounded-md border border-slate-300 px-2 py-1 text-sm"
              :disabled="status === 'pending'"
              :data-testid="`assign-pot-${a.abbr}`"
              @blur="onBlur(a.abbr)"
            />
          </td>
        </tr>
      </tbody>
    </table>

    <div class="mb-4 rounded-md border border-slate-100 bg-slate-50 p-3 text-xs text-slate-600">
      <p class="mb-1 font-medium text-slate-900" data-testid="budget-remaining">Budget remaining: {{ budgetRemaining }}</p>
      <p>Set potential values. Pot &le; 90 equals 1 budget point per attribute point. 91-100 cost 5 budget points per attribute point.</p>
    </div>

    <span v-if="status === 'pending'" class="mb-3 block text-xs text-slate-400">Update requested — refreshing…</span>
    <ErrorBanner v-if="status === 'error'" :message="errorMessage" @dismiss="emit('dismissError')" />

    <div class="flex justify-end gap-2">
      <BaseButton variant="secondary" :disabled="status === 'pending'" @click="emit('cancel')">Cancel</BaseButton>
      <BaseButton :disabled="status === 'pending'" @click="onSubmit">Submit</BaseButton>
    </div>
  </BaseModal>
</template>
