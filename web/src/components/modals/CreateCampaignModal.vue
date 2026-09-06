<script setup lang="ts">
import { ref } from 'vue'
import BaseModal from '@/components/common/BaseModal.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import UserMultiSelect from '@/components/pickers/UserMultiSelect.vue'
import RulesetSelect from '@/components/pickers/RulesetSelect.vue'
import { useCampaigns } from '@/composables/useCampaigns'

const props = defineProps<{ universeId: string }>()
const emit = defineEmits<{ close: []; created: [id: string] }>()

const { create } = useCampaigns()
const name = ref('')
const rulesetId = ref('')
const gamemasterUserIds = ref<string[]>([])
const submitting = ref(false)
const error = ref<string | null>(null)

async function submit() {
  if (!name.value.trim() || !rulesetId.value || gamemasterUserIds.value.length === 0) {
    error.value = 'Name, a Ruleset, and at least one Gamemaster are required.'
    return
  }
  submitting.value = true
  error.value = null
  try {
    const id = await create(props.universeId, name.value.trim(), rulesetId.value, gamemasterUserIds.value)
    emit('created', id)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to create Campaign.'
  } finally {
    submitting.value = false
  }
}
</script>

<template>
  <BaseModal title="Create Campaign" @close="emit('close')">
    <ErrorBanner :message="error" @dismiss="error = null" />
    <form class="space-y-3" @submit.prevent="submit">
      <div>
        <label for="campaign-name" class="mb-1 block text-xs font-medium text-slate-600">Name</label>
        <input id="campaign-name" v-model="name" type="text" class="w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
      </div>
      <div>
        <label for="campaign-ruleset" class="mb-1 block text-xs font-medium text-slate-600">Ruleset</label>
        <RulesetSelect id="campaign-ruleset" v-model="rulesetId" />
      </div>
      <div>
        <label id="campaign-gamemasters-label" class="mb-1 block text-xs font-medium text-slate-600">Gamemasters (at least one)</label>
        <UserMultiSelect labelledby="campaign-gamemasters-label" v-model="gamemasterUserIds" />
      </div>
      <div class="flex justify-end gap-2 pt-2">
        <BaseButton variant="secondary" type="button" @click="emit('close')">Cancel</BaseButton>
        <BaseButton type="submit" :disabled="submitting">{{ submitting ? 'Creating…' : 'Create' }}</BaseButton>
      </div>
    </form>
  </BaseModal>
</template>
