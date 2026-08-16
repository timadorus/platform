<script setup lang="ts">
import { onMounted, ref } from 'vue'
import BaseModal from '@/components/common/BaseModal.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import { useUsers } from '@/composables/useUsers'

const props = defineProps<{ userId: string; userName: string }>()
const emit = defineEmits<{ done: []; close: [] }>()

const { waitForUser } = useUsers()
const timedOut = ref(false)

onMounted(async () => {
  const found = await waitForUser(props.userId)
  if (found) {
    emit('done')
  } else {
    timedOut.value = true
  }
})
</script>

<template>
  <BaseModal title="Creating User" @close="emit('close')">
    <p v-if="!timedOut" class="text-sm text-slate-600">Creating "{{ userName }}"…</p>
    <template v-else>
      <p class="mb-4 text-sm text-slate-600">
        Still waiting for "{{ userName }}" to appear — this is taking longer than expected.
      </p>
      <div class="flex justify-end">
        <BaseButton variant="secondary" @click="emit('close')">Close</BaseButton>
      </div>
    </template>
  </BaseModal>
</template>
