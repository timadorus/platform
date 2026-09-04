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

  const modalForm = page.getByTestId('create-character-form')
  await modalForm.getByTestId('character-name-input').fill('Frodo Baggins')
  await page.getByPlaceholder('Search users…').fill('devuser')
  await page.getByTestId('user-picker').getByRole('button', { name: 'devuser@timadorus.local' }).click()
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
  const baseInfo = page.getByTestId('base-info-card')
  await expect(baseInfo.getByText('Frodo Baggins')).toBeVisible()
})
