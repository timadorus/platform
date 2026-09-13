import { ref } from 'vue'
import { watch as vueWatch } from 'vue'
import { getQueryClient } from '@/api/client'
import { useAuthStore } from '@/stores/auth'
import { getRuntimeConfig } from '@/api/runtimeConfig'
import { watchedClauses } from './useAggregateWatch'

export interface AggregateChange {
  globalSeq: number
  aggregateType: string
  aggregateId: string
  eventType: string
  occurredAt: string
}

// useChangeFeed owns the app's single live connection to cmd/realtime (design spec Decision
// 2/5/6). Only App.vue calls this — every other former caller (WorkspaceView.vue,
// UniverseOverviewPanel.vue) now reads the same shared change via inject('lastAggregateChange')
// instead, exactly like every other watcher already does.
export function useChangeFeed() {
  const lastChange = ref<AggregateChange | null>(null)
  let cursor = 0
  let currentUniverseId = ''
  let eventSource: EventSource | null = null
  // epoch invalidates an in-flight catch-up poll (start() or the onopen handler) that a newer
  // call has already superseded — same pattern as the original polling implementation's own
  // epoch guard, kept for the same reason: a slow response for an abandoned Universe/connection
  // must never land after something newer has already taken over.
  let epoch = 0

  // applyChange is the ONLY place lastChange/cursor are written, from either source (the
  // catch-up poll below, or the live SSE onmessage handler) — see this plan's Global
  // Constraints. Once this feature has two independent delivery paths for the same event, a
  // race between them ("just caught up" vs. "a live event for the same change arrives a moment
  // later") is possible; globalSeq is a single global, monotonically increasing sequence
  // (regardless of aggregate type or universe), so "newer than the last one applied" is always
  // well-defined and safe to compare across both paths.
  function applyChange(change: AggregateChange) {
    if (change.globalSeq <= cursor) return
    cursor = change.globalSeq
    lastChange.value = change
  }

  // catchUp fetches everything since `cursor` for `currentUniverseId`, once (not on an
  // interval) — called every time the SSE connection opens, by openStream()'s onopen handler
  // below. Reuses the original poll() implementation's request/response handling verbatim.
  let inFlight = false
  let pendingRetry = false
  async function catchUp() {
    if (inFlight) {
      pendingRetry = true
      return
    }
    if (!currentUniverseId) return
    inFlight = true
    const myEpoch = epoch
    try {
      const { data, error } = await getQueryClient().GET('/universes/{universeId}/changes', {
        params: { path: { universeId: currentUniverseId }, query: { since: cursor } },
      })
      if (myEpoch !== epoch) return
      if (!error && data) {
        for (const change of data as AggregateChange[]) {
          applyChange(change)
        }
      }
    } catch (err) {
      // fetch rejects (throws) on a genuine network failure (offline, DNS, connection reset)
      // rather than returning { error } — openapi-fetch only normalizes non-2xx HTTP responses.
      // Log and let the next reconnect retry rather than propagating an unhandled rejection.
      console.error('useChangeFeed: catch-up poll failed', err)
    } finally {
      inFlight = false
      if (pendingRetry) {
        pendingRetry = false
        void catchUp()
      }
    }
  }

  // openStream (re)opens the EventSource against the CURRENT watchedClauses.value and the
  // CURRENT access token, closing any previous connection first. Every open — whether from
  // start() (a Universe switch), the watchedClauses watcher below (a filter change), or the
  // browser's own silent auto-reconnect on an already-open EventSource object — fires onopen,
  // which is the one correct hook for Decision 6's "every time the connection (re)opens, first
  // catch up" rule: it's the only event that fires uniformly for both cases.
  function openStream() {
    eventSource?.close()
    eventSource = null
    const token = useAuthStore().accessToken
    if (!token) return // not authenticated yet; the next start()/clause change retries
    const cfg = getRuntimeConfig()
    const watchParam = encodeURIComponent(JSON.stringify(watchedClauses.value))
    const url = `${cfg.realtimeApiBaseUrl}/changes/stream?watch=${watchParam}&access_token=${encodeURIComponent(token)}`
    const es = new EventSource(url)
    es.onopen = () => {
      void catchUp()
    }
    es.onmessage = (e) => {
      applyChange(JSON.parse(e.data) as AggregateChange)
    }
    eventSource = es
  }

  // Reopen whenever the set of registered clauses changes (a component mounted/unmounted, or
  // its own clause's scope changed) — does NOT touch `cursor`, so the catch-up this triggers
  // (via openStream's onopen) resumes from exactly where the feed already was, per Decision 6.
  vueWatch(watchedClauses, () => {
    if (eventSource) openStream()
  })

  // start establishes (or switches) which Universe's catch-up-poll cursor is in scope — called
  // by App.vue whenever route.params.universeId changes. Only a genuine change re-baselines the
  // cursor; calling start() again with the same id is a no-op (a filter-only change is handled
  // by the watcher above, not by re-running this).
  async function start(universeId: string) {
    if (universeId === currentUniverseId) {
      if (!eventSource) openStream() // first-ever call for this Universe with no connection yet
      return
    }
    currentUniverseId = universeId
    cursor = 0
    const myEpoch = ++epoch
    try {
      const { data, error } = await getQueryClient().GET('/universes/{universeId}/changes/cursor', {
        params: { path: { universeId } },
      })
      if (myEpoch !== epoch) return // a newer start() call has already superseded this one
      if (error || !data) return // leave cursor at 0; a later start() (e.g. a route change) retries
      cursor = data.globalSeq
      openStream()
    } catch (err) {
      console.error('useChangeFeed: start failed', err)
    }
  }

  // stop closes the live connection entirely. Not called by App.vue in normal operation (the
  // app root never unmounts) — kept for symmetry with start() and for any future caller that
  // does need a clean teardown (e.g. a future test harness).
  function stop() {
    epoch++
    eventSource?.close()
    eventSource = null
    currentUniverseId = ''
    cursor = 0
  }

  return { lastChange, start, stop }
}
