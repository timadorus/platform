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

test('a change-feed reload during the initial lag neither strands the panel on "Loading…" nor blanks it once rendered', async ({
  page,
  context,
  baseURL,
}) => {
  test.setTimeout(60_000)
  const base = baseURL!
  const authority = `${base}/oidc`
  // 8000ms is long enough that useChangeFeed's 5s poll interval lands mid-lag, well before the
  // Campaign becomes visible — reproducing the exact race from the final-review fix brief: a
  // background change-feed reload (silent: true) pre-empting the still-in-flight initial
  // (non-silent) load.
  const state = seedState({ createVisibilityDelayMs: 8000 })
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/manage')
  await page.getByRole('button', { name: '+ Create Campaign' }).click()
  const form = page.locator('form')
  await form.getByRole('textbox').first().fill('Laggy Campaign')
  await form.getByRole('combobox').selectOption({ label: 'Test Ruleset' })
  await page.getByRole('checkbox').first().check()
  await page.getByRole('button', { name: 'Create', exact: true }).click()

  // Confirm the workspace has mounted (so useChangeFeed's start() has already fetched its initial
  // cursor) before seeding the matching change below — seeding it any earlier would bake this
  // change into that initial cursor fetch, and the poll would never see it as "new".
  await expect(page.getByText('Loading…')).toBeVisible()
  await page.waitForTimeout(300)

  // This test's own state is fresh (nextId starts at 1) and this is the only create-Campaign
  // command it issues, so the new Campaign is deterministically 'campaign-1'. Push a matching
  // change directly onto state.changes, mirroring universe-change-feed.spec.ts's pattern of
  // simulating an externally-made change for the poller to pick up rather than going through a
  // command route.
  state.changes.push({
    globalSeq: 1,
    universeId: 'u1',
    aggregateType: 'campaign',
    aggregateId: 'campaign-1',
    eventType: 'campaign.created.v1',
    occurredAt: new Date().toISOString(),
  })

  // Regression for Important #1 (a silent reload aborting an in-flight non-silent load and
  // stranding the panel on "Loading…" forever): useChangeFeed's poll (every 5s) picks up the
  // change above well before the 8s visibility delay elapses, firing load({silent:true}) while the
  // original non-silent load is still polling waitForCampaign. Before the fix, that silent load
  // unconditionally aborted the in-flight one, whose own early
  // `if (controller.signal.aborted) return` fired before it ever cleared `loading` — and the
  // silent load itself never touches `loading` — so the panel never left "Loading…". The timeout
  // below covers the 8s visibility delay plus margin for the next 750ms poll attempt.
  await expect(page.getByRole('heading', { name: 'Laggy Campaign' })).toBeVisible({ timeout: 15000 })

  // Regression for Important #2 (a silent reload that times out blanking an already-rendered
  // Campaign): make the now-rendered Campaign stop resolving again (simulating it going briefly
  // unreachable) and fire another matching change-feed change. The resulting silent reload polls
  // for the full 15s default timeout and gives up. Before the fix, `campaign.value = found` ran
  // unconditionally, so this silent timeout (found === null) unconditionally nulled it out,
  // dropping the panel straight to "Campaign not found." even though the already-rendered page
  // itself was never actually invalidated.
  const created = state.campaigns.find((c) => c.id === 'campaign-1')!
  created.visibleAt = Date.now() + 999_999_999
  state.changes.push({
    globalSeq: 2,
    universeId: 'u1',
    aggregateType: 'campaign',
    aggregateId: 'campaign-1',
    eventType: 'campaign.renamed.v1',
    occurredAt: new Date().toISOString(),
  })

  // Outlast the silent reload's full 15s poll-and-give-up window, then confirm the already-
  // rendered page survived it untouched.
  await page.waitForTimeout(16_000)
  await expect(page.getByRole('heading', { name: 'Laggy Campaign' })).toBeVisible()
  await expect(page.getByText('Campaign not found.')).not.toBeVisible()
})
