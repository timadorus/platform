import { ref } from 'vue'
import { getQueryClient, getCommandClient } from '@/api/client'
import { problemMessage } from '@/api/problem'

export interface UniverseSummary {
  id: string
  name: string
  isArchived: boolean
}

export function useUniverses() {
  const universes = ref<UniverseSummary[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function list() {
    loading.value = true
    error.value = null
    const { data, error: apiError } = await getQueryClient().GET('/universes')
    loading.value = false
    if (apiError) {
      error.value = 'Failed to load Universes.'
      return
    }
    universes.value = (data ?? []) as UniverseSummary[]
  }

  async function get(id: string): Promise<UniverseSummary | null> {
    const { data, error: apiError } = await getQueryClient().GET('/universes/{universeId}', {
      params: { path: { universeId: id } },
    })
    if (apiError) return null
    return data as UniverseSummary
  }

  async function create(name: string, creatorUserIds: string[]): Promise<string> {
    const { data, error: apiError } = await getCommandClient().POST('/universes', {
      body: { name, creatorUserIds },
    })
    if (apiError || !data) {
      throw new Error(problemMessage(apiError) ?? 'Failed to create Universe.')
    }
    return data.id
  }

  async function rename(id: string, name: string): Promise<void> {
    const { error: apiError } = await getCommandClient().PATCH('/universes/{universeId}', {
      params: { path: { universeId: id } },
      body: { name },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to rename Universe.')
  }

  async function archive(id: string): Promise<void> {
    const { error: apiError } = await getCommandClient().POST('/universes/{universeId}/archive', {
      params: { path: { universeId: id } },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to archive Universe.')
  }

  async function listCreators(id: string): Promise<string[]> {
    const { data, error: apiError } = await getQueryClient().GET('/universes/{universeId}/creators', {
      params: { path: { universeId: id } },
    })
    if (apiError) return []
    return (data ?? []) as string[]
  }

  async function addCreator(universeId: string, userId: string): Promise<void> {
    const { error: apiError } = await getCommandClient().POST('/universes/{universeId}/creators/{userId}', {
      params: { path: { universeId, userId } },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to add Creator.')
  }

  async function removeCreator(universeId: string, userId: string): Promise<void> {
    const { error: apiError } = await getCommandClient().DELETE('/universes/{universeId}/creators/{userId}', {
      params: { path: { universeId, userId } },
    })
    // Removing the last Creator returns 409 (backend invariant) — this throws normally like any
    // other failure, so the caller's existing error handling surfaces it via ErrorBanner.
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to remove Creator.')
  }

  return { universes, loading, error, list, get, create, rename, archive, listCreators, addCreator, removeCreator }
}
