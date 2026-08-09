<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useUniverses } from '@/composables/useUniverses'
import { useSelectionStore } from '@/stores/selection'
import AggregatePickerGrid from '@/components/pickers/AggregatePickerGrid.vue'
import CreateUniverseModal from '@/components/modals/CreateUniverseModal.vue'
import ErrorBanner from '@/components/common/ErrorBanner.vue'

const router = useRouter()
const selection = useSelectionStore()
const { universes, error, list, get } = useUniverses()
const showCreate = ref(false)
const checkingStoredSelection = ref(true)

function goTo(universeId: string) {
  selection.setUniverse(universeId)
  router.push({ name: 'campaign-picker', params: { universeId } })
}

onMounted(async () => {
  selection.load()
  if (selection.selectedUniverseId) {
    const existing = await get(selection.selectedUniverseId)
    if (existing && !existing.isArchived) {
      // Deliberately do NOT set checkingStoredSelection = false here: this component is
      // about to unmount as router.push navigates away, and flipping it first would flash
      // the (still empty, list() never ran) picker grid for a frame before the navigation
      // completes.
      goTo(existing.id)
      return
    }
    selection.clearUniverse()
  }
  checkingStoredSelection.value = false
  await list()
})

function onCreated(id: string) {
  showCreate.value = false
  goTo(id)
}
</script>

<template>
  <div v-if="checkingStoredSelection" class="p-6 text-sm text-slate-500">Loading…</div>
  <div v-else class="mx-auto max-w-2xl p-6">
    <h1 class="mb-4 text-lg font-semibold text-slate-900">Choose a Universe</h1>
    <ErrorBanner :message="error" @dismiss="error = null" />
    <AggregatePickerGrid
      :items="universes.map((u) => ({ id: u.id, name: u.name }))"
      create-label="Create Universe"
      @select="goTo"
      @create="showCreate = true"
    />
    <CreateUniverseModal v-if="showCreate" @close="showCreate = false" @created="onCreated" />
  </div>
</template>
