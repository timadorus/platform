# Universe Panel Create Campaign Button Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a "+ Create Campaign" button below the Campaigns list in `UniverseOverviewPanel.vue`,
reusing the existing `CreateCampaignModal.vue`; on creation, navigate straight into the new
Campaign's workspace.

**Architecture:** Single-file production change (`UniverseOverviewPanel.vue` gains a
`showCreateCampaign` ref, a `BaseButton`, and the modal, mirroring `CampaignPickerView.vue`'s
existing create-flow shape exactly) plus mock-backend support for two endpoints this flow needs
that no existing test has exercised yet (`GET /rulesets` list, `POST
/universes/{universeId}/campaigns`), plus one new e2e test.

**Tech Stack:** Vue 3 `<script setup>`, the existing `CreateCampaignModal.vue`/`useCampaigns.ts`
(unmodified), `@playwright/test`.

## Global Constraints

- No change to `CreateCampaignModal.vue`, `useCampaigns.ts`, or the backend.
- On `created`, close the modal and navigate into the new Campaign via the panel's existing
  `goToCampaign(id)` (already selection-aware) — do NOT stay on this panel and refresh the list
  instead (a design alternative explicitly rejected during brainstorming).
- The button is visible below the Campaigns list whether or not the Universe already has any
  Campaigns.

---

### Task 1: Button + modal wiring, mock support, e2e test

**Files:**
- Modify: `web/src/views/UniverseOverviewPanel.vue`
- Modify: `web/e2e/support/mockBackend.ts`
- Modify: `web/e2e/universe-manage.spec.ts`

**Interfaces:**
- Consumes: `CreateCampaignModal.vue`'s `universe-id` prop and `close`/`created` emits (unchanged);
  `UniverseOverviewPanel.vue`'s existing `goToCampaign(campaignId: string)` (unchanged, already
  selection-aware).
- Produces: nothing new for other tasks — this is the only task in this plan.

- [ ] **Step 1: Wire the button and modal in `UniverseOverviewPanel.vue`**

Add this import alongside the existing ones:
```ts
import CreateCampaignModal from '@/components/modals/CreateCampaignModal.vue'
```

Add this ref alongside the existing `showAddCreator`/`showArchiveConfirm` refs:
```ts
const showCreateCampaign = ref(false)
```

Add this function alongside the existing `goToCampaign`:
```ts
function onCampaignCreated(id: string) {
  showCreateCampaign.value = false
  goToCampaign(id)
}
```

In the template, change the Campaigns section from:
```vue
    <div class="mb-4">
      <label class="mb-1 block text-xs font-medium text-slate-600">Campaigns</label>
      <ul v-if="campaigns.length" class="space-y-1">
        <li v-for="c in campaigns" :key="c.id">
          <button class="text-sm text-indigo-600 hover:underline" @click="goToCampaign(c.id)">{{ c.name }}</button>
        </li>
      </ul>
      <p v-else class="text-sm text-slate-500">No Campaigns yet.</p>
    </div>
```
to:
```vue
    <div class="mb-4">
      <label class="mb-1 block text-xs font-medium text-slate-600">Campaigns</label>
      <ul v-if="campaigns.length" class="space-y-1">
        <li v-for="c in campaigns" :key="c.id">
          <button class="text-sm text-indigo-600 hover:underline" @click="goToCampaign(c.id)">{{ c.name }}</button>
        </li>
      </ul>
      <p v-else class="text-sm text-slate-500">No Campaigns yet.</p>
      <BaseButton class="mt-2" @click="showCreateCampaign = true">+ Create Campaign</BaseButton>
    </div>

    <CreateCampaignModal
      v-if="showCreateCampaign"
      :universe-id="universeId"
      @close="showCreateCampaign = false"
      @created="onCampaignCreated"
    />
```

- [ ] **Step 2: Add the two mock routes this flow needs**

In `web/e2e/support/mockBackend.ts`, add this GET route to the `// ---- query API ----` section,
immediately after the existing `/api/query/rulesets/:rulesetId` route:

```ts
    if (method === 'GET' && p === '/api/query/rulesets') {
      return json(route, state.rulesets)
    }
```

Add this POST route to the `// ---- command API ----` section, immediately after the existing
`PATCH /api/command/universes/:universeId` route:

```ts
    if (method === 'POST' && (m = matchPath('/api/command/universes/:universeId/campaigns', p))) {
      const body = JSON.parse(req.postData() || '{}') as {
        name: string
        rulesetId: string
        gamemasterUserIds: string[]
      }
      const campaignId = newId(state, 'campaign')
      state.campaigns.push({
        id: campaignId,
        universeId: m!.params.universeId,
        name: body.name,
        rulesetId: body.rulesetId,
        isArchived: false,
      })
      return json(route, { id: campaignId }, 201)
    }
```

- [ ] **Step 3: Add the e2e test**

In `web/e2e/universe-manage.spec.ts`, add this test after the existing three tests. It reuses
`seedState`'s default fixtures (which already include a Ruleset `r1`/"Test Ruleset" and a User
`user-1`/`devuser@timadorus.local` matching the harness's seeded auth identity — `UserMultiSelect`
auto-selects a User whose name matches the logged-in email, so no manual Gamemaster click is
needed):

```ts
test('creating a Campaign from the Universe panel navigates into its workspace', async ({
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

  await page.getByRole('button', { name: '+ Create Campaign' }).click()
  await page.getByLabel('Name').fill('New Campaign')
  await page.getByLabel('Ruleset').selectOption({ label: 'Test Ruleset' })
  await expect(page.getByRole('checkbox').first()).toBeChecked()
  await page.getByRole('button', { name: 'Create', exact: true }).click()

  await expect(page).toHaveURL(/\/universes\/u1\/campaigns\/[^/]+$/)
})
```

Note: `CreateCampaignModal.vue`'s `<label>` elements have no `for`/`id` pairing with their
`<input>`/`<select>` — `page.getByLabel(...)` relies on implicit label association, which does NOT
work without that pairing. Verify this by running the test; if `getByLabel` fails to find the
field, fall back to positional/structural locators instead (e.g.
`page.locator('form').getByRole('textbox').first()` for the Name field, and
`page.locator('form').getByRole('combobox')` for the Ruleset `<select>`), and note in your report
which one was actually needed — do not guess silently, verify empirically (this codebase has real,
repeated precedent for exactly this kind of selector assumption turning out to be wrong).

- [ ] **Step 4: Run the full suite**

Run (from `web/`): `npm run test:e2e`
Expected: 9 passed (the 8 existing tests plus this new one). Run it twice to confirm no flakes.

- [ ] **Step 5: Typecheck and build**

Run (from `web/`): `npm run typecheck && npm run build`
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add web/src/views/UniverseOverviewPanel.vue web/e2e/support/mockBackend.ts web/e2e/universe-manage.spec.ts
git commit -m "web: add a Create Campaign button to the Universe panel's Campaigns list"
```

---

## Final Verification

- `npm run typecheck && npm run build && npm run test:e2e` (from `web/`) all clean — 9 tests
  passing, no flakes across at least two full runs.
- `go build ./... && go vet ./... && go test ./...` unaffected (this plan touches no Go code) —
  confirm anyway.
