<script setup lang="ts">
import { ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { useEntities } from '@/composables/useEntities'
import SearchableAggregatePanel from './SearchableAggregatePanel.vue'
import CreateEntityModal from '@/components/modals/CreateEntityModal.vue'
import AdvancedSearchStubModal from './AdvancedSearchStubModal.vue'

const props = defineProps<{ universeId: string }>()
const route = useRoute()
const router = useRouter()

const { entities, loading, search } = useEntities()
const showCreate = ref(false)
const showAdvanced = ref(false)

function onSearch(query: string) {
  search(props.universeId, query)
}
function select(id: string) {
  router.push({ name: 'entity-detail', params: { ...route.params, entityId: id } })
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
    <CreateEntityModal v-if="showCreate" :universe-id="universeId" @close="showCreate = false" @created="showCreate = false" />
    <AdvancedSearchStubModal v-if="showAdvanced" label="Entities" @close="showAdvanced = false" />
  </div>
</template>
