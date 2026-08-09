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

  return { universes, loading, error, list, get, create }
}
