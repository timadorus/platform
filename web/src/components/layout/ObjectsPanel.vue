<script setup lang="ts">
import { inject, onMounted, ref, watch, type Ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useObjects } from '@/composables/useObjects'
import SearchableAggregatePanel from './SearchableAggregatePanel.vue'
import CreateObjectModal from '@/components/modals/CreateObjectModal.vue'
import AdvancedSearchStubModal from './AdvancedSearchStubModal.vue'

const props = defineProps<{ universeId: string }>()
const route = useRoute()
const router = useRouter()

const { objects, loading, search } = useObjects()
const showCreate = ref(false)
const showAdvanced = ref(false)
const currentQuery = ref('')

function onSearch(query: string) {
  currentQuery.value = query
  search(props.universeId, query)
}
function select(id: string) {
  router.push({ name: 'object-detail', params: { ...route.params, objectId: id } })
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
</script>

<template>
  <div>
    <SearchableAggregatePanel
      label="Objects"
      :items="objects"
      :loading="loading"
      @search="onSearch"
      @select="select"
      @create="showCreate = true"
      @advanced-search="showAdvanced = true"
    />
    <CreateObjectModal v-if="showCreate" :universe-id="universeId" @close="showCreate = false" @created="onCreated" />
    <AdvancedSearchStubModal v-if="showAdvanced" label="Objects" @close="showAdvanced = false" />
  </div>
</template>
