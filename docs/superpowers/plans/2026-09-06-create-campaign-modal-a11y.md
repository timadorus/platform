# CreateCampaignModal Accessibility Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Give `CreateCampaignModal.vue`'s fields real `for`/`id`/`aria-labelledby` associations, and
`BaseModal.vue` a proper `role="dialog"`, so `page.getByLabel(...)` and `page.getByRole('dialog', ...)`
work against this modal instead of the existing brittle `page.locator('form').getByRole(...)`
workaround.

**Architecture:** `BaseModal.vue` gains a `useId()`-generated title id wired to `role="dialog"` +
`aria-labelledby` (benefits every modal in the app). `RulesetSelect.vue` and `UserMultiSelect.vue`
each gain one small optional prop (`id` / `labelledby` respectively) so a caller's `<label>` can
associate with them despite their different underlying control shapes (single `<select>` vs. a
checkbox group). `CreateCampaignModal.vue` wires all three.

**Tech Stack:** Vue 3.5 (`useId()`), Tailwind CSS, Playwright.

## Global Constraints

- Only `web/src/components/common/BaseModal.vue`, `web/src/components/pickers/RulesetSelect.vue`,
  `web/src/components/pickers/UserMultiSelect.vue`, `web/src/components/modals/CreateCampaignModal.vue`,
  and `web/e2e/universe-manage.spec.ts` change. The other 6 modals under `web/src/components/modals/`
  are explicitly out of scope for this branch (tracked as a BACKLOG follow-up instead).
- `RulesetSelect`'s new `id` prop and `UserMultiSelect`'s new `labelledby` prop must both be
  optional, defaulting to no attribute when omitted — every other existing caller of these two
  components (if any) must keep working unchanged.
- `npm run build` and the full `npm run test:e2e` suite must stay green.

---

### Task 1: `BaseModal.vue` dialog role + title id

**Files:**
- Modify: `web/src/components/common/BaseModal.vue`

**Interfaces:**
- Produces: `BaseModal.vue`'s root dialog element now carries `role="dialog"`, `aria-modal="true"`,
  and `:aria-labelledby="titleId"`, with `:id="titleId"` on the `<h2>` title — `titleId` generated
  internally via `useId()`, no new prop, no change to any existing caller.

- [ ] **Step 1: Add the dialog role and title id**

Replace the full content of `web/src/components/common/BaseModal.vue`:

```vue
<script setup lang="ts">
import { useId } from 'vue'

defineProps<{ title: string }>()
const emit = defineEmits<{ close: [] }>()
const titleId = useId()
</script>

<template>
  <div class="fixed inset-0 z-50 flex items-center justify-center bg-black/40 p-4" @click.self="emit('close')">
    <div
      class="w-full max-w-md rounded-lg bg-white p-5 shadow-xl"
      role="dialog"
      aria-modal="true"
      :aria-labelledby="titleId"
    >
      <div class="mb-4 flex items-center justify-between">
        <h2 :id="titleId" class="text-base font-semibold text-slate-900">{{ title }}</h2>
        <button class="text-slate-400 hover:text-slate-600" @click="emit('close')" aria-label="Close">✕</button>
      </div>
      <slot />
    </div>
  </div>
</template>
```

- [ ] **Step 2: Build and run the full existing e2e suite — this must not change any other test's behavior**

Run: `cd web && npm run build && npm run test:e2e`
Expected: build clean, every existing e2e test still green (this change is additive — new
attributes only, no existing selector this suite already uses is removed).

- [ ] **Step 3: Commit**

```bash
git add web/src/components/common/BaseModal.vue
git commit -m "web: give BaseModal a real dialog role and title association"
```

---

### Task 2: `RulesetSelect.vue` and `UserMultiSelect.vue` labeling support

**Files:**
- Modify: `web/src/components/pickers/RulesetSelect.vue`
- Modify: `web/src/components/pickers/UserMultiSelect.vue`

**Interfaces:**
- Produces: `RulesetSelect` gains an optional `id?: string` prop, forwarded to its `<select>`.
- Produces: `UserMultiSelect` gains an optional `labelledby?: string` prop, applied as
  `role="group"`/`aria-labelledby` on its root element.

- [ ] **Step 1: `RulesetSelect.vue`**

Change:

```vue
<script setup lang="ts">
import { onMounted } from 'vue'
import { useRulesets } from '@/composables/useRulesets'

const modelValue = defineModel<string>({ required: true })
const { rulesets, loading, list } = useRulesets()

onMounted(list)
</script>

<template>
  <select v-model="modelValue" class="w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm">
```

to:

```vue
<script setup lang="ts">
import { onMounted } from 'vue'
import { useRulesets } from '@/composables/useRulesets'

defineProps<{ id?: string }>()
const modelValue = defineModel<string>({ required: true })
const { rulesets, loading, list } = useRulesets()

onMounted(list)
</script>

<template>
  <select :id="id" v-model="modelValue" class="w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm">
```

- [ ] **Step 2: `UserMultiSelect.vue`**

Change:

```vue
<script setup lang="ts">
import { onMounted } from 'vue'
import { useUsers } from '@/composables/useUsers'
import { useAuthStore } from '@/stores/auth'

const modelValue = defineModel<string[]>({ required: true })
```

to:

```vue
<script setup lang="ts">
import { onMounted } from 'vue'
import { useUsers } from '@/composables/useUsers'
import { useAuthStore } from '@/stores/auth'

defineProps<{ labelledby?: string }>()
const modelValue = defineModel<string[]>({ required: true })
```

and change:

```html
  <div class="max-h-40 overflow-y-auto rounded-md border border-slate-200 p-2">
```

to:

```html
  <div role="group" :aria-labelledby="labelledby" class="max-h-40 overflow-y-auto rounded-md border border-slate-200 p-2">
```

- [ ] **Step 3: Build and run the full existing e2e suite**

Run: `cd web && npm run build && npm run test:e2e`
Expected: build clean, all e2e tests green (both new props are optional with no default attribute
value when omitted, so every existing caller of these two components is unaffected).

- [ ] **Step 4: Commit**

```bash
git add web/src/components/pickers/RulesetSelect.vue web/src/components/pickers/UserMultiSelect.vue
git commit -m "web: give RulesetSelect and UserMultiSelect optional label-association props"
```

---

### Task 3: Wire up `CreateCampaignModal.vue` and fix the test workaround

**Files:**
- Modify: `web/src/components/modals/CreateCampaignModal.vue`
- Modify: `web/e2e/universe-manage.spec.ts`
- Modify: `docs/BACKLOG.md`

**Depends on:** Task 1 (`BaseModal`'s dialog role), Task 2 (`RulesetSelect`/`UserMultiSelect` props).

- [ ] **Step 1: Wire up the three fields**

Replace `CreateCampaignModal.vue`'s template's form body:

```html
    <form class="space-y-3" @submit.prevent="submit">
      <div>
        <label class="mb-1 block text-xs font-medium text-slate-600">Name</label>
        <input v-model="name" type="text" class="w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
      </div>
      <div>
        <label class="mb-1 block text-xs font-medium text-slate-600">Ruleset</label>
        <RulesetSelect v-model="rulesetId" />
      </div>
      <div>
        <label class="mb-1 block text-xs font-medium text-slate-600">Gamemasters (at least one)</label>
        <UserMultiSelect v-model="gamemasterUserIds" />
      </div>
```

with:

```html
    <form class="space-y-3" @submit.prevent="submit">
      <div>
        <label for="campaign-name" class="mb-1 block text-xs font-medium text-slate-600">Name</label>
        <input id="campaign-name" v-model="name" type="text" class="w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
      </div>
      <div>
        <label id="campaign-ruleset-label" for="campaign-ruleset" class="mb-1 block text-xs font-medium text-slate-600">Ruleset</label>
        <RulesetSelect id="campaign-ruleset" v-model="rulesetId" />
      </div>
      <div>
        <label id="campaign-gamemasters-label" class="mb-1 block text-xs font-medium text-slate-600">Gamemasters (at least one)</label>
        <UserMultiSelect labelledby="campaign-gamemasters-label" v-model="gamemasterUserIds" />
      </div>
```

- [ ] **Step 2: Fix `universe-manage.spec.ts`'s workaround**

In the `'creating a Campaign from the Universe panel navigates into its workspace'` test, replace:

```ts
  await page.getByRole('button', { name: '+ Create Campaign' }).click()
  // CreateCampaignModal.vue's <label> elements have no for/id pairing with their input/select, so
  // getByLabel does not find them (verified empirically) — use positional/structural locators
  // scoped to the modal's form instead.
  const form = page.locator('form')
  await form.getByRole('textbox').first().fill('New Campaign')
  await form.getByRole('combobox').selectOption({ label: 'Test Ruleset' })
  await expect(page.getByRole('checkbox').first()).toBeChecked()
  await page.getByRole('button', { name: 'Create', exact: true }).click()
```

with:

```ts
  await page.getByRole('button', { name: '+ Create Campaign' }).click()
  await expect(page.getByRole('dialog', { name: 'Create Campaign' })).toBeVisible()
  await page.getByLabel('Name').fill('New Campaign')
  await page.getByLabel('Ruleset').selectOption({ label: 'Test Ruleset' })
  const gamemasterGroup = page.getByRole('group', { name: 'Gamemasters (at least one)' })
  await expect(gamemasterGroup.getByRole('checkbox').first()).toBeChecked()
  await page.getByRole('button', { name: 'Create', exact: true }).click()
```

- [ ] **Step 3: Update `docs/BACKLOG.md`**

Mark this entry `[x] Fixed`, and add one new `- [ ]` line to the Web SPA section noting the same
`for`/`id`/`role="group"` gap remains in the other 6 modals under `web/src/components/modals/`
(`CreateCharacterModal.vue`, `CreateEntityModal.vue`, `CreateObjectModal.vue`,
`CreateUniverseModal.vue`, `CreateUserModal.vue`, `CreatingUserModal.vue`) as a mechanical follow-up
in the same shape as this fix — not resolved here, just flagged.

- [ ] **Step 4: Run the updated test**

Run: `cd web && npx playwright test universe-manage.spec.ts`
Expected: all tests in the file PASS, including the updated `getByLabel`-based test.

- [ ] **Step 5: Run the full web build and e2e suite**

Run: `cd web && npm run build && npm run test:e2e`
Expected: build clean, all e2e tests green.

- [ ] **Step 6: Commit**

```bash
git add web/src/components/modals/CreateCampaignModal.vue web/e2e/universe-manage.spec.ts docs/BACKLOG.md
git commit -m "web: wire up CreateCampaignModal's field labels; restore getByLabel in its test"
```
