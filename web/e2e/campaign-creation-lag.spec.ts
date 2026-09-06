import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(overrides: Partial<MockState> = {}): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    rulesets: [{ id: 'r1', name: 'Test Ruleset' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    ...overrides,
  })
}

test('the workspace eventually reflects a newly created Campaign despite read-model lag', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  // waitForCampaign defaults to a 750ms interval, first poll at t≈0 — 1600ms is long enough that
  // the first three attempts miss (t≈0, 750, 1500) and the fourth (t≈2250) succeeds, proving the
  // retry actually happens, mirroring character-creation-lag.spec.ts's identical reasoning.
  const state = seedState({ createVisibilityDelayMs: 1600 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/manage')
  await page.getByRole('button', { name: '+ Create Campaign' }).click()
  // CreateCampaignModal.vue's <label> elements have no for/id pairing with their input/select
  // (a separate, already-tracked BACKLOG item), so getByLabel does not find them — use the same
  // positional/structural locators universe-manage.spec.ts already uses instead. The Gamemaster
  // checkbox is pre-checked by UserMultiSelect.vue for the current user, so a redundant `.check()`
  // is harmless (Playwright's check() is a no-op when already checked).
  const form = page.locator('form')
  await form.getByRole('textbox').first().fill('Laggy Campaign')
  await form.getByRole('combobox').selectOption({ label: 'Test Ruleset' })
  await page.getByRole('checkbox').first().check()
  await page.getByRole('button', { name: 'Create', exact: true }).click()

  // Creation navigates into the new Campaign's workspace immediately, while the read model is
  // still lagging — WorkspaceView's header badge and CampaignOverviewPanel's Manage tab both poll
  // via waitForCampaign, so asserting on the Campaign's name appearing in both proves the retry
  // loop actually resolved rather than the page just rendering a "not found" state that happens
  // to look empty.
  await expect(page.getByRole('button', { name: '🎲 Laggy Campaign' })).toBeVisible({ timeout: 10000 })
  await expect(page.getByRole('heading', { name: 'Laggy Campaign' })).toBeVisible()
})

test('the Campaign panel shows a Retry/Back-to-Universe timeout state if the Campaign never becomes visible', async ({
  page,
  context,
  baseURL,
}) => {
  test.setTimeout(45_000)
  const base = baseURL!
  const authority = `${base}/oidc`
  // Far beyond waitForCampaign's own 15s timeout — the Campaign never becomes visible within this
  // test's lifetime, exercising the "give up and show an error" path.
  const state = seedState({ createVisibilityDelayMs: 999_999_999 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/manage')
  await page.getByRole('button', { name: '+ Create Campaign' }).click()
  const form = page.locator('form')
  await form.getByRole('textbox').first().fill('Never Visible Campaign')
  await form.getByRole('combobox').selectOption({ label: 'Test Ruleset' })
  await page.getByRole('checkbox').first().check()
  await page.getByRole('button', { name: 'Create', exact: true }).click()

  await expect(page.getByText("Couldn't load this Campaign", { exact: false })).toBeVisible({ timeout: 20_000 })
  await expect(page.getByRole('button', { name: 'Retry' })).toBeVisible()

  const backLink = page.getByRole('link', { name: 'Back to Universe' })
  await expect(backLink).toBeVisible()
  await backLink.click()
  await expect(page).toHaveURL(/\/universes\/u1\/manage$/)
})
