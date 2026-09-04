<script setup lang="ts">
import { computed, inject, onMounted, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useEntities, type EntitySummary } from '@/composables/useEntities'
import type { AggregateChange } from '@/composables/useChangeFeed'
import BaseButton from '@/components/common/BaseButton.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'

const route = useRoute()
const router = useRouter()
// computed, not a plain const: vue-router reuses this component instance across param-only
// route changes on the same route record (e.g. picking a different Entity from the sidebar
// search results while already viewing one) — a plain const captured once would go stale.
const entityId = computed(() => route.params.entityId as string)

const { get, rename, archive } = useEntities()
const bumpSidebarRefresh = inject<() => void>('bumpSidebarRefresh')
const entity = ref<EntitySummary | null>(null)
const name = ref('')
const error = ref<string | null>(null)
const showArchiveConfirm = ref(false)

async function load() {
  entity.value = await get(entityId.value)
  if (entity.value) name.value = entity.value.name
}
onMounted(load)
watch(entityId, load)

const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'entity' && change.aggregateId.toLowerCase() === entityId.value.toLowerCase()) load()
  })
}

async function submitRename() {
  if (!entity.value) return
  error.value = null
  try {
    await rename(entity.value.id, name.value.trim())
    entity.value.name = name.value.trim()
    bumpSidebarRefresh?.()
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to rename.'
  }
}

async function confirmArchive() {
  if (!entity.value) return
  showArchiveConfirm.value = false
  error.value = null
  try {
    await archive(entity.value.id)
    bumpSidebarRefresh?.()
    router.push({ name: 'campaign-overview', params: { universeId: route.params.universeId, campaignId: route.params.campaignId } })
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Failed to archive.'
  }
}
</script>

<template>
  <div v-if="entity" class="mx-auto max-w-lg p-6">
    <ErrorBanner :message="error" @dismiss="error = null" />
    <div class="mb-4 flex gap-2">
      <input v-model="name" type="text" class="flex-1 rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
      <BaseButton @click="submitRename">Rename</BaseButton>
    </div>
    <div class="border-t border-slate-100 pt-3">
      <BaseButton variant="danger" @click="showArchiveConfirm = true">Archive Entity</BaseButton>
    </div>
    <ConfirmDialog
      v-if="showArchiveConfirm"
      title="Archive Entity"
      message="This entity will be hidden from search. This cannot be undone through this app."
      confirm-label="Archive"
      @confirm="confirmArchive"
      @cancel="showArchiveConfirm = false"
    />
  </div>
  <div v-else class="p-6 text-sm text-slate-500">Loading…</div>
</template>
