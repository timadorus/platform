<script setup lang="ts">
defineProps<{
  character: { id: string; name: string; isArchived: boolean }
  playerLabel: string
}>()
</script>

<template>
  <button class="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-slate-50">
    <span class="h-6 w-6 flex-shrink-0 rounded-full bg-slate-200"></span>
    <span class="min-w-0 flex-1">
      <span class="block truncate text-sm text-slate-900">{{ character.name }}</span>
      <span class="block truncate text-xs text-slate-500">{{ playerLabel }}</span>
    </span>
  </button>
</template>
