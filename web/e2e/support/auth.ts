import type { BrowserContext } from '@playwright/test'
import type { MockAuthConfig } from './mockBackend'

// seedAuth makes the SPA believe it's already signed in, without any real OIDC round trip:
// oidc-client-ts's WebStorageStateStore (see src/stores/auth.ts's UserManager construction)
// reads a JSON blob from sessionStorage under the key `oidc.user:${authority}:${client_id}` —
// this seeds that key with a valid, unexpired user before the app's own boot script runs
// (via addInitScript), so main.ts's auth.restore() finds it via a plain local getUser() call,
// no network access needed.
// Takes the same shared MockAuthConfig as installMockBackend for call-site symmetry (a caller
// builds one config object and passes it to both) — but only reads `authority` and `clientId`;
// `baseURL` is unused here and exists solely so the two functions share a signature.
export async function seedAuth(context: BrowserContext, config: MockAuthConfig): Promise<void> {
  const now = Math.floor(Date.now() / 1000)
  const oidcUser = {
    id_token: 'fake.id.token',
    session_state: null,
    access_token: 'fake-access-token',
    token_type: 'Bearer',
    scope: 'openid profile email',
    profile: {
      sub: 'test-sub',
      iss: config.authority,
      aud: config.clientId,
      exp: now + 3600,
      iat: now,
      name: 'Dev User',
      preferred_username: 'devuser',
      email: 'devuser@timadorus.local',
    },
    expires_at: now + 3600,
  }
  await context.addInitScript(
    ([key, value]) => {
      window.sessionStorage.setItem(key, value)
    },
    [`oidc.user:${config.authority}:${config.clientId}`, JSON.stringify(oidcUser)],
  )
}
