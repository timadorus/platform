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

  // waitForUser polls list() until a User with id appears in the freshly fetched list, or
  // timeoutMs elapses. The Query API's read model is populated asynchronously by a projector, so
  // a User created via create() is not guaranteed to be present in the very next list() call.
  // Returns true once found, false on timeout or a hard fetch error — never throws, since both
  // are expected, handleable outcomes for a caller (CreatingUserModal), not programming errors.
  //
  // A failed list() call (error.value set) bails out immediately rather than retrying for the
  // full timeoutMs — a network/auth failure isn't going to fix itself by polling, and the caller
  // can distinguish "timed out" from "errored" by checking this composable's own `error` ref
  // after a false return.
  //
  // opts.signal, if given, is checked before each list() call and again right after — set it to
  // an AbortController's signal and call .abort() on unmount to stop the loop (and its pending
  // setTimeout) rather than let it keep polling in the background after the caller is gone.
  async function waitForUser(
    id: string,
    opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
  ): Promise<boolean> {
    const intervalMs = opts.intervalMs ?? 750
    const timeoutMs = opts.timeoutMs ?? 15000
    const deadline = Date.now() + timeoutMs
    for (;;) {
      if (opts.signal?.aborted) return false
      await list()
      if (opts.signal?.aborted) return false
      if (error.value) return false
      if (users.value.some((u) => u.id === id)) return true
      if (Date.now() >= deadline) return false
      await new Promise((resolve) => setTimeout(resolve, intervalMs))
    }
  }

  return { users, loading, error, list, create, rename, archive, waitForUser }
}
