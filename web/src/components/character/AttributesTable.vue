<script setup lang="ts">
import { ATTRIBUTES } from '@/lib/attributes'

const props = defineProps<{
  attributes: Record<string, { temp: number; pot: number; bonus: number }>
}>()

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
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4" data-testid="attributes-card">
    <h2 class="mb-3 text-sm font-semibold text-slate-900">Attributes</h2>
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
  </div>
</template>
