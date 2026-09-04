<script setup lang="ts">
import { computed } from 'vue'

const props = defineProps<{ info: string }>()

// The Info field is an opaque JSON string set via PUT /characters/{id}/info — it starts
// unset/empty until that endpoint is called at least once, which is not valid JSON, so
// pretty-printing is attempted defensively rather than assumed to always succeed. Mirrors
// ConfigurationPanel.vue's (Campaign's own equivalent field) identical pattern.
const prettyPrinted = computed(() => {
  if (!props.info) return null
  try {
    return JSON.stringify(JSON.parse(props.info), null, 2)
  } catch {
    return props.info
  }
})
</script>

<template>
  <div class="rounded-md border border-slate-200 p-4">
    <h2 class="mb-3 text-sm font-semibold text-slate-900">Configuration</h2>
    <p v-if="!prettyPrinted" class="text-sm text-slate-500">No configuration set yet.</p>
    <pre v-else class="overflow-x-auto rounded-md bg-slate-50 p-3 text-xs text-slate-800">{{ prettyPrinted }}</pre>
  </div>
</template>
