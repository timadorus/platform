import { ref } from 'vue'
import { getQueryClient, getCommandClient } from '@/api/client'
import { problemMessage } from '@/api/problem'

export interface UserSummary {
  id: string
  name: string
  isArchived: boolean
}

export function useUsers() {
  const users = ref<UserSummary[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function list() {
    loading.value = true
    error.value = null
    const { data, error: apiError } = await getQueryClient().GET('/users')
    loading.value = false
    if (apiError) {
      error.value = 'Failed to load Users.'
      return
    }
    users.value = (data ?? []) as UserSummary[]
  }

  async function create(name: string): Promise<string> {
    const { data, error: apiError } = await getCommandClient().POST('/users', { body: { name } })
    if (apiError || !data) {
      throw new Error(problemMessage(apiError) ?? 'Failed to create User.')
    }
    return data.id
  }

  async function rename(id: string, name: string): Promise<void> {
    const { error: apiError } = await getCommandClient().PATCH('/users/{userId}', {
      params: { path: { userId: id } },
      body: { name },
    })
    if (apiError) {
      throw new Error(problemMessage(apiError) ?? 'Failed to rename User.')
    }
  }

  async function archive(id: string): Promise<void> {
    const { error: apiError } = await getCommandClient().POST('/users/{userId}/archive', {
      params: { path: { userId: id } },
    })
    if (apiError) {
      throw new Error(problemMessage(apiError) ?? 'Failed to archive User.')
    }
  }

  return { users, loading, error, list, create, rename, archive }
}
