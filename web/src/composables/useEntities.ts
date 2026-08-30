import { ref } from 'vue'
import { getQueryClient, getCommandClient } from '@/api/client'
import { problemMessage } from '@/api/problem'

export interface EntitySummary {
  id: string
  name: string
  universeId: string
  isArchived: boolean
}

export function useEntities() {
  const entities = ref<EntitySummary[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  // search calls the Task 1 `?name=` parameter — capped at 20 results server-side only when a
  // non-empty name filter is supplied. An empty name still calls the endpoint (returns the
  // full unfiltered, uncapped list) rather than special-casing an empty query client-side.
  async function search(universeId: string, name: string) {
    loading.value = true
    error.value = null
    const { data, error: apiError } = await getQueryClient().GET('/universes/{universeId}/entities', {
      params: { path: { universeId }, query: name ? { name } : {} },
    })
    loading.value = false
    if (apiError) {
      error.value = 'Failed to search Entities.'
      return
    }
    entities.value = (data ?? []) as EntitySummary[]
  }

  async function get(id: string): Promise<EntitySummary | null> {
    const { data, error: apiError } = await getQueryClient().GET('/entities/{entityId}', {
      params: { path: { entityId: id } },
    })
    if (apiError) return null
    return data as EntitySummary
  }

  async function create(universeId: string, name: string): Promise<string> {
    const { data, error: apiError } = await getCommandClient().POST('/universes/{universeId}/entities', {
      params: { path: { universeId } },
      body: { name },
    })
    if (apiError || !data) {
      throw new Error(problemMessage(apiError) ?? 'Failed to create Entity.')
    }
    return data.id
  }

  async function rename(id: string, name: string): Promise<void> {
    const { error: apiError } = await getCommandClient().PATCH('/entities/{entityId}', {
      params: { path: { entityId: id } },
      body: { name },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to rename Entity.')
  }

  async function archive(id: string): Promise<void> {
    const { error: apiError } = await getCommandClient().POST('/entities/{entityId}/archive', {
      params: { path: { entityId: id } },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to archive Entity.')
  }

  // waitForEntityInList polls search(universeId, '') — deliberately unfiltered, regardless of
  // any search term the caller might otherwise be using, so a new Entity is guaranteed findable
  // rather than potentially excluded by an unrelated in-progress filter — until entityId appears
  // or timeoutMs elapses. Mirrors useUsers.ts's waitForUser / useCharacters.ts's
  // waitForCharacterInList.
  async function waitForEntityInList(
    universeId: string,
    entityId: string,
    opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
  ): Promise<boolean> {
    const intervalMs = opts.intervalMs ?? 750
    const timeoutMs = opts.timeoutMs ?? 15000
    const deadline = Date.now() + timeoutMs
    for (;;) {
      if (opts.signal?.aborted) return false
      await search(universeId, '')
      if (opts.signal?.aborted) return false
      if (error.value) return false
      if (entities.value.some((e) => e.id === entityId)) return true
      if (Date.now() >= deadline) return false
      await new Promise((resolve) => setTimeout(resolve, intervalMs))
    }
  }

  return { entities, loading, error, search, get, create, rename, archive, waitForEntityInList }
}
