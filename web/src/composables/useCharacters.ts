import { ref } from 'vue'
import { getQueryClient, getCommandClient } from '@/api/client'
import { problemMessage } from '@/api/problem'
import { pollUntil } from './usePolling'

export interface CharacterSummary {
  id: string
  name: string
  campaignId: string
  entityId: string
  playerUserId: string
  info: string
  isArchived: boolean
}

export function useCharacters() {
  const characters = ref<CharacterSummary[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function list(campaignId: string) {
    loading.value = true
    error.value = null
    const { data, error: apiError } = await getQueryClient().GET('/campaigns/{campaignId}/characters', {
      params: { path: { campaignId } },
    })
    loading.value = false
    if (apiError) {
      error.value = 'Failed to load Characters.'
      return
    }
    characters.value = (data ?? []) as CharacterSummary[]
  }

  async function get(id: string): Promise<CharacterSummary | null> {
    const { data, error: apiError } = await getQueryClient().GET('/characters/{characterId}', {
      params: { path: { characterId: id } },
    })
    if (apiError) return null
    return data as CharacterSummary
  }

  async function create(campaignId: string, name: string, playerUserId: string): Promise<{ characterId: string; entityId: string }> {
    const { data, error: apiError } = await getCommandClient().POST('/campaigns/{campaignId}/characters', {
      params: { path: { campaignId } },
      body: { name, playerUserId },
    })
    if (apiError || !data) {
      throw new Error(problemMessage(apiError) ?? 'Failed to create Character.')
    }
    return data
  }

  async function rename(id: string, name: string): Promise<void> {
    const { error: apiError } = await getCommandClient().PATCH('/characters/{characterId}', {
      params: { path: { characterId: id } },
      body: { name },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to rename Character.')
  }

  async function archive(id: string): Promise<void> {
    const { error: apiError } = await getCommandClient().POST('/characters/{characterId}/archive', {
      params: { path: { characterId: id } },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to archive Character.')
  }

  async function setPlayer(id: string, userId: string): Promise<void> {
    const { error: apiError } = await getCommandClient().PUT('/characters/{characterId}/player', {
      params: { path: { characterId: id } },
      body: { userId },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to reassign Player.')
  }

  async function requestAction(id: string, payload: Record<string, unknown>): Promise<void> {
    const { error: apiError } = await getCommandClient().PUT('/characters/{characterId}/action', {
      params: { path: { characterId: id } },
      body: payload,
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to request action.')
  }

  // waitForCharacter polls get(id) until it succeeds or timeoutMs elapses. Unlike waitForUser
  // (useUsers.ts), this cannot distinguish "the projector hasn't caught up yet" from "this id
  // doesn't exist" — get() 404s identically either way — so every failed attempt is treated the
  // same and retried until the deadline; callers should show one honest "couldn't load" message
  // on timeout, not a distinct error state.
  async function waitForCharacter(
    id: string,
    opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
  ): Promise<CharacterSummary | null> {
    return pollUntil(() => get(id), opts)
  }

  // waitForCharacterInList polls list(campaignId) until characterId appears in the result or
  // timeoutMs elapses — mirrors waitForUser exactly (list() sets error.value on a real failure,
  // distinct from "not in the list yet", so a hard error bails out immediately instead of
  // retrying pointlessly for the full timeout).
  async function waitForCharacterInList(
    campaignId: string,
    characterId: string,
    opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
  ): Promise<boolean> {
    const found = await pollUntil(
      async () => {
        await list(campaignId)
        return characters.value.some((c) => c.id === characterId) ? true : null
      },
      { ...opts, shouldStop: () => error.value !== null },
    )
    return found ?? false
  }

  return {
    characters,
    loading,
    error,
    list,
    get,
    create,
    rename,
    archive,
    setPlayer,
    requestAction,
    waitForCharacter,
    waitForCharacterInList,
  }
}
