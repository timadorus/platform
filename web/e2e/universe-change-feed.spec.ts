import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(overrides: Partial<MockState> = {}): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [
      { id: 'c1', universeId: 'u1', name: 'Test Campaign', rulesetId: 'r1', isArchived: false },
    ],
    rulesets: [{ id: 'r1', name: 'Test Ruleset' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    gamemasterIds: ['user-1'],
    characters: [
      { id: 'ch1', name: 'Aragorn', campaignId: 'c1', entityId: 'e1', playerUserId: 'user-1', isArchived: false },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('an externally-made Entity change is picked up by the Entities sidebar without any local action', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')
  // Scoped to the Entities sidebar (not a bare page.getByText('Aragorn')): the seed also has a
  // Character named Aragorn, whose own sidebar entry renders as "Aragorn (NPC)" — an unscoped
  // text locator matches both and fails Playwright's strict mode. This test is specifically
  // about the Entities sidebar, so scoping to it is also the more precise assertion.
  const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
  await expect(entitiesSection.getByText('Aragorn')).toBeVisible()

  // Simulate another tab/user creating a new Entity in this Universe: add it to state directly
  // (bypassing any command route — this test is about the change feed noticing it, not about
  // creation itself) and record a matching change-log row the poller will pick up.
  state.entities.push({ id: 'e2', name: 'Gandalf', universeId: 'u1', isArchived: false })
  state.changes.push({
    globalSeq: 1,
    universeId: 'u1',
    aggregateType: 'entity',
    aggregateId: 'e2',
    eventType: 'entity.created.v1',
    occurredAt: new Date().toISOString(),
  })

  // useChangeFeed polls every 5s — wait comfortably past that instead of asserting immediately.
  await expect(entitiesSection.getByText('Gandalf')).toBeVisible({ timeout: 10000 })
})
