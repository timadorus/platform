# Universe Overview Panel Change-Feed Consumer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `UniverseOverviewPanel.vue` its own live change-feed consumer, so a Universe-level
change made elsewhere (rename, archive, Creator add/remove) shows up without a manual reload.

**Architecture:** A second, independent `useChangeFeed()` instance owned directly by
`UniverseOverviewPanel.vue` (not via `inject`, since this panel sits outside `WorkspaceView`'s
provide scope) — same lifecycle wiring `WorkspaceView.vue` already uses, same single-aggregate-self
watch filter `CampaignOverviewPanel.vue` already uses.

**Tech Stack:** Vue 3 `<script setup>`, the existing `useChangeFeed` composable, Playwright.

## Global Constraints

- Only `web/src/views/UniverseOverviewPanel.vue` and `web/e2e/universe-change-feed.spec.ts` change.
- `WorkspaceView.vue`'s provide scope and route structure are NOT touched.
- Scoped strictly to `'universe'`-type changes — do not also wire up `'campaign'`-type changes for
  this panel's Campaigns list (out of scope, see the design spec).
- `npm run build` and the full `npm run test:e2e` suite must stay green.

---

### Task 1: Wire up the change-feed consumer and add the regression test

**Files:**
- Modify: `web/src/views/UniverseOverviewPanel.vue`
- Modify: `web/e2e/universe-change-feed.spec.ts`

**Interfaces:**
- Consumes: `useChangeFeed()` from `web/src/composables/useChangeFeed.ts` — `{ lastChange, start(universeId), stop() }`, unchanged.

- [ ] **Step 1: Give `load()` a `silent` option**

In `web/src/views/UniverseOverviewPanel.vue`, change:

```ts
async function load() {
  loading.value = true
  universe.value = await getUniverse(universeId.value)
  await listUsers()
  creatorIds.value = await listCreators(universeId.value)
  await listByUniverse(universeId.value)
  if (campaignsError.value) error.value = campaignsError.value
  loading.value = false
}
onMounted(load)
watch(universeId, load)
```

to:

```ts
async function load(opts: { silent?: boolean } = {}) {
  if (!opts.silent) loading.value = true
  universe.value = await getUniverse(universeId.value)
  await listUsers()
  creatorIds.value = await listCreators(universeId.value)
  await listByUniverse(universeId.value)
  if (campaignsError.value) error.value = campaignsError.value
  if (!opts.silent) loading.value = false
}
onMounted(() => load())
watch(universeId, () => load())
```

(Wrapping the `onMounted`/`watch` callbacks in arrow functions, not passing `load` directly, avoids
`watch`'s own callback arguments — `(newValue, oldValue, onCleanup)` — silently shadowing `opts`,
matching `CampaignOverviewPanel.vue`'s own comment on this exact hazard.)

- [ ] **Step 2: Add the change-feed instance**

Add the import:

```ts
import { useChangeFeed } from '@/composables/useChangeFeed'
```

(Unlike `CampaignOverviewPanel.vue`'s `inject<Ref<AggregateChange | null>>(...)`, this panel calls
`useChangeFeed()` directly, so TypeScript already infers `lastAggregateChange`'s type from the
composable's own return type — no `AggregateChange` type import is needed here.)

Add, near the other composable calls:

```ts
const { lastChange: lastAggregateChange, start: startChangeFeed, stop: stopChangeFeed } = useChangeFeed()
onMounted(() => startChangeFeed(universeId.value))
watch(universeId, startChangeFeed)
onUnmounted(stopChangeFeed)

watch(lastAggregateChange, (change) => {
  if (change?.aggregateType === 'universe' && change.aggregateId.toLowerCase() === universeId.value.toLowerCase()) {
    load({ silent: true })
  }
})
```

Add `onUnmounted` to the existing `vue` import (`import { computed, onMounted, onUnmounted, ref, watch } from 'vue'`).

Remove the now-resolved comment at the top of the file (lines 2-6, "This panel is a top-level
route... see docs/BACKLOG.md") — it describes exactly the gap this task closes.

- [ ] **Step 3: Write the regression test**

In `web/e2e/universe-change-feed.spec.ts`, add a third test following the file's existing two tests'
exact shape:

```ts
test('an externally-made Universe rename is picked up by the Universe panel without any local action', async ({
  page,
  context,
  baseURL,
}) => {
  const base = baseURL!
  const authority = `${base}/oidc`
  const state = seedState()
  await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
  await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

  await page.goto('/universes/u1/manage')
  await expect(page.getByRole('heading', { name: 'Test Universe' })).toBeVisible()

  // Simulate another tab/user renaming the Universe: mutate state directly (bypassing the rename
  // command route — this test is about the change feed noticing it, not about renaming itself)
  // and record a matching change-log row the poller will pick up.
  state.universes[0].name = 'Renamed Elsewhere'
  state.changes.push({
    globalSeq: 1,
    universeId: 'u1',
    aggregateType: 'universe',
    aggregateId: 'u1',
    eventType: 'universe.renamed.v1',
    occurredAt: new Date().toISOString(),
  })

  // useChangeFeed polls every 5s — wait comfortably past that instead of asserting immediately.
  await expect(page.getByRole('heading', { name: 'Renamed Elsewhere' })).toBeVisible({ timeout: 10000 })
})
```

- [ ] **Step 4: Run the new test**

Run: `cd web && npx playwright test universe-change-feed.spec.ts`
Expected: all 3 tests in the file PASS.

- [ ] **Step 5: Run the full web build and e2e suite**

Run: `cd web && npm run build && npm run test:e2e`
Expected: build clean, all e2e tests green.

- [ ] **Step 6: Commit**

```bash
git add web/src/views/UniverseOverviewPanel.vue web/e2e/universe-change-feed.spec.ts
git commit -m "web: give UniverseOverviewPanel its own live change-feed consumer"
```
