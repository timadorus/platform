# Shared pollUntil Helper + Campaign Creation Retry Parity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extract the poll-with-timeout skeleton duplicated across `useUsers.ts`/`useCharacters.ts`/
`useEntities.ts` into one shared helper, then use it to give Campaign creation the same retry/timeout
parity Character creation already has.

**Architecture:** A new `usePolling.ts` composable exporting `pollUntil<T>`; the four existing
`waitForX` functions become thin wrappers around it (pure refactor); a new `waitForCampaign` in
`useCampaigns.ts` uses it from day one; `CampaignOverviewPanel.vue` and `WorkspaceView.vue` switch to
it, with `CampaignOverviewPanel.vue` gaining the same `loadTimedOut`/Retry/AbortController pattern
`CharacterDetailView.vue` already has.

**Tech Stack:** Vue 3 `<script setup>`, Playwright.

## Global Constraints

- Refactoring the 4 existing `waitForX` functions (Task 1) must not change their public signatures
  or observable behavior — every existing test that exercises them must keep passing unchanged.
- `npm run build` and the full `npm run test:e2e` suite must stay green after every task.

---

### Task 1: Extract `usePolling.ts` and refactor the 4 existing poll functions

**Files:**
- Create: `web/src/composables/usePolling.ts`
- Modify: `web/src/composables/useUsers.ts`
- Modify: `web/src/composables/useCharacters.ts`
- Modify: `web/src/composables/useEntities.ts`

**Interfaces:**
- Produces: `pollUntil<T>(attempt: () => Promise<T | null | undefined>, opts?: PollOptions): Promise<T | null>`
  and `interface PollOptions { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal; shouldStop?: () => boolean }`
  — consumed by Task 2's `waitForCampaign` and by this task's own refactored functions.

- [ ] **Step 1: Create `usePolling.ts`**

```ts
// usePolling.ts extracts the poll-with-timeout skeleton this codebase's eventual-consistency waits
// (waitForUser, waitForCharacter, waitForCharacterInList, waitForEntityInList, and now
// waitForCampaign) all shared as near-identical copies — see docs/BACKLOG.md's "poll-with-timeout
// pattern is now duplicated" entry.
export interface PollOptions {
  intervalMs?: number
  timeoutMs?: number
  signal?: AbortSignal
  // shouldStop, if given, is checked immediately after each attempt (before checking whether it
  // found anything) — for the "list membership" shape, where a reactive error ref getting set is
  // a hard failure that must stop polling right away, distinct from "not found yet, keep going"
  // (which is what a null/undefined attempt result means with no shouldStop signal).
  shouldStop?: () => boolean
}

// pollUntil calls attempt() repeatedly until it returns a non-null/non-undefined value, shouldStop()
// returns true, opts.signal aborts, or timeoutMs elapses. Returns the found value, or null on any
// give-up condition. Checks abort both before and after each attempt call (an in-flight attempt
// that resolves after the caller has already moved on must not be acted on).
export async function pollUntil<T>(
  attempt: () => Promise<T | null | undefined>,
  opts: PollOptions = {},
): Promise<T | null> {
  const intervalMs = opts.intervalMs ?? 750
  const timeoutMs = opts.timeoutMs ?? 15000
  const deadline = Date.now() + timeoutMs
  for (;;) {
    if (opts.signal?.aborted) return null
    const result = await attempt()
    if (opts.signal?.aborted) return null
    if (opts.shouldStop?.()) return null
    if (result !== null && result !== undefined) return result
    if (Date.now() >= deadline) return null
    await new Promise((resolve) => setTimeout(resolve, intervalMs))
  }
}
```

- [ ] **Step 2: Refactor `useCharacters.ts`'s `waitForCharacter` and `waitForCharacterInList`**

Add the import: `import { pollUntil } from './usePolling'`

Replace `waitForCharacter`'s body (keep its doc comment and signature unchanged):

```ts
  async function waitForCharacter(
    id: string,
    opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
  ): Promise<CharacterSummary | null> {
    return pollUntil(() => get(id), opts)
  }
```

Replace `waitForCharacterInList`'s body (keep its doc comment and signature unchanged):

```ts
  async function waitForCharacterInList(
    campaignId: string,
    characterId: string,
    opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
  ): Promise<boolean> {
    const found = await pollUntil(
      async () => {
        await list(campaignId)
        return characters.value.some((c) => c.id === characterId) ? true : null
      },
      { ...opts, shouldStop: () => error.value !== null },
    )
    return found ?? false
  }
```

- [ ] **Step 3: Refactor `useUsers.ts`'s `waitForUser`**

Add the import: `import { pollUntil } from './usePolling'`

Replace `waitForUser`'s body (keep its doc comment and signature unchanged):

```ts
  async function waitForUser(
    id: string,
    opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
  ): Promise<boolean> {
    const found = await pollUntil(
      async () => {
        await list()
        return users.value.some((u) => u.id === id) ? true : null
      },
      { ...opts, shouldStop: () => error.value !== null },
    )
    return found ?? false
  }
```

- [ ] **Step 4: Refactor `useEntities.ts`'s `waitForEntityInList`**

Add the import: `import { pollUntil } from './usePolling'`

Replace `waitForEntityInList`'s body (keep its doc comment and signature unchanged):

```ts
  async function waitForEntityInList(
    universeId: string,
    entityId: string,
    opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
  ): Promise<boolean> {
    const found = await pollUntil(
      async () => {
        await search(universeId, '')
        return entities.value.some((e) => e.id === entityId) ? true : null
      },
      { ...opts, shouldStop: () => error.value !== null },
    )
    return found ?? false
  }
```

- [ ] **Step 5: Run the full build and e2e suite — this is a pure refactor, everything must pass unchanged**

Run: `cd web && npm run build && npm run test:e2e`
Expected: build clean, every existing e2e test green with no changes needed to any of them (in
particular `character-creation-lag.spec.ts` and `character-creation.spec.ts`, which exercise
`waitForCharacter`/`waitForCharacterInList`/`waitForUser` directly).

- [ ] **Step 6: Commit**

```bash
git add web/src/composables/usePolling.ts web/src/composables/useUsers.ts web/src/composables/useCharacters.ts web/src/composables/useEntities.ts
git commit -m "web: extract shared pollUntil helper from the 4 duplicated poll-with-timeout functions"
```

---

### Task 2: `waitForCampaign` + mock backend Campaign visibility delay

**Files:**
- Modify: `web/src/composables/useCampaigns.ts`
- Modify: `web/e2e/support/mockBackend.ts`

**Depends on:** Task 1 (`pollUntil`).

**Interfaces:**
- Produces: `useCampaigns()`'s new `waitForCampaign(id: string, opts?: {intervalMs?, timeoutMs?, signal?}): Promise<CampaignSummary | null>`
  — consumed by Task 3.
- Produces (test infra): `MockCampaign.visibleAt?: number`, gated the same way `MockCharacter.visibleAt` already is.

- [ ] **Step 1: Add `waitForCampaign` to `useCampaigns.ts`**

Add the import: `import { pollUntil } from './usePolling'`

Add, near `get`:

```ts
  // waitForCampaign polls get(id) until it succeeds or timeoutMs elapses. Like waitForCharacter
  // (useCharacters.ts), this cannot distinguish "the projector hasn't caught up yet" from "this id
  // doesn't exist" — get() returns null identically either way — so every failed attempt is
  // retried until the deadline; callers should show one honest "couldn't load" message on timeout,
  // not a distinct error state.
  async function waitForCampaign(
    id: string,
    opts: { intervalMs?: number; timeoutMs?: number; signal?: AbortSignal } = {},
  ): Promise<CampaignSummary | null> {
    return pollUntil(() => get(id), opts)
  }
```

Add `waitForCampaign` to the composable's final `return { ... }` object.

- [ ] **Step 2: Extend `mockBackend.ts`'s visibility-delay mechanism to Campaigns**

In `MockCampaign`'s interface, add (matching `MockCharacter`'s equivalent field's own comment shape):

```ts
  // Optional — set by the create-Campaign command handler below when createVisibilityDelayMs is
  // configured, mirroring MockCharacter.visibleAt.
  visibleAt?: number
```

Update `createVisibilityDelayMs`'s own doc comment on `MockState` from "makes the create-Character
command's new Character and Entity invisible" to "makes the create-Character command's new Character
and Entity, and the create-Campaign command's new Campaign, invisible" (or equivalent wording — keep
it accurate now that a third creation path uses it).

In the `POST /api/command/universes/:universeId/campaigns` handler, change:

```ts
      const campaignId = newId(state, 'campaign')
      state.campaigns.push({
        id: campaignId,
        universeId: m!.params.universeId,
        name: body.name,
        rulesetId: body.rulesetId,
        gamemasterUserIds: body.gamemasterUserIds,
        isArchived: false,
      })
```

to:

```ts
      const campaignId = newId(state, 'campaign')
      const visibleAt = state.createVisibilityDelayMs !== undefined ? Date.now() + state.createVisibilityDelayMs : undefined
      state.campaigns.push({
        id: campaignId,
        universeId: m!.params.universeId,
        name: body.name,
        rulesetId: body.rulesetId,
        gamemasterUserIds: body.gamemasterUserIds,
        isArchived: false,
        visibleAt,
      })
```

In the `GET /api/query/campaigns/:campaignId` handler, change:

```ts
    if (method === 'GET' && (m = matchPath('/api/query/campaigns/:campaignId', p))) {
      const campaign = state.campaigns.find((c) => c.id === m!.params.campaignId)
      return campaign ? json(route, campaign) : json(route, { title: 'not found' }, 404)
    }
```

to:

```ts
    if (method === 'GET' && (m = matchPath('/api/query/campaigns/:campaignId', p))) {
      const campaign = state.campaigns.find((c) => c.id === m!.params.campaignId)
      const visible = campaign && (campaign.visibleAt === undefined || campaign.visibleAt <= Date.now())
      return visible ? json(route, campaign) : json(route, { title: 'not found' }, 404)
    }
```

- [ ] **Step 3: Run the full build and existing e2e suite**

Run: `cd web && npm run build && npm run test:e2e`
Expected: build clean, all existing e2e tests still green — `visibleAt` is optional and undefined for
every existing seed, so `campaign.visibleAt === undefined` keeps every current test's Campaigns
immediately visible, unchanged.

- [ ] **Step 4: Commit**

```bash
git add web/src/composables/useCampaigns.ts web/e2e/support/mockBackend.ts
git commit -m "web: add waitForCampaign; extend mock backend's visibility delay to Campaign creation"
```

---

### Task 3: `CampaignOverviewPanel.vue` Retry parity + `WorkspaceView.vue` badge fix + regression test

**Files:**
- Modify: `web/src/views/CampaignOverviewPanel.vue`
- Modify: `web/src/views/WorkspaceView.vue`
- Create: `web/e2e/campaign-creation-lag.spec.ts`

**Depends on:** Task 2 (`waitForCampaign`).

- [ ] **Step 1: `WorkspaceView.vue` — use `waitForCampaign` for the header badge**

Change:

```ts
const { get: getCampaign } = useCampaigns()
```

to:

```ts
const { waitForCampaign } = useCampaigns()
```

Change `load()`'s body:

```ts
async function load() {
  universe.value = await getUniverse(universeId.value)
  campaign.value = await getCampaign(campaignId.value)
}
```

to:

```ts
async function load() {
  universe.value = await getUniverse(universeId.value)
  campaign.value = await waitForCampaign(campaignId.value)
}
```

No template change needed here — there is no "not found" state to render in the workspace shell;
the header badge simply stays blank for longer while polling instead of blanking permanently.

- [ ] **Step 2: `CampaignOverviewPanel.vue` — Retry/timeout parity**

Add the import: `import BaseButton from '@/components/common/BaseButton.vue'`

Change the composable destructure:

```ts
const { get: getCampaign, rename, archive, listGamemasters, addGamemaster, removeGamemaster } = useCampaigns()
```

to:

```ts
const { waitForCampaign, rename, archive, listGamemasters, addGamemaster, removeGamemaster } = useCampaigns()
```

Add `const loadTimedOut = ref(false)` alongside the other refs, and `let loadController: AbortController | null = null` above `load`.

Replace `load()`:

```ts
async function load(opts: { silent?: boolean } = {}) {
  if (!opts.silent) loading.value = true
  campaign.value = await getCampaign(campaignId.value)
  await listUsers()
  const [ruleset, ids] = await Promise.all([
    campaign.value ? getRuleset(campaign.value.rulesetId) : Promise.resolve(null),
    listGamemasters(campaignId.value),
  ])
  rulesetName.value = ruleset?.name ?? '(unknown)'
  gamemasterIds.value = ids

  await Promise.all([
    listCharacters(campaignId.value),
    searchEntities(universeId.value, ''),
    searchObjects(universeId.value, ''),
  ])
  if (!opts.silent) loading.value = false
}
```

with:

```ts
async function load(opts: { silent?: boolean } = {}) {
  loadController?.abort()
  const controller = new AbortController()
  loadController = controller
  loadTimedOut.value = false
  if (!opts.silent) loading.value = true

  const found = await waitForCampaign(campaignId.value, { signal: controller.signal })
  if (controller.signal.aborted) return
  campaign.value = found
  if (found) {
    await listUsers()
    if (controller.signal.aborted) return
    const [ruleset, ids] = await Promise.all([getRuleset(found.rulesetId), listGamemasters(campaignId.value)])
    if (controller.signal.aborted) return
    rulesetName.value = ruleset?.name ?? '(unknown)'
    gamemasterIds.value = ids

    await Promise.all([
      listCharacters(campaignId.value),
      searchEntities(universeId.value, ''),
      searchObjects(universeId.value, ''),
    ])
    if (controller.signal.aborted) return
  } else if (!opts.silent) {
    loadTimedOut.value = true
  }
  if (!opts.silent) loading.value = false
}
```

Add `onUnmounted(() => loadController?.abort())` alongside the file's other lifecycle hooks (add
`onUnmounted` to the existing `vue` import).

In the template, add a new branch between the existing `v-else-if="campaign"` block and the final
`v-else`:

```html
  <div v-else-if="loadTimedOut" class="p-6">
    <p class="mb-4 text-sm text-slate-600">
      Couldn't load this Campaign — it may not exist, or may still be taking longer than expected
      to appear.
    </p>
    <div class="flex gap-2">
      <BaseButton @click="load()">Retry</BaseButton>
      <router-link
        :to="{ name: 'universe-overview', params: { universeId } }"
        class="rounded-md border border-slate-300 px-3 py-1.5 text-sm font-medium text-slate-700 hover:bg-slate-50"
      >
        Back to Universe
      </router-link>
    </div>
  </div>
```

- [ ] **Step 3: Write the regression test**

Create `web/e2e/campaign-creation-lag.spec.ts`, mirroring `character-creation-lag.spec.ts`'s two
tests exactly:

```ts
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
  const form = page.locator('form')
  await form.getByRole('textbox').first().fill('Laggy Campaign')
  await form.getByRole('combobox').selectOption({ label: 'Test Ruleset' })
  await page.getByRole('checkbox').first().check()
  await page.getByRole('button', { name: 'Create', exact: true }).click()

  await expect(page.getByRole('button', { name: 'Manage', exact: true })).toBeVisible({ timeout: 10000 })
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
```

Note: `CreateCampaignModal.vue`'s labels have no `for`/`id` pairing (a separate, already-tracked
BACKLOG item, fixed independently by another branch), which is why this test uses the same
`form.getByRole(...)` positional locators `universe-manage.spec.ts` already uses rather than
`getByLabel`.

- [ ] **Step 4: Run the new tests**

Run: `cd web && npx playwright test campaign-creation-lag.spec.ts`
Expected: both tests PASS.

- [ ] **Step 5: Run the full web build and e2e suite**

Run: `cd web && npm run build && npm run test:e2e`
Expected: build clean, all e2e tests green.

- [ ] **Step 6: Commit**

```bash
git add web/src/views/CampaignOverviewPanel.vue web/src/views/WorkspaceView.vue web/e2e/campaign-creation-lag.spec.ts
git commit -m "web: give Campaign creation the same retry/timeout parity Character creation already has"
```
