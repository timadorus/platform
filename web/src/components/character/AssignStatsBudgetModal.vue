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

// The highest Pot value this modal will let a player reach — lower than the engine's own
// absolute cap of 100 (internal/engine/timadorus/character_processor.go). Deliberately a
// UI-only restriction: the engine still accepts up to 100 from any other caller, but this modal
// never asks for more than 95, matching the explanation text below and the native `max` attribute
// on each input (which is what makes the browser's own spinner arrows/arrow-key stepping respect
// it too, not just the blur-time check here).
const POT_CEILING = 95

// Runs when a Pot input loses focus. By this point v-model.number has already written the
// just-typed value into draft.value[abbr], so totalSpent (above) already reflects it — a budget
// violation is exactly totalSpent exceeding statBudget, no separate "cost of just this field"
// computation needed.
function onBlur(abbr: string) {
  const value = draft.value[abbr]
  const floor = initialPot[abbr]
  const valid =
    Number.isInteger(value) && value >= floor && value <= POT_CEILING && totalSpent.value <= props.statBudget
  if (!valid) {
    draft.value[abbr] = lastValid.value[abbr]
  } else {
    lastValid.value[abbr] = value
  }
}

function onSubmit() {
  emit('submit', { ...draft.value })
}

// Refs to each Pot input, keyed by abbreviation, so Enter can move focus to the next one in
// ATTRIBUTES order — populated via the template's function-ref binding below.
const inputRefs: Partial<Record<string, HTMLInputElement>> = {}
function setInputRef(abbr: string, el: Element | null) {
  inputRefs[abbr] = (el as HTMLInputElement) ?? undefined
}

// Moving focus away from the current input fires its own @blur first (native browser behavior),
// so the field being left is validated/reverted exactly as it would be on a Tab or a mouse click
// elsewhere — Enter doesn't need to duplicate that check. Wraps from Intuition (the last entry in
// ATTRIBUTES) back to Strength (the first).
function focusNext(abbr: string) {
  const index = ATTRIBUTES.findIndex((a) => a.abbr === abbr)
  const next = ATTRIBUTES[(index + 1) % ATTRIBUTES.length]
  inputRefs[next.abbr]?.focus()
}

// BaseModal fires `close` from both its ✕ button and a backdrop click, neither of which goes
// through the Cancel BaseButton below (the only place `:disabled="status === 'pending'"` is
// otherwise checked) — without this guard a user could dismiss the modal via ✕/backdrop while a
// submit is in flight, and the eventual timeout error would set state on a component that no
// longer exists to show it (ErrorBanner only renders inside this modal). Design spec Decision 5
// requires Cancel to be disabled during a pending submit; this makes ✕/backdrop obey the same
// rule instead of bypassing it.
function onClose() {
  if (props.status !== 'pending') emit('cancel')
}
</script>

<template>
  <BaseModal title="Assign Stats Budget" @close="onClose">
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
              :ref="(el) => setInputRef(a.abbr, el as Element | null)"
              v-model.number="draft[a.abbr]"
              type="number"
              step="1"
              :min="initialPot[a.abbr]"
              :max="POT_CEILING"
              class="w-20 rounded-md border border-slate-300 px-2 py-1 text-sm"
              :disabled="status === 'pending'"
              :data-testid="`assign-pot-${a.abbr}`"
              @blur="onBlur(a.abbr)"
              @keydown.enter.prevent="focusNext(a.abbr)"
            />
          </td>
        </tr>
      </tbody>
    </table>

    <div class="mb-4 rounded-md border border-slate-100 bg-slate-50 p-3 text-xs text-slate-600">
      <p class="mb-1 font-medium text-slate-900" data-testid="budget-remaining">Points remaining: {{ budgetRemaining }}</p>
      <p>Set potential values. Pot &le; 90 equals 1 budget point per attribute point. 91-95 cost 5 budget points per attribute point.</p>
    </div>

    <span v-if="status === 'pending'" class="mb-3 block text-xs text-slate-400">Update requested — refreshing…</span>
    <ErrorBanner v-if="status === 'error'" :message="errorMessage" @dismiss="emit('dismissError')" />

    <div class="flex justify-end gap-2">
      <BaseButton variant="secondary" :disabled="status === 'pending'" @click="onClose">Cancel</BaseButton>
      <BaseButton :disabled="status === 'pending'" @click="onSubmit">Submit</BaseButton>
    </div>
  </BaseModal>
</template>
