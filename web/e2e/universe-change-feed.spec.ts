import { test, expect } from '@playwright/test'
import { createMockState, installMockBackend, type MockState } from './support/mockBackend'
import { startMockRealtimeStream } from './support/mockRealtimeStream'
import { seedAuth } from './support/auth'

const CLIENT_ID = 'test-client'

function seedState(overrides: Partial<MockState> = {}): MockState {
  return createMockState({
    universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
    campaigns: [
      { id: 'c1', universeId: 'u1', name: 'Test Campaign', rulesetId: 'r1', isArchived: false },
    ],
    rulesets: [{ id: 'r1', name: 'Test Ruleset' }],
    users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
    gamemasterIds: ['user-1'],
    characters: [
      { id: 'ch1', name: 'Aragorn', campaignId: 'c1', entityId: 'e1', playerUserId: 'user-1', isArchived: false },
    ],
    entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
    ...overrides,
  })
}

test('a live Entity change is picked up by the Entities sidebar without any local action or reload', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  const realtime = await startMockRealtimeStream()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

  await page.goto('/universes/u1/campaigns/c1')
  const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
  await expect(entitiesSection.getByText('Aragorn')).toBeVisible()

  // Simulate another tab/user creating a new Entity in this Universe: add it to state (so the
  // panel's own re-search finds it) and push the live SSE frame directly — no poll, no wait.
  state.entities.push({ id: 'e2', name: 'Gandalf', universeId: 'u1', isArchived: false })
  realtime.push({
    globalSeq: 1,
    aggregateType: 'entity',
    aggregateId: 'e2',
    eventType: 'entity.created.v1',
    occurredAt: new Date().toISOString(),
  })

  await expect(entitiesSection.getByText('Gandalf')).toBeVisible()
  await realtime.close()
})

test('a batch of live changes updates every affected sidebar, not just the last one', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  const realtime = await startMockRealtimeStream()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

  await page.goto('/universes/u1/campaigns/c1')
  const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
  const charactersSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Characters' }) })
  await expect(entitiesSection.getByText('Aragorn')).toBeVisible()

  // Regression for the original polling-era bug this file's history documents (Important #1):
  // creating a Character emits Entity+Character events together. Push both frames back to
  // back — Vue's flush:'pre' watch coalesces same-tick writes to a Ref, so an implementation
  // that only reacted to the LAST applied change (rather than each one via onmessage firing
  // per frame) would only refresh the Character sidebar, not the Entity one.
  state.entities.push({ id: 'e2', name: 'Gimli', universeId: 'u1', isArchived: false })
  state.characters.push({
    id: 'ch2',
    name: 'Legolas',
    campaignId: 'c1',
    entityId: 'e3',
    playerUserId: 'user-1',
    isArchived: false,
  })
  realtime.push({
    globalSeq: 1,
    aggregateType: 'entity',
    aggregateId: 'e2',
    eventType: 'entity.created.v1',
    occurredAt: new Date().toISOString(),
  })
  realtime.push({
    globalSeq: 2,
    aggregateType: 'character',
    aggregateId: 'ch2',
    eventType: 'character.created.v1',
    occurredAt: new Date().toISOString(),
  })

  await expect(entitiesSection.getByText('Gimli')).toBeVisible()
  await expect(charactersSection.getByText('Legolas')).toBeVisible()
  await realtime.close()
})

test('a live Universe rename is picked up by the Universe panel without any local action or reload', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  const realtime = await startMockRealtimeStream()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

  await page.goto('/universes/u1/manage')
  await expect(page.getByRole('heading', { name: 'Test Universe' })).toBeVisible()

  // Open the Add Creator UserPicker before triggering the reload: this component lives inside
  // the template's `v-if="loading"` gate, so it would be unmounted (destroying its own
  // query-input state) if the change-driven reload below were not silent.
  await page.getByRole('button', { name: '+ Add' }).click()
  const userPicker = page.getByTestId('user-picker')
  await expect(userPicker).toBeVisible()

  state.universes[0].name = 'Renamed Elsewhere'
  realtime.push({
    globalSeq: 1,
    aggregateType: 'universe',
    aggregateId: 'u1',
    eventType: 'universe.renamed.v1',
    occurredAt: new Date().toISOString(),
  })

  await expect(page.getByRole('heading', { name: 'Renamed Elsewhere' })).toBeVisible()
  await expect(userPicker).toBeVisible()
  await realtime.close()
})

test('the Universe picker refreshes live when a Universe is created elsewhere', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ universes: [] }) // empty: the real UniversePickerView renders its
  // grid immediately (no stored selection to redirect through) exactly when there's nothing to
  // redirect to — see UniversePickerView.vue's own onMounted logic.
  const realtime = await startMockRealtimeStream()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

  await page.goto('/')
  await expect(page.getByRole('heading', { name: 'Choose a Universe' })).toBeVisible()

  // This bare-picker route renders almost instantly (no slow Universe/Campaign fetch stands
  // between page.goto() and here, unlike every other test in this file) — wait for the mock's
  // own EventSource handshake to actually land before pushing, or the frame below could be sent
  // to zero connected clients and simply lost. See waitForConnection's own comment.
  await realtime.waitForConnection()
  state.universes.push({ id: 'u9', name: 'New Universe', isArchived: false })
  realtime.push({
    globalSeq: 1,
    aggregateType: 'universe',
    aggregateId: 'u9',
    eventType: 'universe.created.v1',
    occurredAt: new Date().toISOString(),
  })

  await expect(page.getByRole('button', { name: 'New Universe' })).toBeVisible()
  await realtime.close()
})

test('the Campaign picker refreshes live when a Campaign is created elsewhere', async ({ page, context, baseURL }) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({ campaigns: [] })
  const realtime = await startMockRealtimeStream()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

  await page.goto('/universes/u1')
  await expect(page.getByRole('heading', { name: 'Choose a Campaign' })).toBeVisible()

  // Same rationale as the Universe-picker test above — this route also renders almost instantly.
  await realtime.waitForConnection()
  state.campaigns.push({ id: 'c9', universeId: 'u1', name: 'New Campaign', rulesetId: 'r1', isArchived: false })
  realtime.push({
    globalSeq: 1,
    aggregateType: 'campaign',
    aggregateId: 'c9',
    eventType: 'campaign.created.v1',
    occurredAt: new Date().toISOString(),
  })

  await expect(page.getByRole('button', { name: 'New Campaign' })).toBeVisible()
  await realtime.close()
})

test('a live character.info_changed.v1 update refreshes an open Character detail page without a reload', async ({
  page,
  context,
  baseURL,
}) => {
  // This is the exact scenario named in the original report this whole feature exists to fix:
  // "incremental changes like the results of the hooks from adding traits are not picked up."
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({
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
  })
  const realtime = await startMockRealtimeStream()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  const baseInfo = page.getByTestId('base-info-card')
  await expect(baseInfo.getByText('agile', { exact: true })).toBeVisible()

  // Simulate a trait-hook side effect landing with no local action taken on this page at all —
  // a different tab/session triggered it entirely.
  const character = state.characters.find((c) => c.id === 'ch1')!
  character.info = JSON.stringify({ stats: { traitPoints: 1, traits: ['agile', 'strong'] } })
  realtime.push({
    globalSeq: 1,
    aggregateType: 'character',
    aggregateId: 'ch1',
    eventType: 'character.info_changed.v1',
    occurredAt: new Date().toISOString(),
  })

  await expect(baseInfo.getByText('agile, strong', { exact: true })).toBeVisible()
  await realtime.close()
})

test('a live change to a DIFFERENT Character does not reload an open, unrelated Character detail page', async ({
  page,
  context,
  baseURL,
}) => {
  // Proves the backend-side aggregateId filter (Task 8's watchAggregate({type:'character',
  // aggregateId})) genuinely discriminates — not just "any character event reaches this page
  // and it happens to render the same thing anyway". Success is IN-visibility of a marker this
  // page would show if (incorrectly) reloaded.
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState({
    characters: [
      { id: 'ch1', name: 'Aragorn', campaignId: 'c1', entityId: 'e1', playerUserId: 'user-1', isArchived: false },
      { id: 'ch2', name: 'Legolas', campaignId: 'c1', entityId: 'e2', playerUserId: 'user-1', isArchived: false },
    ],
    entities: [
      { id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false },
      { id: 'e2', name: 'Legolas', universeId: 'u1', isArchived: false },
    ],
  })
  const realtime = await startMockRealtimeStream()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

  await page.goto('/universes/u1/campaigns/c1/characters/ch1')
  const baseInfo = page.getByTestId('base-info-card')
  await expect(baseInfo.getByText('Aragorn', { exact: true })).toBeVisible()

  // Rename the OTHER Character and push its event — if this page's own watch clause were not
  // actually scoped to ch1's aggregateId (e.g. a regression back to "any character in this
  // campaign"), CharacterDetailView would silently reload and start showing Legolas's data.
  const other = state.characters.find((c) => c.id === 'ch2')!
  other.name = 'Renamed Legolas'
  realtime.push({
    globalSeq: 1,
    aggregateType: 'character',
    aggregateId: 'ch2',
    eventType: 'character.renamed.v1',
    occurredAt: new Date().toISOString(),
  })

  // Generous settle window with nothing to wait ON (no marker of the bad outcome exists to poll
  // for) — this is an absence assertion, so a fixed wait is the correct tool here even though
  // it's slower than the rest of this file's push-and-immediately-assert tests.
  //
  // Scoped to the `<main>` content area (CharacterDetailView's own subtree), not the bare page:
  // this route renders inside WorkspaceView, whose CharactersPanel sidebar legitimately watches
  // `{ type: 'character', campaignId }` (a broader, intentional scope covering every Character in
  // the campaign — see CharactersPanel.vue) and correctly re-renders "Renamed Legolas" there. An
  // unscoped assertion would be tripped up by that unrelated, correct update; this test is
  // specifically about CharacterDetailView's own `{ aggregateId: 'ch1' }`-scoped watch not firing.
  await page.waitForTimeout(1000)
  await expect(page.getByRole('main').getByText('Renamed Legolas')).not.toBeVisible()
  await expect(baseInfo.getByText('Aragorn', { exact: true })).toBeVisible()
  await realtime.close()
})

test('a dropped SSE connection still catches up via the polling fallback on reconnect', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  const realtime = await startMockRealtimeStream()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

  await page.goto('/universes/u1/campaigns/c1')
  const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
  await expect(entitiesSection.getByText('Aragorn')).toBeVisible()

  // Simulate a change happening WHILE the connection is down: close the mock server's
  // connection (the page's EventSource will see this as a drop and auto-reconnect per spec,
  // default ~3s retry) without ever pushing a live frame for it — the only way this change can
  // reach the page is via the catch-up poll that fires on the reconnect's onopen.
  state.entities.push({ id: 'e2', name: 'Bilbo', universeId: 'u1', isArchived: false })
  state.changes.push({
    globalSeq: 1,
    universeId: 'u1',
    aggregateType: 'entity',
    aggregateId: 'e2',
    eventType: 'entity.created.v1',
    occurredAt: new Date().toISOString(),
  })
  await realtime.close()

  // No realtime.push() call for this one — proves the catch-up poll (not a live frame) is what
  // delivers it. Generous timeout: covers the browser's own auto-reconnect delay.
  await expect(entitiesSection.getByText('Bilbo')).toBeVisible({ timeout: 10000 })
})

test('a dropped connection\'s catch-up batch updates every affected sidebar, not just the last one', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  const realtime = await startMockRealtimeStream()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID, realtimeOrigin: realtime.origin })

  await page.goto('/universes/u1/campaigns/c1')
  const entitiesSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Entities' }) })
  const charactersSection = page.locator('section').filter({ has: page.getByRole('heading', { name: 'Characters' }) })
  await expect(entitiesSection.getByText('Aragorn')).toBeVisible()

  // Same shape as "a batch of live changes..." above, but delivered via the catch-up poll (not
  // a live push) — regression coverage for the bug where a synchronous apply loop with no
  // per-change yield let Vue's flush:'pre' watch coalesce a multi-change batch down to only the
  // last change ever reaching a watcher.
  state.entities.push({ id: 'e2', name: 'Gimli', universeId: 'u1', isArchived: false })
  state.characters.push({
    id: 'ch2',
    name: 'Legolas',
    campaignId: 'c1',
    entityId: 'e3',
    playerUserId: 'user-1',
    isArchived: false,
  })
  state.changes.push(
    {
      globalSeq: 1,
      universeId: 'u1',
      aggregateType: 'entity',
      aggregateId: 'e2',
      eventType: 'entity.created.v1',
      occurredAt: new Date().toISOString(),
    },
    {
      globalSeq: 2,
      universeId: 'u1',
      aggregateType: 'character',
      aggregateId: 'ch2',
      eventType: 'character.created.v1',
      occurredAt: new Date().toISOString(),
    },
  )
  await realtime.close()

  await expect(entitiesSection.getByText('Gimli')).toBeVisible({ timeout: 10000 })
  await expect(charactersSection.getByText('Legolas')).toBeVisible({ timeout: 10000 })
})
