import { ref } from 'vue'
import { getQueryClient, getCommandClient } from '@/api/client'
import { problemMessage } from '@/api/problem'

export interface CharacterSummary {
  id: string
  name: string
  campaignId: string
  entityId: string
  playerUserId: string
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

  return { characters, loading, error, list, get, create, rename, archive, setPlayer }
}
