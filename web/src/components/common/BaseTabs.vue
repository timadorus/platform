<script setup lang="ts">
defineProps<{ tabs: string[]; modelValue: string }>()
const emit = defineEmits<{ 'update:modelValue': [tab: string] }>()
</script>

<template>
  <div class="border-b border-slate-200">
    <nav class="-mb-px flex gap-4" role="tablist">
      <button
        v-for="tab in tabs"
        :key="tab"
        type="button"
        role="tab"
        :aria-selected="tab === modelValue"
        class="border-b-2 px-1 py-2 text-sm font-medium"
        :class="
          tab === modelValue
            ? 'border-indigo-600 text-indigo-600'
            : 'border-transparent text-slate-500 hover:border-slate-300 hover:text-slate-700'
        "
        @click="emit('update:modelValue', tab)"
      >
        {{ tab }}
      </button>
    </nav>
  </div>
</template>
