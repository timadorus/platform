export interface RuntimeConfig {
  commandApiBaseUrl: string
  queryApiBaseUrl: string
  oidc: {
    authority: string
    clientId: string
    redirectUri: string
    postLogoutRedirectUri: string
  }
}

let cached: RuntimeConfig | null = null

// loadRuntimeConfig fetches /config.json once and caches it. In dev, web/public/config.json
// (above) supplies working defaults against a local docker-compose stack. In production, the
// same path is served from a file mounted by Task 16's Helm chart, populated from a
// ConfigMap — never baked into the JS bundle at build time (design spec §9, since Vite env
// vars are resolved at build time and this value must vary per Helm release).
export async function loadRuntimeConfig(): Promise<RuntimeConfig> {
  if (cached) return cached
  const res = await fetch('/config.json', { cache: 'no-store' })
  if (!res.ok) {
    throw new Error(`failed to load /config.json: ${res.status}`)
  }
  cached = (await res.json()) as RuntimeConfig
  return cached
}
