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
        info: JSON.stringify({
          stats: {
            attributes: {
              ST: { temp: 50, pot: 50, bonus: 0 },
              AG: { temp: 50, pot: 50, bonus: 0 },
              CO: { temp: 60, pot: 65, bonus: 1 },
              QU: { temp: 40, pot: 45, bonus: -1 },
              SD: { temp: 50, pot: 50, bonus: 0 },
              ME: { temp: 50, pot: 50, bonus: 0 },
              RE: { temp: 50, pot: 50, bonus: 0 },
              EM: { temp: 50, pot: 50, bonus: 0 },
              PR: { temp: 50, pot: 50, bonus: 0 },
              IN: { temp: 50, pot: 50, bonus: 0 },
            },
          },
        }),
      },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('the Attributes table shows the Character\'s real seeded Temp/Pot/Bonus values', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')

  const attributesCard = page.getByTestId('attributes-card')
  await expect(attributesCard).toBeVisible()

  await expect(attributesCard.getByTestId('attribute-ST-temp')).toHaveText('50')
  await expect(attributesCard.getByTestId('attribute-ST-pot')).toHaveText('50')
  await expect(attributesCard.getByTestId('attribute-ST-bonus')).toHaveText('+0')

  // A positive bonus.
  await expect(attributesCard.getByTestId('attribute-CO-temp')).toHaveText('60')
  await expect(attributesCard.getByTestId('attribute-CO-pot')).toHaveText('65')
  await expect(attributesCard.getByTestId('attribute-CO-bonus')).toHaveText('+1')

  // A negative bonus — must not gain a stray leading "+".
  await expect(attributesCard.getByTestId('attribute-QU-temp')).toHaveText('40')
  await expect(attributesCard.getByTestId('attribute-QU-pot')).toHaveText('45')
  await expect(attributesCard.getByTestId('attribute-QU-bonus')).toHaveText('-1')
})

test('a Character with no seeded attributes shows placeholders in every Temp/Pot/Bonus cell', async ({ page, context, baseURL }) => {
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
        info: JSON.stringify({ stats: {} }),
      },
    ],
  })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')

  const attributesCard = page.getByTestId('attributes-card')
  for (const abbr of ['ST', 'AG', 'CO', 'QU', 'SD', 'ME', 'RE', 'EM', 'PR', 'IN']) {
    await expect(attributesCard.getByTestId(`attribute-${abbr}-temp`)).toHaveText('—')
    await expect(attributesCard.getByTestId(`attribute-${abbr}-pot`)).toHaveText('—')
    await expect(attributesCard.getByTestId(`attribute-${abbr}-bonus`)).toHaveText('—')
  }
})
