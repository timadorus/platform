import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'
const ABBRS = ['ST', 'AG', 'CO', 'QU', 'SD', 'ME', 'RE', 'EM', 'PR', 'IN']

function defaultAttributes(overrides: Partial<Record<string, number>> = {}) {
  const attrs: Record<string, { temp: number; pot: number; bonus: number }> = {}
  for (const abbr of ABBRS) {
    attrs[abbr] = { temp: 50, pot: overrides[abbr] ?? 50, bonus: 0 }
  }
  return attrs
}

function seedState(
  opts: { statBudget?: number; attrs?: Partial<Record<string, number>> } = {},
  overrides: Partial<MockState> = {},
): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [
      {
        id: 'c1',
        universeId: 'u1',
        name: 'Test Campaign',
        rulesetId: 'r1',
        isArchived: false,
        configuration: JSON.stringify({ characterCreation: { maxStatBudget: 35 } }),
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
          stats: { traitPoints: 2, traits: [], attributes: defaultAttributes(opts.attrs), statBudget: opts.statBudget ?? 40 },
        }),
      },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('the Assign Stats Budget button only shows when statBudget is greater than 0', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 0 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')

  const attributesCard = page.getByTestId('attributes-card')
  await expect(attributesCard).toBeVisible()
  await expect(attributesCard.getByRole('button', { name: 'Assign Stats Budget' })).not.toBeVisible()
})

test('the button opens a modal listing all 10 attributes’ Pot values and the explanation text', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 40 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()

  const dialog = page.getByRole('dialog')
  await expect(dialog).toBeVisible()
  for (const abbr of ABBRS) {
    await expect(dialog.getByTestId(`assign-pot-${abbr}`)).toHaveValue('50')
  }
  await expect(
    dialog.getByText('Set potential values. Pot ≤ 90 equals 1 budget point per attribute point. 91-95 cost 5 budget points per attribute point.'),
  ).toBeVisible()
})

test('each Pot input has native min/max attributes reflecting its floor and the 95 ceiling', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 40, attrs: { ST: 60 } })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
  const dialog = page.getByRole('dialog')

  // ST's own floor (60) differs from the other nine (50, the default) — confirms `min` is set
  // per-field from each attribute's own starting Pot, not a single shared constant.
  await expect(dialog.getByTestId('assign-pot-ST')).toHaveAttribute('min', '60')
  await expect(dialog.getByTestId('assign-pot-AG')).toHaveAttribute('min', '50')
  for (const abbr of ABBRS) {
    await expect(dialog.getByTestId(`assign-pot-${abbr}`)).toHaveAttribute('max', '95')
  }
})

test('an invalid edit reverts to its last valid value on blur, and Points remaining updates only for valid edits', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 20 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
  const dialog = page.getByRole('dialog')
  const stInput = dialog.getByTestId('assign-pot-ST')
  const remaining = dialog.getByTestId('budget-remaining')
  await expect(remaining).toHaveText('Points remaining: 20')

  // Below the floor (50) — reverts.
  await stInput.fill('40')
  await stInput.blur()
  await expect(stInput).toHaveValue('50')
  await expect(remaining).toHaveText('Points remaining: 20')

  // Above the 95 ceiling — reverts.
  await stInput.fill('96')
  await stInput.blur()
  await expect(stInput).toHaveValue('50')

  // A valid increase (50 -> 60) costs 10, leaving 10.
  await stInput.fill('60')
  await stInput.blur()
  await expect(stInput).toHaveValue('60')
  await expect(remaining).toHaveText('Points remaining: 10')

  // 60 -> 71 would cost 11, exceeding the 10 remaining — reverts to the last VALID value (60),
  // not all the way back to the original floor (50).
  await stInput.fill('71')
  await stInput.blur()
  await expect(stInput).toHaveValue('60')
  await expect(remaining).toHaveText('Points remaining: 10')
})

test('Cancel closes the modal without sending a request', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 40 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  const apiCalls = await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByTestId('assign-pot-ST').fill('60')
  await dialog.getByTestId('assign-pot-ST').blur()

  await dialog.getByRole('button', { name: 'Cancel' }).click()
  await expect(dialog).not.toBeVisible()
  expect(apiCalls).not.toContain('PUT /api/command/characters/ch1/action')
})

test('Submit sends the submitPot payload with all 10 abbreviations, shows pending state, and closes once confirmed', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 40 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  const apiCalls = await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByTestId('assign-pot-ST').fill('60')
  await dialog.getByTestId('assign-pot-ST').blur()

  const submitRequest = page.waitForRequest(
    (req) => req.method() === 'PUT' && req.url().endsWith('/api/command/characters/ch1/action'),
  )
  await dialog.getByRole('button', { name: 'Submit' }).click()

  // Asserting only "fired" would still pass with a wrong JSON shape (e.g. a flattened payload
  // instead of the nested `pot` object) — the engine fails closed on an unrecognized payload, so
  // every real Submit would silently no-op in production while this whole suite stayed green.
  const request = await submitRequest
  expect(request.postDataJSON()).toEqual({
    action: 'submitPot',
    pot: { ST: 60, AG: 50, CO: 50, QU: 50, SD: 50, ME: 50, RE: 50, EM: 50, PR: 50, IN: 50 },
  })
  await expect(async () => {
    expect(apiCalls).toContain('PUT /api/command/characters/ch1/action')
  }).toPass()

  await expect(dialog.getByText('Update requested — refreshing…')).toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Submit' })).toBeDisabled()
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeDisabled()

  // Simulate the async engine mutation + change-feed catching up, exactly like
  // character-traits.spec.ts does for the sibling Add Trait flow.
  const character = state.characters.find((c) => c.id === 'ch1')!
  const info = JSON.parse(character.info)
  info.stats.attributes.ST.pot = 60
  info.stats.statBudget = 30
  character.info = JSON.stringify(info)
  state.changes.push({
    globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
    universeId: 'u1',
    aggregateType: 'character',
    aggregateId: 'ch1',
    eventType: 'character.action_applied.v1',
    occurredAt: new Date().toISOString(),
  })

  await expect(dialog).not.toBeVisible({ timeout: 10000 })
  await expect(page.getByTestId('attribute-ST-pot')).toHaveText('60')
})

test('a submitPot request that never gets confirmed times out with an error and re-enables Cancel/Submit', async ({
  page,
  context,
  baseURL,
}) => {
  // The pending-timeout itself is real (SUBMIT_POT_TIMEOUT_MS = 10s in AttributesTable.vue) —
  // mirrors character-traits.spec.ts's own identical pattern for Add Trait.
  test.setTimeout(30_000)
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 40 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByTestId('assign-pot-ST').fill('60')
  await dialog.getByTestId('assign-pot-ST').blur()
  await dialog.getByRole('button', { name: 'Submit' }).click()
  await expect(dialog.getByText('Update requested — refreshing…')).toBeVisible()

  // Never push a confirming change — simulates a rejected submitPot (insufficient budget, an
  // out-of-range target, or an archived Character), all of which the engine logs and silently
  // no-ops on, never emitting a change that would let the attributes catch up.
  await expect(dialog.getByText('No confirmation received', { exact: false })).toBeVisible({ timeout: 15_000 })
  await expect(dialog.getByText('Update requested — refreshing…')).not.toBeVisible()
  await expect(dialog.getByRole('button', { name: 'Submit' })).toBeEnabled()
  await expect(dialog.getByRole('button', { name: 'Cancel' })).toBeEnabled()
})

test('the modal\'s close (✕) button is a no-op while a submit is pending', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 40 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
  const dialog = page.getByRole('dialog')
  await dialog.getByTestId('assign-pot-ST').fill('60')
  await dialog.getByTestId('assign-pot-ST').blur()
  await dialog.getByRole('button', { name: 'Submit' }).click()
  await expect(dialog.getByText('Update requested — refreshing…')).toBeVisible()

  // BaseModal's ✕ button fires the same `close` event a backdrop click would — neither should be
  // able to dismiss the modal while a submit is pending (see AssignStatsBudgetModal.vue's onClose
  // guard). Clicking it here must leave the dialog open and the request still pending.
  await dialog.getByRole('button', { name: 'Close' }).click()
  await expect(dialog).toBeVisible()
  await expect(dialog.getByText('Update requested — refreshing…')).toBeVisible()
})

test('pressing Enter in a Pot field moves focus to the next attribute, wrapping from Intuition to Strength', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ statBudget: 40 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
  const dialog = page.getByRole('dialog')

  // ST -> AG follows ATTRIBUTES order.
  await dialog.getByTestId('assign-pot-ST').click()
  await dialog.getByTestId('assign-pot-ST').press('Enter')
  await expect(dialog.getByTestId('assign-pot-AG')).toBeFocused()

  // IN is the last attribute in ATTRIBUTES order — Enter there wraps back to ST, the first.
  await dialog.getByTestId('assign-pot-IN').click()
  await dialog.getByTestId('assign-pot-IN').press('Enter')
  await expect(dialog.getByTestId('assign-pot-ST')).toBeFocused()
})
