<script setup lang="ts">
import { inject, onMounted, onUnmounted, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useEntities } from '@/composables/useEntities'
import SearchableAggregatePanel from './SearchableAggregatePanel.vue'
import CreateEntityModal from '@/components/modals/CreateEntityModal.vue'
import AdvancedSearchStubModal from './AdvancedSearchStubModal.vue'
import type { AggregateChange } from '@/composables/useChangeFeed'

const props = defineProps<{ universeId: string }>()
const route = useRoute()
const router = useRouter()

const { entities, loading, search, waitForEntityInList } = useEntities()
const showCreate = ref(false)
const showAdvanced = ref(false)
const currentQuery = ref('')
// Holds the AbortController for the current pendingEntityId poll (if any), so it can be aborted
// both on unmount and as soon as the user starts typing a search (see onSearch below) — mirrors
// CreatingUserModal.vue's pattern for waitForUser.
let pendingEntityController: AbortController | null = null

function onSearch(query: string) {
  currentQuery.value = query
  // The user has taken over the search box — stop the background poll from clobbering their
  // filter with an unfiltered search every 750ms; let their search win.
  if (query) pendingEntityController?.abort()
  search(props.universeId, query)
}
function select(id: string) {
  router.push({ name: 'entity-detail', params: { ...route.params, entityId: id } })
}
function onCreated() {
  showCreate.value = false
  search(props.universeId, currentQuery.value)
}

onMounted(() => search(props.universeId, ''))

const sidebarRefreshSignal = inject<Ref<number>>('sidebarRefreshSignal')
if (sidebarRefreshSignal) {
  watch(sidebarRefreshSignal, () => {
    search(props.universeId, currentQuery.value)
  })
}

// pendingEntityId (WorkspaceView.vue) names an Entity that was just auto-created elsewhere (via
// Character creation) and might not be visible yet — poll for it specifically, rather than
// relying on the generic sidebarRefreshSignal above, which only re-searches once with no retry.
const pendingEntityId = inject<Ref<string | null>>('pendingEntityId')
if (pendingEntityId) {
  watch(pendingEntityId, async (id) => {
    if (!id) return
    pendingEntityController = new AbortController()
    await waitForEntityInList(props.universeId, id, { signal: pendingEntityController.signal })
    pendingEntityId.value = null
  })
}

onUnmounted(() => pendingEntityController?.abort())

const lastAggregateChange = inject<Ref<AggregateChange | null>>('lastAggregateChange')
if (lastAggregateChange) {
  watch(lastAggregateChange, (change) => {
    if (change?.aggregateType === 'entity') search(props.universeId, currentQuery.value)
  })
}
</script>

<template>
  <div>
    <SearchableAggregatePanel
      label="Entities"
      :items="entities"
      :loading="loading"
      @search="onSearch"
      @select="select"
      @create="showCreate = true"
      @advanced-search="showAdvanced = true"
    />
    <CreateEntityModal v-if="showCreate" :universe-id="universeId" @close="showCreate = false" @created="onCreated" />
    <AdvancedSearchStubModal v-if="showAdvanced" label="Entities" @close="showAdvanced = false" />
  </div>
</template>
