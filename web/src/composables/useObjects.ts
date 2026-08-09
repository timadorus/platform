import { ref } from 'vue'
import { getQueryClient, getCommandClient } from '@/api/client'
import { problemMessage } from '@/api/problem'

export interface ObjectSummary {
  id: string
  name: string
  universeId: string
  isArchived: boolean
}

export function useObjects() {
  const objects = ref<ObjectSummary[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  // search calls the Task 1 `?name=` parameter — always capped at 20 results server-side. An
  // empty name still calls the endpoint (returns the unfiltered, uncapped list) rather than
  // special-casing an empty query client-side.
  async function search(universeId: string, name: string) {
    loading.value = true
    error.value = null
    const { data, error: apiError } = await getQueryClient().GET('/universes/{universeId}/objects', {
      params: { path: { universeId }, query: name ? { name } : {} },
    })
    loading.value = false
    if (apiError) {
      error.value = 'Failed to search Objects.'
      return
    }
    objects.value = (data ?? []) as ObjectSummary[]
  }

  async function get(id: string): Promise<ObjectSummary | null> {
    const { data, error: apiError } = await getQueryClient().GET('/objects/{objectId}', {
      params: { path: { objectId: id } },
    })
    if (apiError) return null
    return data as ObjectSummary
  }

  async function create(universeId: string, name: string): Promise<string> {
    const { data, error: apiError } = await getCommandClient().POST('/universes/{universeId}/objects', {
      params: { path: { universeId } },
      body: { name },
    })
    if (apiError || !data) {
      throw new Error(problemMessage(apiError) ?? 'Failed to create Object.')
    }
    return data.id
  }

  async function rename(id: string, name: string): Promise<void> {
    const { error: apiError } = await getCommandClient().PATCH('/objects/{objectId}', {
      params: { path: { objectId: id } },
      body: { name },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to rename Object.')
  }

  async function archive(id: string): Promise<void> {
    const { error: apiError } = await getCommandClient().POST('/objects/{objectId}/archive', {
      params: { path: { objectId: id } },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to archive Object.')
  }

  return { objects, loading, error, search, get, create, rename, archive }
}
