<script setup lang="ts">
import BaseModal from '@/components/common/BaseModal.vue'
import BaseButton from '@/components/common/BaseButton.vue'

defineProps<{ label: string }>()
const emit = defineEmits<{ close: [] }>()
</script>

<template>
  <BaseModal :title="`Advanced ${label} Search`" @close="emit('close')">
    <p class="mb-4 text-sm text-slate-500">
      Detailed search is coming soon — the exact criteria are still being designed. For now, use
      the quick search box to find {{ label.toLowerCase() }} by name.
    </p>
    <div class="flex justify-end">
      <BaseButton variant="secondary" @click="emit('close')">Close</BaseButton>
    </div>
  </BaseModal>
</template>
