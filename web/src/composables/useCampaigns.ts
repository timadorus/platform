import { ref } from 'vue'
import { getQueryClient, getCommandClient } from '@/api/client'
import { problemMessage } from '@/api/problem'
import { pollUntil } from './usePolling'

export interface CampaignSummary {
  id: string
  name: string
  universeId: string
  rulesetId: string
  configuration: string
  isArchived: boolean
}

export function useCampaigns() {
  const campaigns = ref<CampaignSummary[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function listByUniverse(universeId: string) {
    loading.value = true
    error.value = null
    const { data, error: apiError } = await getQueryClient().GET('/universes/{universeId}/campaigns', {
      params: { path: { universeId } },
    })
    loading.value = false
    if (apiError) {
      error.value = 'Failed to load Campaigns.'
      return
    }
    campaigns.value = (data ?? []) as CampaignSummary[]
  }

  async function get(id: string): Promise<CampaignSummary | null> {
    const { data, error: apiError } = await getQueryClient().GET('/campaigns/{campaignId}', {
      params: { path: { campaignId: id } },
    })
    if (apiError) return null
    return data as CampaignSummary
  }

  // waitForCampaign polls get(id) until it succeeds or timeoutMs elapses. Like waitForCharacter
  // (useCharacters.ts), this cannot distinguish "the projector hasn't caught up yet" from "this id
  // doesn't exist" — get() returns null identically either way — so every failed attempt is
  // retried until the deadline; callers should show one honest "couldn't load" message on timeout,
  // not a distinct error state.
  async function waitForCampaign(
    id: string,
    opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
  ): Promise<CampaignSummary | null> {
    return pollUntil(() => get(id), opts)
  }

  async function create(
    universeId: string,
    name: string,
    rulesetId: string,
    gamemasterUserIds: string[],
  ): Promise<string> {
    const { data, error: apiError } = await getCommandClient().POST('/universes/{universeId}/campaigns', {
      params: { path: { universeId } },
      body: { name, rulesetId, gamemasterUserIds },
    })
    if (apiError || !data) {
      throw new Error(problemMessage(apiError) ?? 'Failed to create Campaign.')
    }
    return data.id
  }

  async function rename(id: string, name: string): Promise<void> {
    const { error: apiError } = await getCommandClient().PATCH('/campaigns/{campaignId}', {
      params: { path: { campaignId: id } },
      body: { name },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to rename Campaign.')
  }

  async function archive(id: string): Promise<void> {
    const { error: apiError } = await getCommandClient().POST('/campaigns/{campaignId}/archive', {
      params: { path: { campaignId: id } },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to archive Campaign.')
  }

  async function requestConfiguration(id: string, payload: Record<string, unknown>): Promise<void> {
    const { error: apiError } = await getCommandClient().PUT('/campaigns/{campaignId}/configure', {
      params: { path: { campaignId: id } },
      body: payload,
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to request configuration change.')
  }

  async function listGamemasters(id: string): Promise<string[]> {
    const { data, error: apiError } = await getQueryClient().GET('/campaigns/{campaignId}/gamemasters', {
      params: { path: { campaignId: id } },
    })
    if (apiError) return []
    return (data ?? []) as string[]
  }

  async function addGamemaster(campaignId: string, userId: string): Promise<void> {
    const { error: apiError } = await getCommandClient().POST('/campaigns/{campaignId}/gamemasters/{userId}', {
      params: { path: { campaignId, userId } },
    })
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to add Gamemaster.')
  }

  async function removeGamemaster(campaignId: string, userId: string): Promise<void> {
    const { error: apiError } = await getCommandClient().DELETE('/campaigns/{campaignId}/gamemasters/{userId}', {
      params: { path: { campaignId, userId } },
    })
    // Removing the last Gamemaster returns 409 (backend invariant) — surfaced via the same
    // error path as any other failure, never swallowed.
    if (apiError) throw new Error(problemMessage(apiError) ?? 'Failed to remove Gamemaster.')
  }

  return { campaigns, loading, error, listByUniverse, get, waitForCampaign, create, rename, archive, requestConfiguration, listGamemasters, addGamemaster, removeGamemaster }
}
