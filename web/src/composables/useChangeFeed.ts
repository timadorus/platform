import { ref } from 'vue'
import { getQueryClient } from '@/api/client'

export interface AggregateChange {
  globalSeq: number
  aggregateType: string
  aggregateId: string
  eventType: string
  occurredAt: string
}

// POLL_INTERVAL_MS is deliberately much slower than the existing 750ms eventual-consistency
// polls (waitForUser/waitForCharacter/etc.) — this is a low-urgency background check for changes
// made elsewhere, not "wait for my own just-submitted action".
const POLL_INTERVAL_MS = 5000

// useChangeFeed polls GET /universes/{id}/changes on an interval and exposes the single most
// recent change as one Ref, following the same provide/watch(ref) idiom this codebase already
// uses for sidebarRefreshSignal/pendingEntityId — deliberately not a new pub-sub/event-emitter
// abstraction. Additive to those mechanisms, not a replacement: this only ever reports changes,
// it never itself refreshes anything — each consumer decides what "this aggregate changed"
// means for its own data.
export function useChangeFeed() {
  const lastChange = ref<AggregateChange | null>(null)
  let cursor = 0
  let currentUniverseId = ''
  let timer: ReturnType<typeof setInterval> | null = null
  let inFlight = false
  let epoch = 0

  async function poll() {
    if (inFlight || !currentUniverseId) return
    inFlight = true
    try {
      const { data, error } = await getQueryClient().GET('/universes/{universeId}/changes', {
        params: { path: { universeId: currentUniverseId }, query: { since: cursor } },
      })
      if (!error && data) {
        for (const change of data as AggregateChange[]) {
          cursor = change.globalSeq
          lastChange.value = change
        }
      }
    } finally {
      inFlight = false
    }
  }

  async function start(universeId: string) {
    stop()
    const myEpoch = ++epoch
    currentUniverseId = universeId
    cursor = 0
    const { data } = await getQueryClient().GET('/universes/{universeId}/changes/cursor', {
      params: { path: { universeId } },
    })
    if (myEpoch !== epoch) return // a newer start() call has already superseded this one
    cursor = data?.globalSeq ?? 0
    timer = setInterval(poll, POLL_INTERVAL_MS)
  }

  function stop() {
    epoch++ // invalidate any in-flight start() call too, so its eventual resolution is a no-op
    if (timer) clearInterval(timer)
    timer = null
    currentUniverseId = ''
  }

  return { lastChange, start, stop }
}
