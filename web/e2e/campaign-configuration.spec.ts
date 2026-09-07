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
        configuration: JSON.stringify({ characterCreation: { maxStatBudget: 35 } }),
      },
    ],
    rulesets: [{ id: 'r1', name: 'Timadorus' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    creatorIds: ['user-1'],
    ...overrides,
  })
}

test('changing Max Stat Budget sends the setMaxStatBudget action and reflects the updated value once it lands', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  const apiCalls = await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')
  await page.getByRole('tab', { name: 'Configuration' }).click()

  const budgetInput = page.locator('input[type="number"]')
  await expect(budgetInput).toHaveValue('35')

  await budgetInput.fill('45')
  await page.getByRole('button', { name: 'Save' }).click()

  // 1. the request actually fired
  await expect(async () => {
    expect(apiCalls).toContain('PUT /api/command/campaigns/c1/configure')
  }).toPass()

  // 2. pending status shown — the mock's 204 doesn't itself change anything yet
  await expect(page.getByText('Update requested')).toBeVisible()

  // 3. simulate the async engine mutation + change-feed catching up: update the mock's own
  // recorded configuration and inject a change-feed row, exactly like universe-change-feed.spec.ts
  // does for an externally-made change.
  const campaign = state.campaigns.find((c) => c.id === 'c1')!
  campaign.configuration = JSON.stringify({ characterCreation: { maxStatBudget: 45 } })
  state.changes.push({
    globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
    universeId: 'u1',
    aggregateType: 'campaign',
    aggregateId: 'c1',
    eventType: 'campaign.configuration_changed.v1',
    occurredAt: new Date().toISOString(),
  })

  // 4. the panel picks it up via the existing change-feed poll and the pending status clears
  await expect(page.getByText('Update requested')).not.toBeVisible({ timeout: 10000 })
  await expect(budgetInput).toHaveValue('45')
})

test('an unrelated Campaign change while a save is pending does not revert the still-pending input or status', async ({
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
  await page.getByRole('tab', { name: 'Configuration' }).click()

  const budgetInput = page.locator('input[type="number"]')
  await budgetInput.fill('45')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Update requested')).toBeVisible()

  // An unrelated Campaign change lands (e.g. a rename) — configuration itself is UNCHANGED, so
  // this must not be mistaken for confirmation of the pending save.
  const campaign = state.campaigns.find((c) => c.id === 'c1')!
  campaign.name = 'Renamed Unrelated'
  state.changes.push({
    globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
    universeId: 'u1',
    aggregateType: 'campaign',
    aggregateId: 'c1',
    eventType: 'campaign.renamed.v1',
    occurredAt: new Date().toISOString(),
  })

  // Give the change-feed poll (5s interval) time to land and be (mis)handled.
  await page.waitForTimeout(6000)

  await expect(page.getByText('Update requested')).toBeVisible()
  await expect(budgetInput).toHaveValue('45')
})

test('an unrelated Campaign change does not wipe an unsaved Max Stat Budget draft', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')
  await page.getByRole('tab', { name: 'Configuration' }).click()

  const budgetInput = page.locator('input[type="number"]')
  await expect(budgetInput).toHaveValue('35')
  await budgetInput.fill('99') // unsaved draft — Save never clicked

  const campaign = state.campaigns.find((c) => c.id === 'c1')!
  campaign.name = 'Renamed Unrelated'
  state.changes.push({
    globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
    universeId: 'u1',
    aggregateType: 'campaign',
    aggregateId: 'c1',
    eventType: 'campaign.renamed.v1',
    occurredAt: new Date().toISOString(),
  })

  await page.waitForTimeout(6000)

  await expect(budgetInput).toHaveValue('99')
})

test('Max Stat Budget updates when the configuration changes with no pending save in this session', async ({
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
  await page.getByRole('tab', { name: 'Configuration' }).click()

  const budgetInput = page.locator('input[type="number"]')
  await expect(budgetInput).toHaveValue('35')

  // Someone else (another Gamemaster, another tab, an eventually-consistent write that landed
  // after this page's initial load) changes the budget — no Save was clicked in this session, so
  // there is no pending save to confirm.
  const campaign = state.campaigns.find((c) => c.id === 'c1')!
  campaign.configuration = JSON.stringify({ characterCreation: { maxStatBudget: 50 } })
  state.changes.push({
    globalSeq: (state.changes.at(-1)?.globalSeq ?? 0) + 1,
    universeId: 'u1',
    aggregateType: 'campaign',
    aggregateId: 'c1',
    eventType: 'campaign.configuration_changed.v1',
    occurredAt: new Date().toISOString(),
  })

  await expect(budgetInput).toHaveValue('50', { timeout: 10000 })
})

test('a save that never gets confirmed times out with an error and re-enables Save', async ({ page, context, baseURL }) => {
  // The pending-timeout itself is real (10s) — mirrors character-creation-lag.spec.ts's own
  // pattern of waiting out a real, short, production timeout rather than injecting a test-only one.
  test.setTimeout(30_000)
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')
  await page.getByRole('tab', { name: 'Configuration' }).click()

  const budgetInput = page.locator('input[type="number"]')
  await budgetInput.fill('45')
  await page.getByRole('button', { name: 'Save' }).click()
  await expect(page.getByText('Update requested')).toBeVisible()

  // Never push a confirming change — simulates a non-Timadorus Campaign, which silently no-ops
  // on configure and never emits a ConfigurationChanged event.
  await expect(page.getByText('No confirmation received', { exact: false })).toBeVisible({ timeout: 15_000 })
  await expect(page.getByRole('button', { name: 'Save' })).toBeEnabled()
})

test('a non-integer Max Stat Budget is rejected client-side with no request sent', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  const apiCalls = await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/campaigns/c1')
  await page.getByRole('tab', { name: 'Configuration' }).click()

  const budgetInput = page.locator('input[type="number"]')
  await budgetInput.fill('40.5')
  await page.getByRole('button', { name: 'Save' }).click()

  await expect(page.getByText('Max Stat Budget must be a whole number.')).toBeVisible()
  expect(apiCalls).not.toContain('PUT /api/command/campaigns/c1/configure')
})
