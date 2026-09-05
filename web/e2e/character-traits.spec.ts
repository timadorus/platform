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
  const addTraitRequest = page.waitForRequest(
    (req) => req.method() === 'PUT' && req.url().endsWith('/api/command/characters/ch1/action'),
  )
  await baseInfo.getByRole('button', { name: 'Add', exact: true }).click()

  // 1. the request actually fired, AND with the right payload — asserting only firing (as this
  // test used to) would still pass if BaseInfoTable.vue sent the wrong JSON key (e.g. "traitName"
  // instead of "trait"): the engine fails closed on an unrecognized payload, so every real Add
  // Trait click would silently no-op in production while this whole suite stayed green.
  const request = await addTraitRequest
  expect(request.postDataJSON()).toEqual({ action: 'addTrait', trait: 'strong' })
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

test('an unrelated Character change while an Add Trait request is pending does not revert the still-pending status', async ({
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
  await baseInfo.getByRole('button', { name: 'Add Trait' }).click()
  const select = baseInfo.getByRole('combobox')
  await select.selectOption('strong')
  await baseInfo.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(page.getByText('Update requested — refreshing…')).toBeVisible()

  // An unrelated Character change lands (e.g. a rename) — the Character's traits themselves are
  // UNCHANGED, so CharacterDetailView.vue's silent reload of this event must not be mistaken for
  // confirmation of the still-pending Add Trait, exactly like campaign-configuration.spec.ts's
  // sibling test for the Max Stat Budget save.
  const character = state.characters.find((c) => c.id === 'ch1')!
  character.name = 'Renamed Unrelated'
  state.changes.push({
    globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
    universeId: 'u1',
    aggregateType: 'character',
    aggregateId: 'ch1',
    eventType: 'character.renamed.v1',
    occurredAt: new Date().toISOString(),
  })

  // Give the change-feed poll (5s interval) time to land and be (mis)handled.
  await page.waitForTimeout(6000)

  await expect(page.getByText('Update requested — refreshing…')).toBeVisible()
})

test('an Add Trait request that never gets confirmed times out with an error and re-enables Add Trait', async ({
  page,
  context,
  baseURL,
}) => {
  // The pending-timeout itself is real (ADD_TRAIT_TIMEOUT_MS = 10s in BaseInfoTable.vue) — mirrors
  // campaign-configuration.spec.ts's own identical pattern (and, in turn,
  // character-creation-lag.spec.ts's) of waiting out a real, short, production timeout rather than
  // injecting a test-only one.
  test.setTimeout(30_000)
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')

  const baseInfo = page.getByTestId('base-info-card')
  await baseInfo.getByRole('button', { name: 'Add Trait' }).click()
  const select = baseInfo.getByRole('combobox')
  await select.selectOption('strong')
  await baseInfo.getByRole('button', { name: 'Add', exact: true }).click()
  await expect(page.getByText('Update requested — refreshing…')).toBeVisible()

  // Never push a confirming change — simulates a rejected addTrait (not a Campaign trait, no
  // points remaining, already has it, or an archived Character), all of which the engine logs and
  // silently no-ops on, never emitting a change that would let the traits list catch up.
  await expect(baseInfo.getByText('No confirmation received', { exact: false })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByText('Update requested — refreshing…')).not.toBeVisible()
  await expect(baseInfo.getByRole('button', { name: 'Add Trait' })).toBeVisible()
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
