# CreateCampaignModal Accessibility Fix

## Context

`docs/BACKLOG.md`'s Web SPA section documents a real accessibility gap that has already forced two
separate test-writing passes to route around it: `CreateCampaignModal.vue`'s `<label>` elements have
no `for`/`id` pairing with their `<input>`/`RulesetSelect`/`UserMultiSelect`, so Playwright's
`page.getByLabel(...)` cannot locate them — `universe-manage.spec.ts` already has to fall back to
`page.locator('form').getByRole(...)`, an unscoped, strict-mode-dependent workaround (confirmed via
its own inline comment). Confirmed by grepping every file under `web/src/components/modals/` for
`for="` — zero matches anywhere — this gap is systemic to every modal in the app, not unique to this
one, but per the user's own scoping this branch fixes only `CreateCampaignModal.vue`'s own fields
plus the one shared fix that helps every modal at once (`BaseModal.vue`'s missing `role="dialog"`).

## Fix

**`BaseModal.vue`** (shared by every modal in the app): add `role="dialog"`, `aria-modal="true"`, and
`aria-labelledby` pointing at the title's own id, generated via Vue 3.5's `useId()` (already
available — `web/package.json` pins `vue: ^3.5.0`) so each modal instance gets a stable, unique id
without any caller needing to supply one:

```html
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
        ...
```

**`RulesetSelect.vue`**: a real single `<select>` — accept an optional `id` prop, forward it to the
element, so a caller's `<label for="...">` can pair with it:

```html
<script setup lang="ts">
defineProps<{ id?: string }>()
...
</script>
<template>
  <select :id="id" v-model="modelValue" ...>
```

**`UserMultiSelect.vue`**: a checkbox list, not a single control — `for`/`id` doesn't map onto it.
Accept an optional `labelledby` prop and apply `role="group"` + `:aria-labelledby="labelledby"` on
the root element, so a caller's plain (non-`for`) `<label>` (given a matching `id`) still associates
with the group for assistive tech:

```html
<script setup lang="ts">
defineProps<{ labelledby?: string }>()
...
</script>
<template>
  <div role="group" :aria-labelledby="labelledby" class="max-h-40 overflow-y-auto rounded-md border border-slate-200 p-2">
```

**`CreateCampaignModal.vue`**: wire up all three fields —
- Name: `<label for="campaign-name">` + `<input id="campaign-name">`.
- Ruleset: `<label id="campaign-ruleset-label" for="campaign-ruleset">` + `<RulesetSelect id="campaign-ruleset" .../>`.
- Gamemasters: `<label id="campaign-gamemasters-label">` (no `for` — see `UserMultiSelect` above) +
  `<UserMultiSelect labelledby="campaign-gamemasters-label" .../>`.

## Testing

Update `universe-manage.spec.ts`'s "creating a Campaign from the Universe panel" test: its existing
comment (lines 104-111) documents the exact `getByLabel`-doesn't-work workaround this fix resolves —
switch it to `page.getByLabel('Name')`, `page.getByLabel('Ruleset')`, and the Gamemasters group's own
appropriate locator (`page.getByRole('group', { name: 'Gamemasters (at least one)' })` for scoping,
still using `.getByRole('checkbox')` within it for the actual interaction, since a checkbox group
has no single "the label" element `getByLabel` targets) — removing the stale comment once it no
longer applies. Add a small assertion that `BaseModal`'s dialog role is now present (e.g.
`page.getByRole('dialog', { name: 'Create Campaign' })`), proving the `aria-labelledby` wiring works
end to end, not just that the individual field labels do.

## Out of Scope

Applying the same `for`/`id`/`role="group"` treatment to the other 6 modals under
`web/src/components/modals/` (`CreateCharacterModal.vue`, `CreateEntityModal.vue`,
`CreateObjectModal.vue`, `CreateUniverseModal.vue`, `CreateUserModal.vue`,
`CreatingUserModal.vue`) — each has the identical gap (confirmed via the same grep), but fixing them
is a separate, mechanical follow-up in the same shape as this branch, not bundled in here. Add one
line to `docs/BACKLOG.md` flagging this remaining scope so it isn't lost.
