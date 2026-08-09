import createClient from 'openapi-fetch'
import type { paths as CommandPaths } from './command.types'
import type { paths as QueryPaths } from './query.types'
import type { RuntimeConfig } from './runtimeConfig'

type CommandClient = ReturnType<typeof createClient<CommandPaths>>
type QueryClient = ReturnType<typeof createClient<QueryPaths>>

let commandClient: CommandClient | null = null
let queryClient: QueryClient | null = null

// getAccessToken is set by the auth store (Task 5) via setAccessTokenGetter, once, at boot.
// api/client.ts intentionally has no dependency on Pinia/the auth store itself, so this
// module stays usable independent of Vue's reactivity system.
let getAccessToken: () => string | null = () => null

export function setAccessTokenGetter(fn: () => string | null): void {
  getAccessToken = fn
}

function authMiddleware() {
  return {
    async onRequest({ request }: { request: Request }) {
      const token = getAccessToken()
      if (token) {
        request.headers.set('Authorization', `Bearer ${token}`)
      }
      return request
    },
  }
}

// initApiClients must be called once, after loadRuntimeConfig() resolves and before any
// composable runs — main.ts (Task 5) does this at boot.
export function initApiClients(cfg: RuntimeConfig): void {
  commandClient = createClient<CommandPaths>({ baseUrl: cfg.commandApiBaseUrl })
  queryClient = createClient<QueryPaths>({ baseUrl: cfg.queryApiBaseUrl })
  commandClient.use(authMiddleware())
  queryClient.use(authMiddleware())
}

export function getCommandClient(): CommandClient {
  if (!commandClient) throw new Error('api clients not initialized — call initApiClients() first')
  return commandClient
}

export function getQueryClient(): QueryClient {
  if (!queryClient) throw new Error('api clients not initialized — call initApiClients() first')
  return queryClient
}
