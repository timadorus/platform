<script setup lang="ts">
import { computed, inject, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useObjects, type ObjectSummary } from '@/composables/useObjects'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

const route = useRoute()
const router = useRouter()
// computed, not a plain const — same reasoning as EntityDetailView.vue (Task 12): vue-router
// reuses this component instance across param-only route changes on the same route record.
const objectId = computed(() => route.params.objectId as string)

const { get, rename, archive } = useObjects()
const bumpSidebarRefresh = inject<() => void>('bumpSidebarRefresh')
const object = ref<ObjectSummary | null>(null)
const name = ref('')
const error = ref<string | null>(null)
const showArchiveConfirm = ref(false)

async function load() {
  object.value = await get(objectId.value)
  if (object.value) name.value = object.value.name
}
onMounted(load)
watch(objectId, load)

async function submitRename() {
  if (!object.value) return
  error.value = null
  try {
    await rename(object.value.id, name.value.trim())
    object.value.name = name.value.trim()
    bumpSidebarRefresh?.()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to rename.'
  }
}

async function confirmArchive() {
  if (!object.value) return
  showArchiveConfirm.value = false
  error.value = null
  try {
    await archive(object.value.id)
    bumpSidebarRefresh?.()
    router.push({ name: 'campaign-overview', params: { universeId: route.params.universeId, campaignId: route.params.campaignId } })
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  }
}
</script>

<template>
  <div v-if="object" class="mx-auto max-w-lg p-6">
    <ErrorBanner :message="error" @dismiss="error = null" />
    <div class="mb-4 flex gap-2">
      <input v-model="name" type="text" class="flex-1 rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
      <BaseButton @click="submitRename">Rename</BaseButton>
    </div>
    <div class="border-t border-slate-100 pt-3">
      <BaseButton variant="danger" @click="showArchiveConfirm = true">Archive Object</BaseButton>
    </div>
    <ConfirmDialog
      v-if="showArchiveConfirm"
      title="Archive Object"
      message="This object will be hidden from search. This cannot be undone through this app."
      confirm-label="Archive"
      @confirm="confirmArchive"
      @cancel="showArchiveConfirm = false"
    />
  </div>
  <div v-else class="p-6 text-sm text-slate-500">Loading…</div>
</template>
