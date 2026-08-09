<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import BaseModal from '@/components/common/BaseModal.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import UserPicker from '@/components/pickers/UserPicker.vue'
import { useCharacters } from '@/composables/useCharacters'
import { useUsers } from '@/composables/useUsers'

const props = defineProps<{ campaignId: string }>()
const emit = defineEmits<{ close: []; created: [] }>()

const { create } = useCharacters()
const { users, list: listUsers } = useUsers()
onMounted(listUsers)

const name = ref('')
const playerUserId = ref<string | null>(null)
const submitting = ref(false)
const error = ref<string | null>(null)

const playerName = computed(() => users.value.find((u) => u.id === playerUserId.value)?.name ?? '')

async function submit() {
  if (!name.value.trim() || !playerUserId.value) {
    error.value = 'Name and a Player are required.'
    return
  }
  submitting.value = true
  error.value = null
  try {
    await create(props.campaignId, name.value.trim(), playerUserId.value)
    emit('created')
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to create Character.'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <BaseModal title="Create Character" @close="emit('close')">
    <ErrorBanner :message="error" @dismiss="error = null" />
    <form class="space-y-3" @submit.prevent="submit">
      <div>
        <label class="mb-1 block text-xs font-medium text-slate-600">Name</label>
        <input v-model="name" type="text" class="w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
      </div>
      <div>
        <label class="mb-1 block text-xs font-medium text-slate-600">Player</label>
        <p v-if="playerName" class="mb-1 text-sm text-slate-700">Selected: {{ playerName }}</p>
        <UserPicker @select="(id) => (playerUserId = id)" />
      </div>
      <div class="flex justify-end gap-2 pt-2">
        <BaseButton variant="secondary" type="button" @click="emit('close')">Cancel</BaseButton>
        <BaseButton type="submit" :disabled="submitting">{{ submitting ? 'Creating…' : 'Create' }}</BaseButton>
      </div>
    </form>
  </BaseModal>
</template>
