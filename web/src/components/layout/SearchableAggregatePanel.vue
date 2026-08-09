<script setup lang="ts">
import { ref, watch } from 'vue'

defineProps<{
  label: string
  items: { id: string; name: string }[]
  loading: boolean
}>()
const emit = defineEmits<{
  search: [query: string]
  select: [id: string]
  create: []
  'advanced-search': []
}>()

const query = ref('')
let debounceTimer: ReturnType<typeof setTimeout> | undefined
watch(query, (value) => {
  clearTimeout(debounceTimer)
  debounceTimer = setTimeout(() => emit('search', value.trim()), 250)
})
</script>

<template>
  <div>
    <input
      v-model="query"
      type="text"
      :placeholder="`Search ${label.toLowerCase()}…`"
      class="mb-1 w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm"
    />
    <div class="mb-1 flex items-center justify-between">
      <button class="text-xs text-slate-500 hover:text-indigo-600 hover:underline" @click="emit('create')">+ Create</button>
      <button class="text-xs text-slate-500 hover:text-indigo-600 hover:underline" @click="emit('advanced-search')">
        ⚙ Advanced search…
      </button>
    </div>
    <div class="max-h-48 overflow-y-auto rounded-md border border-slate-200">
      <button
        v-for="item in items"
        :key="item.id"
        class="block w-full px-2 py-1.5 text-left text-sm hover:bg-slate-50"
        @click="emit('select', item.id)"
      >
        {{ item.name }}
      </button>
      <p v-if="!loading && items.length === 0" class="px-2 py-1.5 text-xs text-slate-400">No matches — try a different search.</p>
    </div>
  </div>
</template>
