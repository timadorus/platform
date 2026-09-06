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

test('a multi-change poll batch updates every affected sidebar, not just the last change', async ({
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
  const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
  const charactersSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Characters' }) })
  await expect(entitiesSection.getByText('Aragorn')).toBeVisible()

  // Regression for Important #1: creating a Character always emits EntityCreated then
  // CharacterCreated in one unit of work, and both routinely land in the same poll response.
  // Seed both an Entity change and a Character change with different aggregateTypes into the
  // *same* mocked /changes response — Vue's flush:'pre' watch coalesces same-tick writes to a
  // Ref, so without the fix only the last change (Character) would ever reach a watcher and the
  // Entities sidebar would never refresh.
  state.entities.push({ id: 'e2', name: 'Gimli', universeId: 'u1', isArchived: false })
  state.characters.push({
    id: 'ch2',
    name: 'Legolas',
    campaignId: 'c1',
    entityId: 'e3',
    playerUserId: 'user-1',
    isArchived: false,
  })
  state.changes.push(
    {
      globalSeq: 1,
      universeId: 'u1',
      aggregateType: 'entity',
      aggregateId: 'e2',
      eventType: 'entity.created.v1',
      occurredAt: new Date().toISOString(),
    },
    {
      globalSeq: 2,
      universeId: 'u1',
      aggregateType: 'character',
      aggregateId: 'ch2',
      eventType: 'character.created.v1',
      occurredAt: new Date().toISOString(),
    },
  )

  // useChangeFeed polls every 5s — wait comfortably past that instead of asserting immediately.
  // Both assertions must hold: proving only the Character sidebar (the last change in the batch)
  // updated would demonstrate the bug, not the fix.
  await expect(entitiesSection.getByText('Gimli')).toBeVisible({ timeout: 10000 })
  await expect(charactersSection.getByText('Legolas')).toBeVisible({ timeout: 10000 })
})

test('an externally-made Universe rename is picked up by the Universe panel without any local action', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/manage')
  await expect(page.getByRole('heading', { name: 'Test Universe' })).toBeVisible()

  // Simulate another tab/user renaming the Universe: mutate state directly (bypassing the rename
  // command route — this test is about the change feed noticing it, not about renaming itself)
  // and record a matching change-log row the poller will pick up.
  state.universes[0].name = 'Renamed Elsewhere'
  state.changes.push({
    globalSeq: 1,
    universeId: 'u1',
    aggregateType: 'universe',
    aggregateId: 'u1',
    eventType: 'universe.renamed.v1',
    occurredAt: new Date().toISOString(),
  })

  // useChangeFeed polls every 5s — wait comfortably past that instead of asserting immediately.
  await expect(page.getByRole('heading', { name: 'Renamed Elsewhere' })).toBeVisible({ timeout: 10000 })
})
