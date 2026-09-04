import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(overrides: Partial<MockState> = {}): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [{ id: 'c1', universeId: 'u1', name: 'Test Campaign', rulesetId: 'r1', isArchived: false }],
    rulesets: [{ id: 'r1', name: 'Test Ruleset' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    gamemasterIds: ['user-1'],
    characters: [
      {
        id: 'ch1',
        name: 'Aragorn',
        campaignId: 'c1',
        entityId: 'e1',
        playerUserId: 'user-1',
        isArchived: false,
        info: JSON.stringify({ actions: [], maxStatBudget: 40 }),
      },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('the Character detail page has a Configuration tab showing the pretty-printed info field', async ({
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

  const baseInfo = page.getByTestId('base-info-card')
  await expect(baseInfo.getByText('Aragorn')).toBeVisible()

  // BaseTabs.vue's tab buttons carry an explicit role="tab", overriding the implicit "button"
  // role — getByRole('button', ...) would not match them (matches campaign-manage.spec.ts's own
  // established convention for the sibling Configuration tab on Campaign).
  await page.getByRole('tab', { name: 'Configuration', exact: true }).click()
  const configPanel = page.locator('div.rounded-md').filter({ hasText: 'Configuration' })
  await expect(configPanel.locator('pre')).toContainText('"maxStatBudget": 40')
})

test('a Character with no info shows the empty-configuration message', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({
    characters: [{ id: 'ch1', name: 'Aragorn', campaignId: 'c1', entityId: 'e1', playerUserId: 'user-1', isArchived: false }],
  })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByRole('tab', { name: 'Configuration', exact: true }).click()

  const configPanel = page.locator('div.rounded-md').filter({ hasText: 'Configuration' })
  await expect(configPanel.getByText('No configuration set yet.')).toBeVisible()
})
