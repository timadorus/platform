import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(overrides: Partial<MockState> = {}): MockState {
  return createMockState({
    universes: [
      { id: 'u1', name: 'Universe One', isArchived: false },
      { id: 'u2', name: 'Universe Two', isArchived: false },
    ],
    campaigns: [
      { id: 'c1', universeId: 'u1', name: 'Campaign One', rulesetId: 'r1', isArchived: false },
      { id: 'c2', universeId: 'u2', name: 'Campaign Two', rulesetId: 'r1', isArchived: false },
    ],
    rulesets: [{ id: 'r1', name: 'Test Ruleset' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    ...overrides,
  })
}

test('a deep link to a different Universe\'s campaign picker does not corrupt the persisted selection pair', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  // Simulate a prior session that had u1 selected — seedAuth's subject is always 'test-sub'
  // (support/auth.ts), so this is the exact storage key selection.ts's storageKey() computes.
  // Only set this on the first load; don't overwrite it on subsequent page.goto() calls.
  let firstLoad = true
  await context.addInitScript(
    ([key, value]) => {
      if (!window.localStorage.getItem(key)) {
        window.localStorage.setItem(key, value)
      }
    },
    [
      'timadorus:selection:test-sub',
      JSON.stringify({ selectedUniverseId: 'u1', selectedCampaignId: null }),
    ],
  )

  // Deep link straight to u2's campaign picker — skips u1 and the Universe picker entirely, the
  // exact scenario that previously corrupted the persisted pair (BACKLOG.md, Web SPA section).
  await page.goto('/universes/u2')
  await expect(page.getByRole('button', { name: 'Campaign Two' })).toBeVisible()
  await page.getByRole('button', { name: 'Campaign Two' }).click()
  await expect(page).toHaveURL(/\/universes\/u2\/campaigns\/c2$/)

  const stored = await page.evaluate(() => window.localStorage.getItem('timadorus:selection:test-sub'))
  expect(JSON.parse(stored!)).toEqual({ selectedUniverseId: 'u2', selectedCampaignId: 'c2' })

  // Reload from the root and confirm the restored pair is no longer mismatched — before the fix,
  // UniversePickerView would restore u1, route to u1's campaign picker, which would then find
  // existing.universeId ('u2's stored campaign) !== universeId.value ('u1') and clear it.
  await page.goto('/')
  await expect(page).toHaveURL(/\/universes\/u2\/campaigns\/c2$/)
})
