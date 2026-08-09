import { ref } from 'vue'
import { getQueryClient } from '@/api/client'

export interface RulesetSummary {
  id: string
  name: string
  isArchived: boolean
}

export function useRulesets() {
  const rulesets = ref<RulesetSummary[]>([])
  const loading = ref(false)
  const error = ref<string | null>(null)

  async function list() {
    loading.value = true
    error.value = null
    const { data, error: apiError } = await getQueryClient().GET('/rulesets')
    loading.value = false
    if (apiError) {
      error.value = 'Failed to load Rulesets.'
      return
    }
    rulesets.value = (data ?? []) as RulesetSummary[]
  }

  async function get(id: string): Promise<RulesetSummary | null> {
    const { data, error: apiError } = await getQueryClient().GET('/rulesets/{rulesetId}', {
      params: { path: { rulesetId: id } },
    })
    if (apiError) return null
    return data as RulesetSummary
  }

  return { rulesets, loading, error, list, get }
}
