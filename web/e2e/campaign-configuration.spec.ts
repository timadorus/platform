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
