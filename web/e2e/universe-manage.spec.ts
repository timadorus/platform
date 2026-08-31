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
    creatorIds: ['user-1'],
    characters: [
      { id: 'ch1', name: 'Aragorn', campaignId: 'c1', entityId: 'e1', playerUserId: 'user-1', isArchived: false },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('the Universe panel shows the Creators list and the Campaigns list for the active universe', async ({
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
  await expect(page.getByText('devuser@timadorus.local')).toBeVisible()
  await expect(page.getByRole('button', { name: 'Test Campaign' })).toBeVisible()
})

test('the universe badge navigates to the Universe panel from the workspace, and renaming there updates the header', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await expect(page.getByRole('button', { name: '🌍 Test Universe' })).toBeVisible()

  await page.getByRole('button', { name: '🌍 Test Universe' }).click()
  await expect(page).toHaveURL(/\/universes\/u1\/manage$/)
  await expect(page.getByRole('heading', { name: 'Test Universe' })).toBeVisible()

  await page.getByRole('button', { name: 'Rename', exact: true }).click()
  const nameInput = page.locator('input[type="text"]').first()
  await nameInput.fill('Renamed Universe')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Renamed Universe' })).toBeVisible()

  await expect(page.getByRole('button', { name: '🌍 Renamed Universe' })).toBeVisible()
})

test('clicking a Campaign in the Universe panel navigates into its workspace', async ({
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
  await page.getByRole('button', { name: 'Test Campaign' }).click()

  await expect(page).toHaveURL(/\/universes\/u1\/campaigns\/c1$/)
})
