import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(overrides: Partial<MockState> = {}): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [
      {
        id: 'c1',
        universeId: 'u1',
        name: 'Test Campaign',
        rulesetId: 'r1',
        isArchived: false,
        configuration: JSON.stringify({ difficulty: 'hard', maxPlayers: 5 }),
      },
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

test('the Manage tab shows the folded-in overview content, and Configuration shows pretty-printed JSON', async ({
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

  await expect(page.getByRole('heading', { name: 'Test Campaign' })).toBeVisible()
  await expect(page.getByText('Ruleset: Test Ruleset')).toBeVisible()
  await expect(page.getByText('devuser@timadorus.local')).toBeVisible()

  // The Characters/Entities/Objects counts (folded in from the old CampaignOverviewPanel) render
  // as a labeled grid — checking the labels is enough to prove the fold-in worked, without a
  // fragile exact-number-anywhere-on-the-page assertion.
  const countsGrid = page.locator('div.grid.grid-cols-3')
  await expect(countsGrid).toContainText('Characters')
  await expect(countsGrid).toContainText('Entities')
  await expect(countsGrid).toContainText('Objects')

  // BaseTabs.vue's tab buttons carry an explicit role="tab" (added in an earlier accessibility
  // fix), which overrides the implicit "button" role — getByRole('button', ...) would not match
  // them.
  await page.getByRole('tab', { name: 'Configuration', exact: true }).click()
  const configPanel = page.locator('div.rounded-md').filter({ hasText: 'Configuration' })
  await expect(configPanel.locator('pre')).toContainText('"difficulty": "hard"')
  await expect(configPanel.locator('pre')).toContainText('"maxPlayers": 5')
})

test('the campaign badge navigates to the Manage tab, and renaming there updates the header', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  // Start on a different route within the workspace, where the header still shows the campaign
  // badge.
  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await expect(page.getByRole('button', { name: '🎲 Test Campaign' })).toBeVisible()

  await page.getByRole('button', { name: '🎲 Test Campaign' }).click()
  await expect(page).toHaveURL(/\/universes\/u1\/campaigns\/c1$/)
  await expect(page.getByRole('heading', { name: 'Test Campaign' })).toBeVisible()

  await page.getByRole('button', { name: 'Rename', exact: true }).click()
  // Scoped to <main>: the sidebar's Entities/Objects search boxes are also
  // `input[type="text"]` and precede <main> in the DOM, so an unscoped `.first()` would grab one
  // of those instead of the rename input.
  const nameInput = page.locator('main').locator('input[type="text"]').first()
  await nameInput.fill('Renamed Campaign')
  await page.getByRole('button', { name: 'Save', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Renamed Campaign' })).toBeVisible()

  // The header badge (driven by WorkspaceView's own campaign fetch) picks up the rename too.
  await expect(page.getByRole('button', { name: '🎲 Renamed Campaign' })).toBeVisible()
})
