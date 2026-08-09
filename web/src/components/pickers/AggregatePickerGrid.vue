<script setup lang="ts">
defineProps<{
  items: { id: string; name: string; subtitle?: string }[]
  createLabel: string
}>()
const emit = defineEmits<{ select: [id: string]; create: [] }>()
</script>

<template>
  <div class="grid grid-cols-2 gap-3">
    <button
      v-for="item in items"
      :key="item.id"
      class="rounded-md border border-slate-200 bg-white p-4 text-left hover:border-indigo-400 hover:shadow-sm"
      @click="emit('select', item.id)"
    >
      <div class="font-medium text-slate-900">{{ item.name }}</div>
      <div v-if="item.subtitle" class="mt-1 text-xs text-slate-500">{{ item.subtitle }}</div>
    </button>
    <button
      class="rounded-md border border-dashed border-slate-300 p-4 text-left text-slate-500 hover:border-indigo-400 hover:text-indigo-600"
      @click="emit('create')"
    >
      + {{ createLabel }}
    </button>
  </div>
</template>
