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

  return { campaigns, loading, error, listByUniverse, get, create }
}
