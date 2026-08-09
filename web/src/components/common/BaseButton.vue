<script setup lang="ts">
withDefaults(
  defineProps<{
    variant?: 'primary' | 'secondary' | 'danger'
    type?: 'button' | 'submit'
    disabled?: boolean
  }>(),
  { variant: 'primary', type: 'button', disabled: false },
)
</script>

<template>
  <button
    :type="type"
    :disabled="disabled"
    class="rounded-md px-3 py-1.5 text-sm font-medium disabled:opacity-50 disabled:cursor-not-allowed"
    :class="{
      'bg-indigo-600 text-white hover:bg-indigo-500': variant === 'primary',
      'bg-white text-slate-700 border border-slate-300 hover:bg-slate-50': variant === 'secondary',
      'bg-red-600 text-white hover:bg-red-500': variant === 'danger',
    }"
  >
    <slot />
  </button>
</template>
