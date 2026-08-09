<script setup lang="ts">
import BaseModal from './BaseModal.vue'
import BaseButton from './BaseButton.vue'

withDefaults(defineProps<{ title: string; message: string; confirmLabel?: string }>(), {
  confirmLabel: 'Confirm',
})
const emit = defineEmits<{ confirm: []; cancel: [] }>()
</script>

<template>
  <BaseModal :title="title" @close="emit('cancel')">
    <p class="mb-4 text-sm text-slate-600">{{ message }}</p>
    <div class="flex justify-end gap-2">
      <BaseButton variant="secondary" @click="emit('cancel')">Cancel</BaseButton>
      <BaseButton variant="danger" @click="emit('confirm')">{{ confirmLabel }}</BaseButton>
    </div>
  </BaseModal>
</template>
