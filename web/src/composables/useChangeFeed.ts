import { nextTick, ref } from 'vue'
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
    const myEpoch = epoch
    try {
      const { data, error } = await getQueryClient().GET('/universes/{universeId}/changes', {
        params: { path: { universeId: currentUniverseId }, query: { since: cursor } },
      })
      if (myEpoch !== epoch) return // stop()/a new start() superseded this poll while it was in flight
      if (!error && data) {
        for (const change of data as AggregateChange[]) {
          cursor = change.globalSeq
          lastChange.value = change
          // Let each change flush to watchers before the next overwrites lastChange — Vue's
          // flush:'pre' watch coalesces same-tick writes, and a poll batch can contain more than
          // one change (e.g. Character creation emits Entity+Character events together).
          await nextTick()
          // Re-check after every await, not just once before the loop: a stop() (or a new
          // start() for a different Universe) landing in the gap between two nextTick() calls
          // must stop this now-abandoned batch from writing any more remaining changes.
          if (myEpoch !== epoch) return
        }
      }
    } catch (err) {
      // fetch rejects (throws) on a genuine network failure (offline, DNS, connection reset)
      // rather than returning { error } — openapi-fetch only normalizes non-2xx HTTP responses.
      // Log and let the next tick retry rather than propagating an unhandled rejection out of
      // setInterval(poll, ...).
      console.error('useChangeFeed: poll failed', err)
    } finally {
      inFlight = false
    }
  }

  async function start(universeId: string) {
    stop()
    const myEpoch = ++epoch
    currentUniverseId = universeId
    cursor = 0
    try {
      const { data, error } = await getQueryClient().GET('/universes/{universeId}/changes/cursor', {
        params: { path: { universeId } },
      })
      if (myEpoch !== epoch) return // a newer start() call has already superseded this one
      if (error || !data) return // leave the feed stopped; a later start() (e.g. a route change) retries
      cursor = data.globalSeq
      timer = setInterval(poll, POLL_INTERVAL_MS)
    } catch (err) {
      // Same network-failure case as poll() above. Leave the feed stopped (timer never gets
      // assigned) rather than letting the rejection propagate out of an unawaited call site
      // (onMounted(() => startChangeFeed(...))) and killing the feed for the rest of the session
      // with no retry.
      console.error('useChangeFeed: start failed', err)
    }
  }

  function stop() {
    epoch++ // invalidate any in-flight start() call too, so its eventual resolution is a no-op
    if (timer) clearInterval(timer)
    timer = null
    currentUniverseId = ''
  }

  return { lastChange, start, stop }
}
