<script setup lang="ts">
defineProps<{ message: string | null }>()
const emit = defineEmits<{ dismiss: [] }>()
</script>

<template>
  <div
    v-if="message"
    class="mb-3 flex items-start justify-between gap-3 rounded-md bg-red-50 px-3 py-2 text-sm text-red-800"
  >
    <span>{{ message }}</span>
    <button class="text-red-500 hover:text-red-700" @click="emit('dismiss')" aria-label="Dismiss">✕</button>
  </div>
</template>
