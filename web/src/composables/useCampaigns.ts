import { ref } from 'vue'
import { getQueryClient, getCommandClient } from '@/api/client'
import { problemMessage } from '@/api/problem'

export interface CampaignSummary {
  id: string
  name: string
  universeId: string
  rulesetId: string
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

  return { campaigns, loading, error, listByUniverse, get, create, rename, archive, listGamemasters, addGamemaster, removeGamemaster }
}
