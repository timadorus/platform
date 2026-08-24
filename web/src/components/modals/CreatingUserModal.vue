<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'
import BaseModal from '@/components/common/BaseModal.vue'
import BaseButton from '@/components/common/BaseButton.vue'
import { useUsers } from '@/composables/useUsers'

const props = defineProps<{ userId: string; userName: string }>()
const emit = defineEmits<{ done: []; close: [] }>()

const { waitForUser, error: fetchError } = useUsers()
const timedOut = ref(false)
const controller = new AbortController()

onMounted(async () => {
  const found = await waitForUser(props.userId, { signal: controller.signal })
  if (controller.signal.aborted) return // unmounted while waiting — nothing left to update
  if (found) {
    emit('done')
  } else {
    timedOut.value = true
  }
})

// Stops the poll (and its pending setTimeout) rather than let it keep running against an
// unmounted component — see waitForUser's own doc comment for why this is safe either way.
onUnmounted(() => controller.abort())
</script>

<template>
  <BaseModal title="Creating User" @close="emit('close')">
    <p v-if="!timedOut" class="text-sm text-slate-600">Creating "{{ userName }}"…</p>
    <template v-else>
      <p v-if="fetchError" class="mb-4 text-sm text-red-800">{{ fetchError }}</p>
      <p v-else class="mb-4 text-sm text-slate-600">
        Still waiting for "{{ userName }}" to appear — this is taking longer than expected.
      </p>
      <div class="flex justify-end">
        <BaseButton variant="secondary" @click="emit('close')">Close</BaseButton>
      </div>
    </template>
  </BaseModal>
</template>
