# Base Info Table Column-Width Jitter Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop `BaseInfoTable.vue`'s table columns from shifting width depending on which optional
row (Reassign Player picker, Add Trait select) is currently rendered.

**Architecture:** Switch the table from the browser default `table-layout: auto` to `table-layout:
fixed` with an explicit `<colgroup>`, so column widths never depend on conditionally-rendered row
content.

**Tech Stack:** Vue 3 `<script setup>`, Tailwind CSS, Playwright.

## Global Constraints

- Only `web/src/components/character/BaseInfoTable.vue` and one new/extended Playwright spec file
  change — no other component.
- `npm run build` and the full `npm run test:e2e` suite must stay green.

---

### Task 1: Fix the table layout and add the regression test

**Files:**
- Modify: `web/src/components/character/BaseInfoTable.vue`
- Create or modify: a Playwright spec under `web/e2e/` exercising a Character detail page's Reassign
  Player toggle (check first whether an existing spec already mounts a Character detail page in a
  state where "Reassign Player" is easy to click — if so, add the test there; otherwise create a new
  small spec file, e.g. `web/e2e/character-base-info-layout.spec.ts`)

**Interfaces:**
- No new props/emits on `BaseInfoTable.vue` — purely a template/class change.

- [ ] **Step 1: Add the `<colgroup>` and switch to `table-layout: fixed`**

In `web/src/components/character/BaseInfoTable.vue`, change:

```html
    <table class="w-full text-sm">
      <tbody>
```

to:

```html
    <table class="w-full table-fixed text-sm">
      <colgroup>
        <col class="w-1/4" />
        <col />
        <col class="w-44" />
      </colgroup>
      <tbody>
```

`w-44` (11rem) is a starting value for the actions column (wide enough for "Archive Character," the
widest action label, to stay on one line) — not yet verified pixel-exact against the real rendered
font. Step 3 verifies it visually; if "Archive Character" still wraps, widen this class (e.g. `w-48`
or `w-52`) and note in your report which value you ended up using and why.

- [ ] **Step 2: Write the regression test**

Add a test (new file or an existing spec that already reaches a Character detail page) that:

```ts
test('Base Info table column widths do not shift when the Reassign Player picker opens', async ({ page, context, baseURL }) => {
  // ... seedAuth / installMockBackend / navigate to a Character detail page, following this
  // file's (or the nearest existing spec's) established seedState/seedAuth pattern ...

  const nameCell = page.locator('td', { hasText: 'Character Name' })
  const before = await nameCell.boundingBox()
  expect(before).not.toBeNull()

  await page.getByRole('button', { name: 'Reassign Player' }).click()

  const after = await nameCell.boundingBox()
  expect(after).not.toBeNull()
  expect(after!.width).toBe(before!.width)

  const archiveButton = page.getByRole('button', { name: 'Archive Character' })
  const archiveBox = await archiveButton.boundingBox()
  expect(archiveBox).not.toBeNull()
  // A single-line button in this design system's text-sm scale renders at roughly 20-24px tall;
  // pick the exact threshold from what you actually observe rendered and document it here — the
  // point of the assertion is "did not grow to two lines' height", not a specific magic number.
  expect(archiveBox!.height).toBeLessThan(28)
})
```

Fill in the seed/navigation boilerplate to match whichever file you place this in — use the nearest
existing Character-detail-page spec's `seedState`/`seedAuth`/`installMockBackend` pattern for
consistency (e.g. `web/e2e/character-attributes.spec.ts` or `character-traits.spec.ts`) rather than
inventing a new shape.

- [ ] **Step 3: Run the new test and visually confirm the fix**

Run: `cd web && npx playwright test <your spec file>`
Expected: PASS. If `archiveBox!.height` assertion fails (still wrapping), widen the third column's
`w-44` class from Step 1 and re-run until it passes; record the final value used.

- [ ] **Step 4: Run the full web build and e2e suite**

Run: `cd web && npm run build && npm run test:e2e`
Expected: build clean, all e2e tests green.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/character/BaseInfoTable.vue web/e2e/
git commit -m "web: fix Base Info table column-width jitter with table-layout: fixed"
```
