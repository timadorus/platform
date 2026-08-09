import { defineStore } from 'pinia'
import { UserManager, WebStorageStateStore, type User as OidcUser } from 'oidc-client-ts'
import type { RuntimeConfig } from '@/api/runtimeConfig'

// userManager is kept as a module-level (non-reactive) singleton rather than in Pinia's
// `state`. Functionally either works, but `UserManager`'s settings type transitively
// references oidc-client-ts's internal (unexported) `DPoPSettings` type; when it's part of
// the store's reactive state, Pinia's inferred store type structurally exposes that type in
// `useAuthStore`'s public signature, which `vue-tsc -b`'s declaration-emit build (composite
// project references, see tsconfig.app.json) then fails to name (TS4023). Keeping the
// UserManager instance outside `state` avoids that without changing any public behavior.
let userManager: UserManager | null = null

export const useAuthStore = defineStore('auth', {
  state: () => ({
    oidcUser: null as OidcUser | null,
  }),
  getters: {
    isAuthenticated: (state) => !!state.oidcUser && !state.oidcUser.expired,
    accessToken: (state) => state.oidcUser?.access_token ?? null,
    // subject is the JWT `sub` claim — Task 6's selection store keys localStorage by this,
    // so switching accounts in the same browser doesn't leak the previous user's selection
    // (design spec §5).
    subject: (state) => state.oidcUser?.profile.sub ?? null,
    displayName: (state) => state.oidcUser?.profile.name ?? state.oidcUser?.profile.preferred_username ?? null,
  },
  actions: {
    init(cfg: RuntimeConfig) {
      userManager = new UserManager({
        authority: cfg.oidc.authority,
        client_id: cfg.oidc.clientId,
        redirect_uri: cfg.oidc.redirectUri,
        post_logout_redirect_uri: cfg.oidc.postLogoutRedirectUri,
        response_type: 'code',
        scope: 'openid profile',
        automaticSilentRenew: true,
        // oidc-client-ts needs its own storage to survive the PKCE redirect round-trip and
        // drive silent renew — sessionStorage, not localStorage (Global Constraints). The
        // app's own code never reads this storage directly, only the getters above.
        userStore: new WebStorageStateStore({ store: window.sessionStorage }),
      })
      userManager.events.addUserLoaded((user) => {
        this.oidcUser = user
      })
      userManager.events.addUserUnloaded(() => {
        this.oidcUser = null
      })
      userManager.events.addSilentRenewError((err) => {
        console.error('silent renew failed', err)
      })
    },
    // restore checks for an already-signed-in session (e.g. a page reload) without
    // triggering a redirect. Called once at boot, before the router guard runs.
    async restore() {
      if (!userManager) throw new Error('auth store not initialized — call init() first')
      const user = await userManager.getUser()
      this.oidcUser = user && !user.expired ? user : null
    },
    // login redirects to the IdP; returnPath is round-tripped through OIDC `state` and read
    // back in handleCallback so the guard can send the GM back where they were headed.
    async login(returnPath: string) {
      if (!userManager) throw new Error('auth store not initialized — call init() first')
      await userManager.signinRedirect({ state: { returnPath } })
    },
    async handleCallback(): Promise<string> {
      if (!userManager) throw new Error('auth store not initialized — call init() first')
      const user = await userManager.signinRedirectCallback()
      this.oidcUser = user
      const state = user.state as { returnPath?: string } | undefined
      return state?.returnPath ?? '/'
    },
    async logout() {
      if (!userManager) throw new Error('auth store not initialized — call init() first')
      await userManager.signoutRedirect()
    },
  },
})
