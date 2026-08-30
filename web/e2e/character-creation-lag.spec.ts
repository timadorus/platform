import { test, expect, type Page } from '@playwright/test'
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
    ...overrides,
  })
}

async function createCharacter(page: Page, name: string) {
  await page.getByRole('button', { name: '+ Create Character' }).click()
  await expect(page.getByRole('heading', { name: 'Create Character' })).toBeVisible()
  const modalForm = page.locator('form')
  await modalForm.locator('input[type="text"]').first().fill(name)
  await page.getByPlaceholder('Search users…').fill('devuser')
  await page.getByRole('button', { name: 'devuser@timadorus.local' }).click()
  await modalForm.getByRole('button', { name: 'Create', exact: true }).click()
  await expect(page.getByRole('heading', { name: 'Create Character' })).toHaveCount(0)
}

test('sidebar lists and the main pane eventually reflect a newly created Character despite read-model lag', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  // ~2 poll intervals (waitForCharacter/waitForCharacterInList/waitForEntityInList all default
  // to a 750ms interval) — long enough to prove the retry actually happens, short enough to keep
  // this test fast.
  const state = seedState({ createVisibilityDelayMs: 1600 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')
  await createCharacter(page, 'Samwise Gamgee')

  const charactersSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Characters' }) })
  await expect(charactersSection.getByText('Samwise Gamgee')).toBeVisible()

  const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
  await expect(entitiesSection.getByRole('button', { name: 'Samwise Gamgee' })).toBeVisible()

  const baseInfo = page.locator('div.rounded-md').filter({ hasText: 'Base Info' })
  await expect(baseInfo.getByText('Samwise Gamgee')).toBeVisible()
})

test('the main pane shows a Retry/Back-to-Campaign timeout state if the Character never becomes visible', async ({
  page,
  context,
  baseURL,
}) => {
  test.setTimeout(45_000)
  const base = baseURL!
  const authority = `${base}/oidc`
  // Far beyond waitForCharacter's own 15s timeout — the Character never becomes visible within
  // this test's lifetime, exercising the "give up and show an error" path.
  const state = seedState({ createVisibilityDelayMs: 999_999_999 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')
  await createCharacter(page, 'Peregrin Took')

  await expect(page.getByText("Couldn't load this Character", { exact: false })).toBeVisible({ timeout: 20_000 })
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()

  const backLink = page.getByRole('link', { name: 'Back to Campaign' })
  await expect(backLink).toBeVisible()
  await backLink.click()
  await expect(page).toHaveURL(/\/universes\/u1\/campaigns\/c1$/)
})
