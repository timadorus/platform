import http from 'node:http'
import type { AddressInfo } from 'node:net'

export interface MockRealtimeChange {
  globalSeq: number
  aggregateType: string
  aggregateId: string
  eventType: string
  occurredAt: string
}

export interface MockRealtimeStream {
  // origin is the base URL (e.g. "http://127.0.0.1:54231") a test passes as
  // installMockBackend's realtimeOrigin — mockBackend.ts's route handler recognizes this
  // origin and calls route.continue() for it instead of intercepting, letting requests reach
  // this real server directly.
  origin: string
  // push fans one change out to every currently-connected client — there is normally exactly
  // one: the app's single EventSource (design spec Decision 2). This mock does not implement
  // cmd/realtime's own server-side WatchClause filtering (it ignores the `watch` query
  // parameter entirely) — give each test its own MockRealtimeStream instance and only push
  // events that test's own assertions actually care about, exactly like MockState.changes
  // already works for the catch-up-poll endpoints.
  push(change: MockRealtimeChange): void
  close(): Promise<void>
  // waitForConnection resolves once at least one client is currently connected (immediately, if
  // one already is). push() has no buffering — a frame pushed before the page's EventSource has
  // actually completed its handshake with this server is simply lost, since there is no
  // production-equivalent redelivery path for that in this mock (only the real catch-up poll,
  // exercised separately by the reconnect test, does that). Every other spec in this file happens
  // to have enough incidental async work (Universe/Campaign/Entity fetches) between page.goto()
  // and its first push() for the connection to land first; the two bare-picker-route tests
  // (nearly instant render, no slow fetch in between) do not, so they call this first to make
  // that ordering an explicit, condition-based wait instead of an accidental one.
  waitForConnection(): Promise<void>
}

export async function startMockRealtimeStream(): Promise<MockRealtimeStream> {
  const clients: http.ServerResponse[] = []
  let notifyConnected: (() => void) | null = null
  const server = http.createServer((req, res) => {
    if (req.url?.startsWith('/changes/stream')) {
      // The page is served from a different origin (e.g. http://localhost:4173) than this mock
      // server's own ephemeral port, so the browser's EventSource treats every request here as
      // cross-origin and enforces CORS — without this header the connection is blocked outright
      // (net::ERR_FAILED, no onopen/onerror ever fires) before a single frame can be written.
      res.writeHead(200, {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
        'Access-Control-Allow-Origin': '*',
      })
      clients.push(res)
      notifyConnected?.()
      req.on('close', () => {
        const i = clients.indexOf(res)
        if (i !== -1) clients.splice(i, 1)
      })
      return
    }
    res.writeHead(404)
    res.end()
  })
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
  const { port } = server.address() as AddressInfo
  return {
    origin: `http://127.0.0.1:${port}`,
    push(change) {
      const frame = `data: ${JSON.stringify(change)}\n\n`
      for (const res of clients) res.write(frame)
    },
    close: () =>
      new Promise<void>((resolve) => {
        for (const res of clients) res.end()
        server.close(() => resolve())
      }),
    waitForConnection() {
      if (clients.length > 0) return Promise.resolve()
      return new Promise<void>((resolve) => {
        notifyConnected = resolve
      })
    },
  }
}
