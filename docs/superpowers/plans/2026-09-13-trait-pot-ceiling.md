# Trait-Based Pot Ceiling Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** In the SPA's Assign Stats Budget modal, raise the per-attribute Pot ceiling from 95 to 100 for any attribute the character has a matching trait for (Strong→ST, Agile→AG, Quick→QU), leaving every other attribute — and ST/AG/QU on a character without the matching trait — at the existing 95.

**Architecture:** Add a small trait→abbreviation lookup table to the shared `web/src/lib/attributes.ts`, thread the character's `traits: string[]` down through `CharacterDetailView.vue` → `AttributesTable.vue` → `AssignStatsBudgetModal.vue` (a prop the modal doesn't currently receive), and replace the modal's flat `POT_CEILING` constant with a per-attribute `ceilingFor(abbr)` function used by both the native `max` HTML attribute and the blur-time validation. No engine change — `trySubmitPot` already accepts up to 100 from any caller.

**Tech Stack:** Vue 3 `<script setup>` + TypeScript, Playwright e2e tests (this project has no unit-test framework for `.vue`/`.ts` files — Playwright and `vue-tsc` typechecking are the only verification tools available).

## Global Constraints

- Default ceiling stays exactly **95**; a traited attribute's ceiling is exactly **100** (matches the engine's absolute cap in `internal/engine/timadorus/character_processor.go`).
- The trait→attribute mapping is exactly `{ strong: 'ST', agile: 'AG', quick: 'QU' }` — mirrors `traitAttributeBonuses` in `internal/engine/timadorus/trait_hooks.go`. Do not invent additional pairings.
- Cost-tier wording/formula (≤90 = 1 budget point/Pot point, 91+ = 5) is unchanged — only the ceiling moves.
- Explanation paragraph gains exactly this trailing sentence: `An attribute granted by a matching trait (Strong, Agile, Quick) can reach 100.`
- No changes to any Go/engine file — this is SPA-only.

---

### Task 1: Trait-based ceiling in the Assign Stats Budget modal

**Files:**
- Modify: `web/src/lib/attributes.ts`
- Modify: `web/src/components/character/AssignStatsBudgetModal.vue`
- Modify: `web/src/components/character/AttributesTable.vue`
- Modify: `web/src/views/CharacterDetailView.vue`
- Test: `web/e2e/character-stats-budget.spec.ts`

**Interfaces:**
- Consumes: existing `ATTRIBUTES` (`{ name, abbr }[]`) and `potCost(initial, target): number` from `web/src/lib/attributes.ts`; existing `traits` computed (`Ref<string[]>`, parsed from `character.info.stats.traits ?? []`) already defined in `CharacterDetailView.vue`.
- Produces: new exported `TRAIT_ATTRIBUTES: Record<string, string>` from `web/src/lib/attributes.ts`, consumed by `AssignStatsBudgetModal.vue`. New prop `traits: string[]` on both `AttributesTable.vue` and `AssignStatsBudgetModal.vue`.

- [ ] **Step 1: Extend the e2e seed helper and write the three failing tests**

  In `web/e2e/character-stats-budget.spec.ts`, add a `traits` option to `seedState` and use it in the `info` payload:

  ```ts
  function seedState(
    opts: { statBudget?: number; attrs?: Partial<Record<string, number>>; traits?: string[] } = {},
    overrides: Partial<MockState> = {},
  ): MockState {
    return createMockState({
      universes: [{ id: 'u1', name: 'Test Universe', isArchived: false }],
      campaigns: [
        {
          id: 'c1',
          universeId: 'u1',
          name: 'Test Campaign',
          rulesetId: 'r1',
          isArchived: false,
          configuration: JSON.stringify({ characterCreation: { maxStatBudget: 35 } }),
        },
      ],
      rulesets: [{ id: 'r1', name: 'Timadorus' }],
      users: [{ id: 'user-1', name: 'devuser@timadorus.local', isArchived: false }],
      gamemasterIds: ['user-1'],
      characters: [
        {
          id: 'ch1',
          name: 'Aragorn',
          campaignId: 'c1',
          entityId: 'e1',
          playerUserId: 'user-1',
          isArchived: false,
          info: JSON.stringify({
            stats: {
              traitPoints: 2,
              traits: opts.traits ?? [],
              attributes: defaultAttributes(opts.attrs),
              statBudget: opts.statBudget ?? 40,
            },
          }),
        },
      ],
      entities: [{ id: 'e1', name: 'Aragorn', universeId: 'u1', isArchived: false }],
      ...overrides,
    })
  }
  ```

  Then append these three tests at the end of the file (after the "pressing Enter" test):

  ```ts
  test('an attribute granted by a matching trait raises its own ceiling to 100, leaving others at 95', async ({
    page,
    context,
    baseURL,
  }) => {
    const base = baseURL!
    const authority = `${base}/oidc`
    const state = seedState({ statBudget: 40, traits: ['strong'] })
    await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
    await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

    await page.goto('/universes/u1/campaigns/c1/characters/ch1')
    await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
    const dialog = page.getByRole('dialog')

    await expect(dialog.getByTestId('assign-pot-ST')).toHaveAttribute('max', '100')
    for (const abbr of ABBRS.filter((a) => a !== 'ST')) {
      await expect(dialog.getByTestId(`assign-pot-${abbr}`)).toHaveAttribute('max', '95')
    }
  })

  test('a traited attribute accepts a blur-time edit up to 100, while an untraited one on the same character still reverts', async ({
    page,
    context,
    baseURL,
  }) => {
    const base = baseURL!
    const authority = `${base}/oidc`
    // Budget of 100 covers ST 50->100: (90-50)*1 + (100-90)*5 = 40 + 50 = 90, leaving 10.
    const state = seedState({ statBudget: 100, traits: ['strong'] })
    await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
    await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

    await page.goto('/universes/u1/campaigns/c1/characters/ch1')
    await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
    const dialog = page.getByRole('dialog')
    const stInput = dialog.getByTestId('assign-pot-ST')
    const agInput = dialog.getByTestId('assign-pot-AG')

    await stInput.fill('100')
    await stInput.blur()
    await expect(stInput).toHaveValue('100')
    await expect(dialog.getByTestId('budget-remaining')).toHaveText('Points remaining: 10')

    // AG has no matching trait on this character, so 96 (above its own 95 ceiling) still reverts.
    await agInput.fill('96')
    await agInput.blur()
    await expect(agInput).toHaveValue('50')
  })

  test('the explanation text notes that a trait-granted attribute can reach 100', async ({ page, context, baseURL }) => {
    const base = baseURL!
    const authority = `${base}/oidc`
    const state = seedState({ statBudget: 40 })
    await seedAuth(context, { baseURL: base, authority, clientId: CLIENT_ID })
    await installMockBackend(page, state, { baseURL: base, authority, clientId: CLIENT_ID })

    await page.goto('/universes/u1/campaigns/c1/characters/ch1')
    await page.getByTestId('attributes-card').getByRole('button', { name: 'Assign Stats Budget' }).click()
    const dialog = page.getByRole('dialog')
    await expect(
      dialog.getByText('An attribute granted by a matching trait (Strong, Agile, Quick) can reach 100.'),
    ).toBeVisible()
  })
  ```

- [ ] **Step 2: Run the new tests and confirm they fail**

  Run: `cd web && npm run test:e2e -- character-stats-budget.spec.ts`
  Expected: the three new tests FAIL — the first two on the `max` / reverted-value assertions (still 95 everywhere, no ceiling exception exists yet), the third because the explanation text doesn't yet contain the new sentence. The pre-existing tests in this file still PASS (traits defaults to `[]`, so behavior for them is unchanged).

- [ ] **Step 3: Add the trait→attribute mapping to `web/src/lib/attributes.ts`**

  Add this export after the existing `ATTRIBUTES` array (before `potCost`):

  ```ts
  // Traits that grant a flat Pot bonus to one specific attribute, mirroring
  // traitAttributeBonuses in internal/engine/timadorus/trait_hooks.go — used here only to know
  // which attribute abbreviation a given trait is "for" (not the bonus amount itself, which the
  // engine applies automatically and independently of this modal's ceiling). Drives
  // AssignStatsBudgetModal.vue's per-attribute Pot ceiling — see
  // docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md's 2026-09-13 addendum.
  export const TRAIT_ATTRIBUTES: Record<string, string> = {
    strong: 'ST',
    agile: 'AG',
    quick: 'QU',
  }
  ```

- [ ] **Step 4: Update `AssignStatsBudgetModal.vue` to use a per-attribute ceiling**

  Change the import on line 6 from:

  ```ts
  import { ATTRIBUTES, potCost } from '@/lib/attributes'
  ```

  to:

  ```ts
  import { ATTRIBUTES, potCost, TRAIT_ATTRIBUTES } from '@/lib/attributes'
  ```

  Add `traits: string[]` to the `defineProps` block (after `attributes`):

  ```ts
  const props = defineProps<{
    attributes: Record<string, { temp: number; pot: number; bonus: number }>
    traits: string[]
    statBudget: number
    status: 'idle' | 'pending' | 'error'
    errorMessage: string | null
  }>()
  ```

  Replace the `POT_CEILING` block and the comment above `maxFor` with:

  ```ts
  // The highest Pot value this modal will let a player reach for an attribute with no matching
  // trait — lower than the engine's own absolute cap of 100
  // (internal/engine/timadorus/character_processor.go). Deliberately a UI-only restriction: the
  // engine still accepts up to 100 from any other caller, but this modal never asks for more than
  // 95 by default, matching the explanation text below and the native `max` attribute on each
  // input (which is what makes the browser's own spinner arrows/arrow-key stepping respect it
  // too, not just the blur-time check here). An attribute the character has a matching trait for
  // gets the full 100 instead — see ceilingFor below.
  const POT_CEILING = 95

  // The ceiling for one specific attribute: 100 if the character has a trait that grants abbr a
  // Pot bonus (TRAIT_ATTRIBUTES — Strong/Agile/Quick raise ST/AG/QU respectively), otherwise the
  // default POT_CEILING. Every attribute with no matching trait in TRAIT_ATTRIBUTES always
  // resolves to POT_CEILING, since no trait exists that could ever raise its cap.
  function ceilingFor(abbr: string): number {
    const hasMatchingTrait = props.traits.some((trait) => TRAIT_ATTRIBUTES[trait] === abbr)
    return hasMatchingTrait ? 100 : POT_CEILING
  }

  // The native `max` a given field's input should carry right now. Once the remaining budget hits
  // zero, no field can be increased any further — including the field that spent the last point —
  // so every input's max drops to wherever its own draft value already sits, freezing the native
  // spinner arrows and arrow-key stepping in place. This re-evaluates on every keystroke (draft is
  // read reactively both directly and via budgetRemaining), so a field that's later decreased back
  // down (freeing budget — floor still applies, but a field can be lowered from a value it was
  // itself raised to) immediately regains room up to its own ceiling (ceilingFor) again.
  function maxFor(abbr: string): number {
    if (budgetRemaining.value <= 0) return draft.value[abbr]
    return ceilingFor(abbr)
  }
  ```

  Update `onBlur` to validate against `ceilingFor(abbr)` instead of the flat constant:

  ```ts
  function onBlur(abbr: string) {
    const value = draft.value[abbr]
    const floor = initialPot[abbr]
    const valid =
      Number.isInteger(value) && value >= floor && value <= ceilingFor(abbr) && totalSpent.value <= props.statBudget
    if (!valid) {
      draft.value[abbr] = lastValid.value[abbr]
    } else {
      lastValid.value[abbr] = value
    }
  }
  ```

  Update the explanation paragraph in the template:

  ```html
  <p>Set potential values. Pot &le; 90 equals 1 budget point per attribute point. 91-95 cost 5 budget points per attribute point. An attribute granted by a matching trait (Strong, Agile, Quick) can reach 100.</p>
  ```

- [ ] **Step 5: Pass `traits` through `AttributesTable.vue`**

  Add `traits: string[]` to its `defineProps`:

  ```ts
  const props = defineProps<{
    attributes: Record<string, { temp: number; pot: number; bonus: number }>
    traits: string[]
    characterId: string
    statBudget: number
  }>()
  ```

  Add `:traits="traits"` to the `<AssignStatsBudgetModal>` usage in the template (alongside the existing `:attributes="attributes"`):

  ```html
  <AssignStatsBudgetModal
    v-if="showAssignModal"
    :attributes="attributes"
    :traits="traits"
    :stat-budget="statBudget"
    :status="submitPotStatus"
    :error-message="submitPotError"
    @cancel="showAssignModal = false"
    @submit="onSubmitPot"
    @dismiss-error="submitPotStatus = 'idle'"
  />
  ```

- [ ] **Step 6: Pass `traits` from `CharacterDetailView.vue` into `AttributesTable`**

  `CharacterDetailView.vue` already has a `traits` computed (used by `BaseInfoTable`). Add `:traits="traits"` to the existing `<AttributesTable>` tag:

  ```html
  <AttributesTable class="flex-1" :attributes="attributes" :traits="traits" :character-id="character.id" :stat-budget="statBudget" />
  ```

- [ ] **Step 7: Typecheck**

  Run: `cd web && npm run typecheck`
  Expected: no errors (confirms every new/changed prop is threaded with matching types across all three components).

- [ ] **Step 8: Run the full stats-budget spec and confirm everything passes**

  Run: `cd web && npm run test:e2e -- character-stats-budget.spec.ts`
  Expected: all tests PASS, including the three new ones and every pre-existing test in the file (traits defaults to `[]` for all of them, so their 95-ceiling assertions are unaffected).

- [ ] **Step 9: Run the adjacent character specs as a regression check**

  `CharacterDetailView.vue` and `AttributesTable.vue` are shared by other flows on the same page — confirm nothing there broke:

  Run: `cd web && npm run test:e2e -- character-traits.spec.ts character-configuration.spec.ts`
  Expected: all PASS, unchanged from before this task.

- [ ] **Step 10: Commit**

  ```bash
  cd /home/sage/git/timadorus-platform
  git add web/src/lib/attributes.ts \
    web/src/components/character/AssignStatsBudgetModal.vue \
    web/src/components/character/AttributesTable.vue \
    web/src/views/CharacterDetailView.vue \
    web/e2e/character-stats-budget.spec.ts
  git commit -m "feat(web): raise Assign Stats Budget's Pot ceiling to 100 for trait-granted attributes

Co-Authored-By: Claude Sonnet 5 <noreply@anthropic.com>"
  ```

---

## Self-Review Notes

- **Spec coverage:** every bullet of the 2026-09-13 addendum (mapping location/shape, prop plumbing through all three components, `ceilingFor` replacing `POT_CEILING` in both `maxFor` and `onBlur`, explanation-text sentence, and the three test scenarios called for) is covered by a step above.
- **Placeholder scan:** no TBD/TODO; every step carries the literal code to write, not a description of it.
- **Type consistency:** `traits: string[]` is spelled identically in `AttributesTable.vue`'s and `AssignStatsBudgetModal.vue`'s `defineProps`; `TRAIT_ATTRIBUTES` is imported by the exact name it's exported under; `ceilingFor` is defined once and used by both `maxFor` and `onBlur` with the same signature (`(abbr: string) => number`).
