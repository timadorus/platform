<script setup lang="ts">
import { onMounted } from 'vue'
import { useRulesets } from '@/composables/useRulesets'

defineProps<{ id?: string }>()
const modelValue = defineModel<string>({ required: true })
const { rulesets, loading, list } = useRulesets()

onMounted(list)
</script>

<template>
  <select :id="id" v-model="modelValue" class="w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm">
    <option value="" disabled>{{ loading ? 'Loading Rulesets…' : 'Select a Ruleset' }}</option>
    <option v-for="r in rulesets" :key="r.id" :value="r.id">{{ r.name }}</option>
  </select>
  <p v-if="!loading && rulesets.length === 0" class="mt-1 text-xs text-slate-400">
    No Rulesets yet — create one via <code>timadorusctl create ruleset</code>.
  </p>
</template>
