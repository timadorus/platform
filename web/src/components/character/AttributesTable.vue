<script setup lang="ts">
// Attribute values/bonuses are placeholders: no backend field exists yet for Character
// attributes. Every attribute reads 75/+0 until a real data source is wired up.
const attributes = [
  { name: 'Strength', abbr: 'ST' },
  { name: 'Agility', abbr: 'AG' },
  { name: 'Constitution', abbr: 'CO' },
  { name: 'Quickness', abbr: 'QU' },
  { name: 'Self Discipline', abbr: 'SD' },
  { name: 'Memory', abbr: 'ME' },
  { name: 'Reasoning', abbr: 'RE' },
  { name: 'Empathy', abbr: 'EM' },
  { name: 'Presence', abbr: 'PR' },
  { name: 'Intuition', abbr: 'IN' },
].map((a) => ({ ...a, value: 75, bonus: '+0' }))
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4">
    <h2 class="mb-3 text-sm font-semibold text-slate-900">Attributes</h2>
    <table class="w-full text-sm">
      <thead>
        <tr class="border-b border-slate-100 text-left text-xs font-medium text-slate-500">
          <th class="pb-1.5">Attribute</th>
          <th class="pb-1.5">Abbr</th>
          <th class="pb-1.5">Value</th>
          <th class="pb-1.5">Bonus</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="a in attributes" :key="a.abbr" class="border-b border-slate-50 last:border-0">
          <td class="py-1.5 text-slate-900">{{ a.name }}</td>
          <td class="py-1.5 text-slate-500">{{ a.abbr }}</td>
          <td class="py-1.5 text-slate-900">{{ a.value }}</td>
          <td class="py-1.5 text-slate-900">{{ a.bonus }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
