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
        configuration: JSON.stringify({ traits: ['strong', 'agile', 'quick'] }),
      },
    ],
    rulesets: [{ id: 'r1', name: 'Timadorus' }],
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
        info: JSON.stringify({ stats: { traitPoints: 2, traits: ['agile'] } }),
      },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('the traits row shows the held traits and an Add Trait control whose picker excludes them, and a full submit/pending/confirm round trip updates it', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  const apiCalls = await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')

  const baseInfo = page.getByTestId('base-info-card')
  await expect(baseInfo.getByText('agile', { exact: true })).toBeVisible()
  const addTraitButton = baseInfo.getByRole('button', { name: 'Add Trait' })
  await expect(addTraitButton).toBeVisible()

  // The picker must exclude the already-held trait ("agile") and offer only the remaining
  // Campaign-configured ones.
  await addTraitButton.click()
  const select = baseInfo.getByRole('combobox')
  const optionValues = await select.locator('option').evaluateAll((opts) => opts.map((o) => (o as HTMLOptionElement).value))
  expect(optionValues.filter(Boolean)).toEqual(['strong', 'quick'])

  await select.selectOption('strong')
  await baseInfo.getByRole('button', { name: 'Add', exact: true }).click()

  // 1. the request actually fired with the right payload
  await expect(async () => {
    expect(apiCalls).toContain('PUT /api/command/characters/ch1/action')
  }).toPass()

  // 2. pending status shown — the mock's 204 doesn't itself change anything yet
  await expect(page.getByText('Update requested — refreshing…')).toBeVisible()

  // 3. simulate the async engine mutation + change-feed catching up, exactly like
  // campaign-configuration.spec.ts does for the sibling Max Stat Budget flow.
  const character = state.characters.find((c) => c.id === 'ch1')!
  character.info = JSON.stringify({ stats: { traitPoints: 1, traits: ['agile', 'strong'] } })
  state.changes.push({
    globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
    universeId: 'u1',
    aggregateType: 'character',
    aggregateId: 'ch1',
    eventType: 'character.action_applied.v1',
    occurredAt: new Date().toISOString(),
  })

  // 4. the panel picks it up via the change-feed poll and the pending status clears
  await expect(page.getByText('Update requested — refreshing…')).not.toBeVisible({ timeout: 10000 })
  await expect(baseInfo.getByText('agile, strong', { exact: true })).toBeVisible()
})

test('a Character with no traitPoints left shows no Add Trait button', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({
    characters: [
      {
        id: 'ch1',
        name: 'Aragorn',
        campaignId: 'c1',
        entityId: 'e1',
        playerUserId: 'user-1',
        isArchived: false,
        info: JSON.stringify({ stats: { traitPoints: 0, traits: ['agile'] } }),
      },
    ],
  })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')

  const baseInfo = page.getByTestId('base-info-card')
  await expect(baseInfo.getByText('agile', { exact: true })).toBeVisible()
  await expect(baseInfo.getByRole('button', { name: 'Add Trait' })).not.toBeVisible()
})

test('a Character with no traits selected shows the empty-traits placeholder', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({
    characters: [
      {
        id: 'ch1',
        name: 'Aragorn',
        campaignId: 'c1',
        entityId: 'e1',
        playerUserId: 'user-1',
        isArchived: false,
        info: JSON.stringify({ stats: { traitPoints: 2, traits: [] } }),
      },
    ],
  })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')

  const baseInfo = page.getByTestId('base-info-card')
  await expect(baseInfo.getByText('(no traits selected)', { exact: true })).toBeVisible()
  await expect(baseInfo.getByRole('button', { name: 'Add Trait' })).toBeVisible()
})
