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
}

export async function startMockRealtimeStream(): Promise<MockRealtimeStream> {
  const clients: http.ServerResponse[] = []
  const server = http.createServer((req, res) => {
    if (req.url?.startsWith('/changes/stream')) {
      res.writeHead(200, {
        'Content-Type': 'text/event-stream',
        'Cache-Control': 'no-cache',
        Connection: 'keep-alive',
      })
      clients.push(res)
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
  }
}
