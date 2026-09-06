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
  // Unscoped on purpose: safe today only because this fires before UserPicker's own search box
  // (also type="text") is ever mounted in this test (its "+ Add" toggle is never clicked). A
  // future test in this file that opens "+ Add" without also being in rename mode would need to
  // scope this locator instead of relying on .first().
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

test('creating a Campaign from the Universe panel navigates into its workspace', async ({
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

  await page.getByRole('button', { name: '+ Create Campaign' }).click()
  const dialog = page.getByRole('dialog', { name: 'Create Campaign' })
  await expect(dialog).toBeVisible()
  await dialog.getByLabel('Name').fill('New Campaign')
  await dialog.getByLabel('Ruleset').selectOption({ label: 'Test Ruleset' })
  const gamemasterGroup = dialog.getByRole('group', { name: 'Gamemasters (at least one)' })
  await expect(gamemasterGroup.getByRole('checkbox').first()).toBeChecked()
  await page.getByRole('button', { name: 'Create', exact: true }).click()

  await expect(page).toHaveURL(/\/universes\/u1\/campaigns\/[^/]+$/)
  // Proves content actually rendered inside the new workspace, not just that the URL changed —
  // regression coverage for the router.push({name:'workspace'}) vs. {name:'campaign-overview'}
  // bug (see UniverseOverviewPanel.vue's goToCampaign comment): pushing the parent 'workspace'
  // route by name resolves `matched` to [workspace] only, leaving the nested <router-view> (this
  // heading) permanently empty even though the URL is identical to the fixed behavior.
  await expect(page.getByRole('heading', { name: 'New Campaign' })).toBeVisible()

  // The checkbox assertion above only proves UserMultiSelect ticked a box in the DOM, not that
  // the selected user id actually reached the request body — the mock would 201 just as happily
  // on gamemasterUserIds: [], which the real backend rejects with 422. Assert the mock's
  // recorded state directly to close that gap.
  expect(state.campaigns.at(-1)).toMatchObject({
    name: 'New Campaign',
    rulesetId: 'r1',
    universeId: 'u1',
    gamemasterUserIds: ['user-1'],
  })
})
