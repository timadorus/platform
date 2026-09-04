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
  // Optional so existing seeds (character-creation.spec.ts, character-creation-lag.spec.ts)
  // don't need to change — ConfigurationPanel.vue already renders "No configuration set yet"
  // when this is absent.
  configuration?: string
  // Optional for the same reason as configuration above — existing seeds that construct a
  // MockCampaign directly (rather than via the POST route below) don't carry this. The POST
  // route always sets it, so a test asserting against a just-created campaign can rely on it.
  gamemasterUserIds?: string[]
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
  // visibleAt (epoch ms) simulates read-model lag: unset means "always visible" (the default,
  // matching every existing test's expectations); set means query routes hide this record until
  // Date.now() reaches it, even though it already exists in `state`.
  visibleAt?: number
}

export interface MockEntity {
  id: string
  name: string
  universeId: string
  isArchived: boolean
  visibleAt?: number
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
  creatorIds: string[]
  nextId: number
  // createVisibilityDelayMs, when set, makes the create-Character command's new Character and
  // Entity invisible to every query route that checks `visibleAt` for this many milliseconds
  // after creation — simulating an async projector that hasn't caught up yet. Unset (the
  // default) means immediately visible, matching every existing test's expectations.
  createVisibilityDelayMs?: number
  // changes simulates externally-made changes (another tab/user) for universe-change-feed.spec.ts
  // — tests push onto this array directly; the mock routes below serve from it exactly like a
  // real universe_changes_read_model would.
  changes: { globalSeq: number; universeId: string; aggregateType: string; aggregateId: string; eventType: string; occurredAt: string }[]
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
    creatorIds: [],
    nextId: 1,
    changes: [],
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
//
// Route coverage grows with each test that needs a new endpoint; see the `matchPath` arms below
// for the current inventory rather than trusting an enumeration here to stay in sync with it.
// Adding a new page/flow to this harness is meant to be a small, additive change — a new
// `matchPath` arm following the existing pattern, not a rewrite — but it does need to happen
// before a test exercising that page can pass: unmocked GETs now silently return `[]` (by design,
// see below), and unmocked commands now fail loudly with a 501 (see the fallback below).
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
    if (method === 'GET' && matchPath('/api/query/universes/:universeId/creators', p)) {
      return json(route, state.creatorIds)
    }
    if (method === 'GET' && (m = matchPath('/api/query/universes/:universeId/campaigns', p))) {
      const matches = state.campaigns.filter((c) => c.universeId === m!.params.universeId && !c.isArchived)
      return json(route, matches)
    }
    if (method === 'GET' && (m = matchPath('/api/query/universes/:universeId/changes/cursor', p))) {
      const relevant = state.changes.filter((c) => c.universeId === m!.params.universeId)
      const cursor = relevant.length ? Math.max(...relevant.map((c) => c.globalSeq)) : 0
      return json(route, { globalSeq: cursor })
    }
    if (method === 'GET' && (m = matchPath('/api/query/universes/:universeId/changes', p))) {
      const since = Number(query.get('since') ?? '0')
      const matches = state.changes
        .filter((c) => c.universeId === m!.params.universeId && c.globalSeq > since)
        .sort((a, b) => a.globalSeq - b.globalSeq)
        .slice(0, 20)
      return json(route, matches)
    }
    if (method === 'GET' && (m = matchPath('/api/query/campaigns/:campaignId', p))) {
      const campaign = state.campaigns.find((c) => c.id === m!.params.campaignId)
      return campaign ? json(route, campaign) : json(route, { title: 'not found' }, 404)
    }
    if (method === 'GET' && matchPath('/api/query/campaigns/:campaignId/gamemasters', p)) {
      return json(route, state.gamemasterIds)
    }
    if (method === 'GET' && (m = matchPath('/api/query/campaigns/:campaignId/characters', p))) {
      const visible = state.characters.filter(
        (c) =>
          c.campaignId === m!.params.campaignId &&
          !c.isArchived &&
          (c.visibleAt === undefined || c.visibleAt <= Date.now()),
      )
      return json(route, visible)
    }
    if (method === 'GET' && (m = matchPath('/api/query/characters/:characterId', p))) {
      const character = state.characters.find((c) => c.id === m!.params.characterId)
      const visible = character && (character.visibleAt === undefined || character.visibleAt <= Date.now())
      return visible ? json(route, character) : json(route, { title: 'not found' }, 404)
    }
    if (method === 'GET' && (m = matchPath('/api/query/universes/:universeId/entities', p))) {
      const name = query.get('name')?.toLowerCase() ?? ''
      const matches = state.entities.filter(
        (e) =>
          e.universeId === m!.params.universeId &&
          !e.isArchived &&
          (!name || e.name.toLowerCase().includes(name)) &&
          (e.visibleAt === undefined || e.visibleAt <= Date.now()),
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
    if (method === 'GET' && p === '/api/query/rulesets') {
      return json(route, state.rulesets)
    }

    // ---- command API ----
    if (method === 'PATCH' && (m = matchPath('/api/command/campaigns/:campaignId', p))) {
      const body = JSON.parse(req.postData() || '{}') as { name: string }
      const campaign = state.campaigns.find((c) => c.id === m!.params.campaignId)
      if (campaign) campaign.name = body.name
      return route.fulfill({ status: 204, body: '' })
    }

    // Mirrors the real backend's fire-and-forget semantics: the actual endpoint queues an async
    // engine mutation and returns before it lands, so this mock only accepts the request — it
    // deliberately does NOT touch `state.campaigns` here. A test that wants to see the eventual
    // effect updates `state.campaigns`/`state.changes` itself, exactly like
    // universe-change-feed.spec.ts already does for an externally-made change.
    if (method === 'PUT' && matchPath('/api/command/campaigns/:campaignId/configure', p)) {
      return route.fulfill({ status: 204, body: '' })
    }

    // Unlike the real backend, which rejects an empty name with a 422 (ErrNameRequired, see
    // internal/domain/universe/universe.go), this mock route accepts any body including an empty
    // name — do not rely on it as a validation oracle for that case.
    if (method === 'PATCH' && (m = matchPath('/api/command/universes/:universeId', p))) {
      const body = JSON.parse(req.postData() || '{}') as { name: string }
      const universe = state.universes.find((u) => u.id === m!.params.universeId)
      if (universe) universe.name = body.name
      return route.fulfill({ status: 204, body: '' })
    }

    // Unlike the real backend, which rejects a blank name or an empty gamemasterUserIds list with
    // a 422 (ErrNameRequired / ErrGamemastersRequired, see internal/domain/campaign/campaign.go's
    // New()) and rejects an unknown universe/ruleset/user with a 404, this mock route accepts any
    // body — do not rely on it as a validation oracle for those cases.
    if (method === 'POST' && (m = matchPath('/api/command/universes/:universeId/campaigns', p))) {
      const body = JSON.parse(req.postData() || '{}') as {
        name: string
        rulesetId: string
        gamemasterUserIds: string[]
      }
      const campaignId = newId(state, 'campaign')
      state.campaigns.push({
        id: campaignId,
        universeId: m!.params.universeId,
        name: body.name,
        rulesetId: body.rulesetId,
        gamemasterUserIds: body.gamemasterUserIds,
        isArchived: false,
      })
      return json(route, { id: campaignId }, 201)
    }

    if (method === 'POST' && (m = matchPath('/api/command/campaigns/:campaignId/characters', p))) {
      const body = JSON.parse(req.postData() || '{}') as { name: string; playerUserId: string }
      const characterId = newId(state, 'character')
      const entityId = newId(state, 'entity')
      const campaign = state.campaigns.find((c) => c.id === m!.params.campaignId)
      // visibleAt simulates real read-model lag: when createVisibilityDelayMs is configured, the
      // new Character/Entity exist in `state` immediately (matching the real backend's write-side
      // truth) but don't appear through any query route until that many milliseconds have passed
      // — exactly what a slow projector looks like from the SPA's perspective.
      const visibleAt = state.createVisibilityDelayMs !== undefined ? Date.now() + state.createVisibilityDelayMs : undefined
      state.characters.push({
        id: characterId,
        name: body.name,
        campaignId: m!.params.campaignId,
        entityId,
        playerUserId: body.playerUserId,
        isArchived: false,
        visibleAt,
      })
      // Mirrors internal/command/character/service.go's real cross-aggregate CreateCharacter:
      // the auto-created Entity gets the same name as the Character.
      state.entities.push({
        id: entityId,
        name: body.name,
        universeId: campaign?.universeId ?? '',
        isArchived: false,
        visibleAt,
      })
      return json(route, { characterId, entityId }, 201)
    }

    if (method === 'GET') {
      console.warn(`[mockBackend] unhandled ${method} ${p} -> []`)
      return json(route, [])
    }
    // Non-GET (command) requests fail loudly instead of silently succeeding: a future test
    // hitting an unmocked command should fail at the actual gap (a missing matchPath arm), not
    // several confusing steps later with an undefined id or similar.
    console.warn(`[mockBackend] unhandled ${method} ${p} -> 501`)
    return json(route, { error: `mockBackend: no handler for ${method} ${p}` }, 501)
  })

  return apiCalls
}
