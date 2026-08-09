<script setup lang="ts">
defineProps<{ title: string }>()
const emit = defineEmits<{ close: [] }>()
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" @click.self="emit('close')">
    <div class="w-full max-w-md rounded-lg bg-white p-5 shadow-xl">
      <div class="mb-4 flex items-center justify-between">
        <h2 class="text-base font-semibold text-slate-900">{{ title }}</h2>
        <button class="text-slate-400 hover:text-slate-600" @click="emit('close')" aria-label="Close">✕</button>
      </div>
      <slot />
    </div>
  </div>
</template>
