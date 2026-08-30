# Web SPA E2E Test Harness — Design

## Context

`web/` has no test tooling at all today — no framework, no test directory, no CI test step beyond
"generated API clients are up to date." Every SPA behavior verification so far (three separate
branches now) has been an ad-hoc Playwright script written into a scratchpad, re-derived by hand
each time: launch headless Chromium, seed a fake OIDC session into `sessionStorage`, intercept every
`/api/*` call against an in-memory mock backend, serve the built app, drive it, assert on the
rendered DOM. This spec formalizes that proven pattern into a real, checked-in harness, and adds one
test to it: after creating a Character, both sidebar lists (Characters, Entities) update to include
the new records, and the main view selects and displays the new Character.

This directly exercises the two most recent SPA fixes: `CreateCharacterModal` now emits the new
`characterId` and `CharactersPanel` navigates to it; `CharactersPanel`'s `onCreated` now bumps the
shared `sidebarRefreshSignal` so `EntitiesPanel` also re-fetches (since Character creation
auto-creates a paired Entity server-side).

## Decisions

- **Framework: `@playwright/test`** (the real test runner, not the raw `playwright` library used ad
  hoc so far) — real `expect()` assertions, HTML reports, trace-on-failure, and a `webServer` config
  that removes the hand-rolled static-server script every prior verification needed.
- **Backend is mocked, not a live cluster** — matches the technique already proven three times by
  hand. This keeps the suite fast, deterministic, and runnable in CI without a Kubernetes cluster
  (unlike the Go backend's `test/e2e`, which is intentionally cluster-dependent and manual-only —
  `make test-e2e`, not wired into `ci.yml`).
- **Wired into CI now**, in the existing `web-build` job, since no cluster is needed — just installing
  the Chromium browser.
- **The mock backend is generalized, not scenario-specific** — arrays (`universes`, `campaigns`,
  `users`, `characters`, `entities`, `rulesets`, `gamemasterIds`) seeded per-test, not a single
  hardcoded Character/Campaign pair, so the next test written against this harness doesn't have to
  re-derive the same interception plumbing.
- **The mocked create-Character command mirrors real backend behavior**: it must create both a
  Character *and* an Entity with the same name, matching `internal/command/character/service.go`'s
  actual cross-aggregate `CreateCharacter` — otherwise the test would prove nothing about the real
  contract this feature depends on.

## Harness Structure

```
web/
  e2e/
    support/
      mockBackend.ts
      auth.ts
    character-creation.spec.ts
  playwright.config.ts
```

### `web/e2e/support/auth.ts`

Seeds a fake OIDC session so the SPA believes it's already authenticated — no real Zitadel
involved. Mirrors the scratchpad pattern exactly (the key format matches what `oidc-client-ts`
looks for: `oidc.user:${authority}:${clientId}`):

```ts
import type { BrowserContext } from '@playwright/test'

export interface MockAuthOptions {
  baseURL: string
  authority: string
  clientId: string
}

export async function seedAuth(context: BrowserContext, opts: MockAuthOptions): Promise<void> {
  const now = Math.floor(Date.now() / 1000)
  const oidcUser = {
    id_token: 'fake.id.token',
    session_state: null,
    access_token: 'fake-access-token',
    token_type: 'Bearer',
    scope: 'openid profile email',
    profile: {
      sub: 'test-sub',
      iss: opts.authority,
      aud: opts.clientId,
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
    [`oidc.user:${opts.authority}:${opts.clientId}`, JSON.stringify(oidcUser)],
  )
}
```

### `web/e2e/support/mockBackend.ts`

A per-test mutable state object plus a route handler installed via `page.route('**/*', ...)`.
Answers `/config.json` (the app's own runtime-config endpoint), the OIDC well-known/discovery
endpoints, and every query/command API call this scenario's page tree makes. Full detail (exact
route list, request/response shapes) is left to the implementation plan — the important structural
decisions are: (1) state is an object of arrays the test seeds before navigating, not module-level
globals, so tests don't leak state into each other; (2) unhandled `/api/*` calls log a warning and
return an empty array/`204` rather than hanging the test or throwing, so a test only has to mock
what it actually exercises; (3) the create-Character handler appends both a Character and an Entity
(same name), returning `{ characterId, entityId }` matching `CharacterCreatedResponse`.

### `web/playwright.config.ts`

```ts
import { defineConfig, devices } from '@playwright/test'

export default defineConfig({
  testDir: './e2e',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: 'html',
  use: {
    baseURL: 'http://localhost:4173',
    trace: 'on-first-retry',
  },
  webServer: {
    command: 'npm run build && npm run preview -- --port 4173',
    url: 'http://localhost:4173',
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
})
```

Rebuilding on every run (rather than pointing at a possibly-stale `dist`) matches what every prior
manual verification had to remember to do by hand.

### `web/e2e/character-creation.spec.ts`

Seeds one Universe, Campaign (with a Ruleset), and User; navigates to
`/universes/{u}/campaigns/{c}`; clicks **+ Create Character**; fills the modal (name + player);
submits; then asserts, in order:

1. The `CreateCharacterModal` closes (no longer in the DOM).
2. The Characters sidebar panel's list now includes the new character's name.
3. The Entities sidebar panel's list now includes an entity with that same name.
4. The URL is now `/universes/{u}/campaigns/{c}/characters/{newCharacterId}`.
5. The main view (`CharacterDetailView`, rendered via `<router-view>`) shows the new character's
   name in its Base Info card — proving it was actually selected and displayed, not just created.

## `package.json` / CI Changes

- `npm install --save-dev @playwright/test` (installs whatever the current release is — this spec
  does not pin a version by hand).
- New script: `"test:e2e": "playwright test"`.
- `.github/workflows/ci.yml`'s `web-build` job gains two steps after the existing `npm ci`:
  installing the Chromium browser (`npx playwright install --with-deps chromium`), then running
  `npm run test:e2e`.

## Testing

This spec's own subject *is* the SPA's test tooling, so there is no separate "how do we verify this"
layer beyond: the new suite must actually pass locally (`npm run test:e2e`) and in CI, and — since
this is the first Playwright config this repo has ever had — the implementer should confirm the
`webServer` auto-start/reuse behavior actually works (a common first-setup foot-gun) rather than
just trusting the config file compiles.

## Explicitly Out of Scope

- Any test beyond the one Character-creation scenario (more tests are a natural follow-up once the
  harness exists, not part of this branch).
- Testing against a real, live dev cluster — deliberately mocked, per the Decisions section.
- Visual/screenshot regression testing (Playwright supports it; not requested, not built here).
- Any change to backend/API code — this branch is entirely new test infrastructure plus, if needed,
  minor test-only fixtures; no production `web/src` code changes are anticipated (the two features
  under test already merged in prior branches).
