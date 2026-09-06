# CampaignPickerView Deep-Link Selection Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix `CampaignPickerView.vue`'s `goTo()` so a deep link to a different Universe's campaign
picker no longer corrupts the persisted selection pairing.

**Architecture:** One-line fix mirroring an existing sibling pattern (`UniverseOverviewPanel.goToCampaign`),
plus a new Playwright regression test reproducing the exact corruption sequence.

**Tech Stack:** Vue 3 `<script setup>`, Pinia (`useSelectionStore`), Playwright (`web/e2e`).

## Global Constraints

- `web/src/stores/selection.ts` is not modified — the fix is entirely in the call site.
- No new mock-backend route arms — `GET /universes/:universeId`, `GET /universes/:universeId/campaigns`,
  and `GET /campaigns/:campaignId` are already mocked in `web/e2e/support/mockBackend.ts`.
- `npm run build` (from `web/`) and the full `npm run test:e2e` suite must stay green.

---

### Task 1: Fix `goTo` and add the regression test

**Files:**
- Modify: `web/src/views/CampaignPickerView.vue`
- Create: `web/e2e/campaign-picker-deep-link.spec.ts`

**Interfaces:**
- Consumes: `useSelectionStore()`'s existing `setUniverse(id: string)` and `setCampaign(id: string)`
  actions (`web/src/stores/selection.ts`, unchanged).
- Consumes: `web/e2e/support/mockBackend.ts`'s `createMockState`/`installMockBackend`, and
  `web/e2e/support/auth.ts`'s `seedAuth` — same pattern every other spec in `web/e2e` uses.

- [ ] **Step 1: Fix `goTo`**

In `web/src/views/CampaignPickerView.vue`, change:

```ts
function goTo(campaignId: string) {
  selection.setCampaign(campaignId)
  router.push({ name: 'workspace', params: { universeId: universeId.value, campaignId } })
}
```

to:

```ts
function goTo(campaignId: string) {
  selection.setUniverse(universeId.value)
  selection.setCampaign(campaignId)
  router.push({ name: 'workspace', params: { universeId: universeId.value, campaignId } })
}
```

This mirrors `UniverseOverviewPanel.vue`'s `goToCampaign` exactly. Note this is called from three
places in the file (the stored-selection restore path in `onMounted`, the picker grid's
`@select="goTo"`, and `onCreated`) — all three become correct with this one change; no other call
site needs editing.

- [ ] **Step 2: Write the regression test**

Create `web/e2e/campaign-picker-deep-link.spec.ts`:

```ts
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
  await context.addInitScript(
    ([key, value]) => window.localStorage.setItem(key, value),
    [
      'timadorus:selection:test-sub',
      JSON.stringify({ selectedUniverseId: 'u1', selectedCampaignId: null }),
    ],
  )

  // Deep link straight to u2's campaign picker — skips u1 and the Universe picker entirely, the
  // exact scenario that previously corrupted the persisted pair (BACKLOG.md, Web SPA section).
  // CampaignPickerView's actual route is /universes/:universeId (web/src/router/index.ts's
  // 'campaign-picker' route) — no /campaigns suffix.
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
```

- [ ] **Step 3: Run the new test**

Run: `cd web && npx playwright test campaign-picker-deep-link.spec.ts`
Expected: PASS. (Run it against the pre-fix `goTo` first if you want to confirm it actually catches
the bug — reverting Step 1 should make the `localStorage` assertion fail with
`selectedUniverseId: 'u1'` instead of `'u2'`.)

- [ ] **Step 4: Run the full web build and e2e suite**

Run: `cd web && npm run build && npm run test:e2e`
Expected: build clean, all e2e tests green (including the new one).

- [ ] **Step 5: Commit**

```bash
git add web/src/views/CampaignPickerView.vue web/e2e/campaign-picker-deep-link.spec.ts
git commit -m "web: fix CampaignPickerView.goTo to record the selected Universe on deep link"
```
