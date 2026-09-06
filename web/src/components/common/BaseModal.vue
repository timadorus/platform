<script setup lang="ts">
import { useId } from 'vue'

defineProps<{ title: string }>()
const emit = defineEmits<{ close: [] }>()
const titleId = useId()
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" @click.self="emit('close')">
    <div
      class="w-full max-w-md rounded-lg bg-white p-5 shadow-xl"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="titleId"
    >
      <div class="mb-4 flex items-center justify-between">
        <h2 :id="titleId" class="text-base font-semibold text-slate-900">{{ title }}</h2>
        <button class="text-slate-400 hover:text-slate-600" @click="emit('close')" aria-label="Close">✕</button>
      </div>
      <slot />
    </div>
  </div>
</template>
