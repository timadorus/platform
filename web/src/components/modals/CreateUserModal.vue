<script setup lang="ts">
import { ref } from 'vue'
import BaseModal from '@/components/common/BaseModal.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import { useUsers } from '@/composables/useUsers'

const emit = defineEmits<{ close: []; created: [id: string, name: string] }>()

const { create } = useUsers()
const name = ref('')
const submitting = ref(false)
const error = ref<string | null>(null)

async function submit() {
  if (!name.value.trim()) {
    error.value = 'Name is required.'
    return
  }
  submitting.value = true
  error.value = null
  try {
    const trimmed = name.value.trim()
    const id = await create(trimmed)
    emit('created', id, trimmed)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to create User.'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <BaseModal title="Create User" @close="emit('close')">
    <ErrorBanner :message="error" @dismiss="error = null" />
    <form class="space-y-3" @submit.prevent="submit">
      <div>
        <label class="mb-1 block text-xs font-medium text-slate-600">Name</label>
        <input v-model="name" type="text" class="w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
      </div>
      <div class="flex justify-end gap-2 pt-2">
        <BaseButton variant="secondary" type="button" @click="emit('close')">Cancel</BaseButton>
        <BaseButton type="submit" :disabled="submitting">{{ submitting ? 'Creating…' : 'Create' }}</BaseButton>
      </div>
    </form>
  </BaseModal>
</template>
