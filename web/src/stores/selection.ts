import { defineStore } from 'pinia'
import { useAuthStore } from './auth'

interface SelectionState {
  selectedUniverseId: string | null
  selectedCampaignId: string | null
}

function storageKey(subject: string): string {
  return `timadorus:selection:${subject}`
}

export const useSelectionStore = defineStore('selection', {
  state: (): SelectionState => ({
    selectedUniverseId: null,
    selectedCampaignId: null,
  }),
  actions: {
    // load reads any persisted selection for the current subject. This does NOT validate the
    // ids still exist/aren't archived — callers (the picker views) do that via a real API
    // call before trusting the restored value (design spec §5).
    load() {
      const auth = useAuthStore()
      if (!auth.subject) return
      const raw = localStorage.getItem(storageKey(auth.subject))
      if (!raw) return
      try {
        const parsed = JSON.parse(raw) as SelectionState
        this.selectedUniverseId = parsed.selectedUniverseId ?? null
        this.selectedCampaignId = parsed.selectedCampaignId ?? null
      } catch {
        // corrupt or old-shape data — ignore, start fresh rather than throwing at boot
      }
    },
    persist() {
      const auth = useAuthStore()
      if (!auth.subject) return
      localStorage.setItem(
        storageKey(auth.subject),
        JSON.stringify({
          selectedUniverseId: this.selectedUniverseId,
          selectedCampaignId: this.selectedCampaignId,
        }),
      )
    },
    setUniverse(id: string) {
      this.selectedUniverseId = id
      this.selectedCampaignId = null
      this.persist()
    },
    setCampaign(id: string) {
      this.selectedCampaignId = id
      this.persist()
    },
    clearCampaign() {
      this.selectedCampaignId = null
      this.persist()
    },
    clearUniverse() {
      this.selectedUniverseId = null
      this.selectedCampaignId = null
      this.persist()
    },
  },
})
