# Web SPA E2E Test Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `web/` its first real, checked-in test harness (`@playwright/test`, mocked backend,
no live cluster needed), with one test proving that creating a Character updates both sidebar
lists (Characters, Entities) and selects/displays the new Character in the main view — per
`docs/superpowers/specs/2026-08-30-spa-e2e-test-harness-design.md`.

**Architecture:** A generalized mock-backend module (route interception over an in-memory,
per-test-seeded state) plus an auth-seeding helper, both extracted from a pattern already proven
by hand three times on prior branches. One Playwright config, one spec file. A second task wires
the suite into CI, since the mocked backend needs no live cluster.

**Tech Stack:** `@playwright/test`, TypeScript, Vue 3 (already in place).

## Global Constraints

- No backend/API/domain changes anywhere in this plan — this is test infrastructure only.
- No production `web/src` code changes are anticipated. If the implementer finds the existing
  markup genuinely cannot be selected/asserted on without a source change (e.g., a missing
  `aria-label`), it must be a minimal, additive change with no behavior change, called out
  explicitly in the task report — not a silent scope expansion.
- The mock backend is deliberately **general-purpose**: state is arrays seeded per-test, not a
  single hardcoded scenario. A future test reusing `support/mockBackend.ts`/`support/auth.ts`
  should not need to touch this plan's files.
- The mocked create-Character command must create both a Character **and** an Entity with the
  **same name** (mirrors `internal/command/character/service.go`'s real cross-aggregate behavior)
  — not just the Character.
- The auth mock's OIDC "authority" must be **same-origin** with the app under test (e.g.
  `${baseURL}/oidc`, not an external domain) — the mock backend intercepts by pathname only, so a
  cross-origin authority would silently escape interception and hang/fail on a real network call.
- `npm run build` and `npm run test:e2e` must both pass after Task 1; `npm run typecheck` must
  stay clean throughout (the new `.ts` support files are real TypeScript, not excluded from the
  project's type-checking).
- File layout after this plan:
  - `web/e2e/support/auth.ts` (new)
  - `web/e2e/support/mockBackend.ts` (new)
  - `web/e2e/character-creation.spec.ts` (new)
  - `web/playwright.config.ts` (new)
  - `web/package.json` (modified — new devDependency, new script)
  - `.github/workflows/ci.yml` (modified — Task 2 only)

---

### Task 1: Harness + Character-creation test

**Files:**
- Create: `web/e2e/support/auth.ts`
- Create: `web/e2e/support/mockBackend.ts`
- Create: `web/e2e/character-creation.spec.ts`
- Create: `web/playwright.config.ts`
- Modify: `web/package.json`

**Interfaces:**
- Produces: `seedAuth(context, opts: { baseURL, authority, clientId })` (auth.ts);
  `createMockState(overrides?)`, `installMockBackend(page, state, opts: { baseURL, authority,
  clientId }): Promise<string[]>` (mockBackend.ts, returns the observed `METHOD /path` call log).
- Consumed by: the one spec file in this task, and by any future spec file (out of scope for this
  plan, but the interface is designed for that reuse per the Global Constraints).

- [ ] **Step 1: Install the dependency**

Run (from `web/`): `npm install --save-dev @playwright/test`
Then: `npx playwright install --with-deps chromium`

This resolves and locks whatever the current `@playwright/test` release is — do not hand-edit a
version number into `package.json`; let `npm install` write it.

- [ ] **Step 2: Add the npm script**

In `web/package.json`, add to `"scripts"`:

```json
    "test:e2e": "playwright test"
```

(Keep every existing script exactly as-is; this is one new line in that object.)

- [ ] **Step 3: Write the auth-seeding helper**

Create `web/e2e/support/auth.ts`:

```ts
import type { BrowserContext } from '@playwright/test'

export interface MockAuthConfig {
  baseURL: string
  authority: string
  clientId: string
}

// seedAuth makes the SPA believe it's already signed in, without any real OIDC round trip:
// oidc-client-ts's WebStorageStateStore (see src/stores/auth.ts's UserManager construction)
// reads a JSON blob from sessionStorage under the key `oidc.user:${authority}:${client_id}` —
// this seeds that key with a valid, unexpired user before the app's own boot script runs
// (via addInitScript), so main.ts's auth.restore() finds it via a plain local getUser() call,
// no network access needed.
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
```

- [ ] **Step 4: Write the mock backend**

Create `web/e2e/support/mockBackend.ts`:

```ts
import type { Page, Route } from '@playwright/test'

export interface MockUniverse {
  id: string
  name: string
  isArchived: boolean
}

export interface MockCampaign {
  id: string
  universeId: string
  name: string
  rulesetId: string
  isArchived: boolean
}

export interface MockUser {
  id: string
  name: string
  isArchived: boolean
}

export interface MockCharacter {
  id: string
  name: string
  campaignId: string
  entityId: string
  playerUserId: string
  isArchived: boolean
}

export interface MockEntity {
  id: string
  name: string
  universeId: string
  isArchived: boolean
}

export interface MockRuleset {
  id: string
  name: string
}

export interface MockState {
  universes: MockUniverse[]
  campaigns: MockCampaign[]
  users: MockUser[]
  characters: MockCharacter[]
  entities: MockEntity[]
  rulesets: MockRuleset[]
  gamemasterIds: string[]
  nextId: number
}

// createMockState seeds a fresh, per-test state object — arrays, not module-level globals, so
// tests never leak data into each other even when run in parallel (playwright.config.ts sets
// fullyParallel: true).
export function createMockState(overrides: Partial<MockState> = {}): MockState {
  return {
    universes: [],
    campaigns: [],
    users: [],
    characters: [],
    entities: [],
    rulesets: [],
    gamemasterIds: [],
    nextId: 1,
    ...overrides,
  }
}

function newId(state: MockState, prefix: string): string {
  return `${prefix}-${state.nextId++}`
}

interface RouteMatch {
  params: Record<string, string>
}

// matchPath is a tiny path-template matcher (":id" segments only) — enough for this harness's
// flat REST-ish paths, not a general router.
function matchPath(pattern: string, path: string): RouteMatch | null {
  const patternParts = pattern.split('/').filter(Boolean)
  const pathParts = path.split('/').filter(Boolean)
  if (patternParts.length !== pathParts.length) return null
  const params: Record<string, string> = {}
  for (let i = 0; i < patternParts.length; i++) {
    const part = patternParts[i]
    if (part.startsWith(':')) {
      params[part.slice(1)] = decodeURIComponent(pathParts[i])
    } else if (part !== pathParts[i]) {
      return null
    }
  }
  return { params }
}

export interface MockAuthConfig {
  baseURL: string
  authority: string
  clientId: string
}

// installMockBackend intercepts /config.json, the OIDC well-known endpoint, and every
// command/query API call this harness's page tree can make, answering from `state` — a mutable
// object the caller seeds before navigating and can inspect afterward. Deliberately
// general-purpose (arrays, matched-by-pattern routes) rather than scenario-specific, so a later
// test can reuse it with its own seed data without touching this file. Returns the observed
// `METHOD /path` call log for tests that want to assert a specific request was (or wasn't) sent.
export async function installMockBackend(page: Page, state: MockState, auth: MockAuthConfig): Promise<string[]> {
  const apiCalls: string[] = []
  const { baseURL, authority, clientId } = auth

  function json(route: Route, body: unknown, status = 200) {
    return route.fulfill({ status, contentType: 'application/json', body: JSON.stringify(body) })
  }

  await page.route('**/*', async (route) => {
    const req = route.request()
    const url = new URL(req.url())
    const p = url.pathname
    const method = req.method()

    if (p === '/config.json') {
      return json(route, {
        commandApiBaseUrl: `${baseURL}/api/command`,
        queryApiBaseUrl: `${baseURL}/api/query`,
        oidc: {
          authority,
          clientId,
          redirectUri: `${baseURL}/login`,
          postLogoutRedirectUri: `${baseURL}/`,
        },
      })
    }
    if (p === '/oidc/.well-known/openid-configuration') {
      return json(route, {
        issuer: authority,
        authorization_endpoint: `${authority}/authorize`,
        token_endpoint: `${authority}/token`,
        userinfo_endpoint: `${authority}/userinfo`,
        end_session_endpoint: `${authority}/logout`,
        jwks_uri: `${authority}/keys`,
      })
    }
    if (p.startsWith('/oidc/')) return json(route, {})

    if (!p.startsWith('/api/')) return route.continue()

    apiCalls.push(`${method} ${p}`)
    const query = url.searchParams
    let m: RouteMatch | null

    // ---- query API ----
    if (method === 'GET' && (m = matchPath('/api/query/universes/:universeId', p))) {
      const universe = state.universes.find((u) => u.id === m!.params.universeId)
      return universe ? json(route, universe) : json(route, { title: 'not found' }, 404)
    }
    if (method === 'GET' && (m = matchPath('/api/query/campaigns/:campaignId', p))) {
      const campaign = state.campaigns.find((c) => c.id === m!.params.campaignId)
      return campaign ? json(route, campaign) : json(route, { title: 'not found' }, 404)
    }
    if (method === 'GET' && matchPath('/api/query/campaigns/:campaignId/gamemasters', p)) {
      return json(route, state.gamemasterIds)
    }
    if (method === 'GET' && (m = matchPath('/api/query/campaigns/:campaignId/characters', p))) {
      return json(route, state.characters.filter((c) => c.campaignId === m!.params.campaignId && !c.isArchived))
    }
    if (method === 'GET' && (m = matchPath('/api/query/characters/:characterId', p))) {
      const character = state.characters.find((c) => c.id === m!.params.characterId)
      return character ? json(route, character) : json(route, { title: 'not found' }, 404)
    }
    if (method === 'GET' && (m = matchPath('/api/query/universes/:universeId/entities', p))) {
      const name = query.get('name')?.toLowerCase() ?? ''
      const matches = state.entities.filter(
        (e) => e.universeId === m!.params.universeId && !e.isArchived && (!name || e.name.toLowerCase().includes(name)),
      )
      return json(route, matches)
    }
    if (method === 'GET' && matchPath('/api/query/universes/:universeId/objects', p)) {
      return json(route, [])
    }
    if (method === 'GET' && p === '/api/query/users') {
      return json(route, state.users)
    }
    if (method === 'GET' && (m = matchPath('/api/query/rulesets/:rulesetId', p))) {
      const ruleset = state.rulesets.find((r) => r.id === m!.params.rulesetId)
      return ruleset ? json(route, ruleset) : json(route, { title: 'not found' }, 404)
    }

    // ---- command API ----
    if (method === 'POST' && (m = matchPath('/api/command/campaigns/:campaignId/characters', p))) {
      const body = JSON.parse(req.postData() || '{}') as { name: string; playerUserId: string }
      const characterId = newId(state, 'character')
      const entityId = newId(state, 'entity')
      const campaign = state.campaigns.find((c) => c.id === m!.params.campaignId)
      state.characters.push({
        id: characterId,
        name: body.name,
        campaignId: m!.params.campaignId,
        entityId,
        playerUserId: body.playerUserId,
        isArchived: false,
      })
      // Mirrors internal/command/character/service.go's real cross-aggregate CreateCharacter:
      // the auto-created Entity gets the same name as the Character.
      state.entities.push({
        id: entityId,
        name: body.name,
        universeId: campaign?.universeId ?? '',
        isArchived: false,
      })
      return json(route, { characterId, entityId }, 201)
    }

    console.warn(`[mockBackend] unhandled ${method} ${p} -> []`)
    return json(route, [])
  })

  return apiCalls
}
```

- [ ] **Step 5: Write the Playwright config**

Create `web/playwright.config.ts`:

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

- [ ] **Step 6: Write the test**

Create `web/e2e/character-creation.spec.ts`:

```ts
import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [{ id: 'c1', universeId: 'u1', name: 'Test Campaign', rulesetId: 'r1', isArchived: false }],
    rulesets: [{ id: 'r1', name: 'Test Ruleset' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    gamemasterIds: ['user-1'],
  })
}

test('creating a Character updates both sidebar lists and selects it in the main view', async ({ page, context, baseURL }) => {
  const base = baseURL!
  // Same-origin authority — the mock backend intercepts by pathname only (Global Constraints),
  // so an external domain here would silently escape interception.
  const authority = `${base}/oidc`

  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  const apiCalls = await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')

  await page.getByRole('button', { name: '+ Create Character' }).click()
  await expect(page.getByRole('heading', { name: 'Create Character' })).toBeVisible()

  const modalForm = page.locator('form')
  await modalForm.locator('input[type="text"]').first().fill('Frodo Baggins')
  await page.getByPlaceholder('Search users…').fill('devuser')
  await page.getByRole('button', { name: 'devuser@timadorus.local' }).click()
  await modalForm.getByRole('button', { name: 'Create', exact: true }).click()

  // 1. modal closes
  await expect(page.getByRole('heading', { name: 'Create Character' })).toHaveCount(0)

  // 2. Characters sidebar shows the new character
  const charactersSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Characters' }) })
  await expect(charactersSection.getByText('Frodo Baggins')).toBeVisible()

  // 3. Entities sidebar shows the new, same-named Entity
  const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
  await expect(entitiesSection.getByRole('button', { name: 'Frodo Baggins' })).toBeVisible()

  // 4. the create command actually fired (not just a coincidentally-correct UI)
  expect(apiCalls).toContain('POST /api/command/campaigns/c1/characters')

  // 5. URL now points at the new Character's own detail route
  await expect(page).toHaveURL(/\/universes\/u1\/campaigns\/c1\/characters\/character-\d+$/)

  // 6. main view selects and displays the new Character
  const baseInfo = page.locator('div.rounded-md').filter({ hasText: 'Base Info' })
  await expect(baseInfo.getByText('Frodo Baggins')).toBeVisible()
})
```

- [ ] **Step 7: Run the suite**

Run (from `web/`): `npm run test:e2e`
Expected: 1 passed. If it fails, read the failure carefully — this is this repo's first-ever
Playwright config, so a `webServer` startup timeout, a route-interception miss (check the
`[mockBackend] unhandled ...` console warnings Playwright's HTML report captures), or a selector
mismatch against the actual rendered DOM are all plausible first-run issues, not necessarily a
plan defect. Fix forward against what the app actually renders; do not weaken an assertion to
make it pass.

- [ ] **Step 8: Confirm nothing else broke**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean (the new `.ts` files under `e2e/` must type-check cleanly; `playwright.config.ts`
and the `e2e/` directory should not need to be excluded from anything for the existing `build`
script to keep working, since `vue-tsc -b`'s project references only apply to the `src/` app
build — verify this assumption holds rather than assuming it).

- [ ] **Step 9: Commit**

```bash
git add web/package.json web/package-lock.json web/playwright.config.ts web/e2e
git commit -m "web: add a real Playwright test harness, starting with Character creation"
```

---

### Task 2: Wire the suite into CI

**Files:**
- Modify: `.github/workflows/ci.yml`

**Interfaces:**
- Consumes: `npm run test:e2e` (Task 1).

- [ ] **Step 1: Add CI steps**

In `.github/workflows/ci.yml`'s `web-build` job, add two new steps immediately after the existing
`npm ci` step (keep every existing step in this job exactly as-is; this only inserts two new ones
between `npm ci` and whatever currently comes next):

```yaml
      - name: install Playwright browser
        working-directory: web
        run: npx playwright install --with-deps chromium

      - name: e2e tests
        working-directory: web
        run: npm run test:e2e
```

- [ ] **Step 2: Verify the workflow file is syntactically valid**

Run: `python3 -c "import yaml, sys; yaml.safe_load(open('.github/workflows/ci.yml'))" && echo OK`
(or any other locally-available YAML validator) — this repo doesn't run its own CI from a local
task, so this syntax check is the practical substitute; the real proof is the next PR's Actions
run, which is outside this plan's ability to trigger.

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: run the new web SPA Playwright suite in the web-build job"
```

---

## Final Verification

- `npm run typecheck && npm run build && npm run test:e2e` (from `web/`) all clean.
- `go build ./... && go vet ./... && go test ./...` unaffected (this plan touches no Go code) —
  re-confirm anyway, since a stray edit is cheap to catch here and expensive to catch later.
- Read the final `.github/workflows/ci.yml` once as a whole file (not just the diff) to confirm
  the new steps sit inside the `web-build` job specifically, not accidentally duplicated into
  `build-test` or left dangling outside any job's `steps:` list.
