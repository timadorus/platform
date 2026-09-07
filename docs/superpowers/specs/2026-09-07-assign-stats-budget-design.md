# Assign Stats Budget (Pot Editing) Design

## Context

The Character detail page's Stats tab (`AttributesTable.vue`) currently displays each attribute's
Temp/Pot/Bonus read-only ([[character-attributes-design]]). A Timadorus-ruleset Character also
carries `info.stats.statBudget` (a number, seeded from the Campaign's own
`characterCreation.maxStatBudget` — [[campaign-max-stat-budget-design]]), but nothing in the SPA
lets the player spend it. This work adds that: a modal where the player raises one or more
attributes' Pot values, paying for each increase out of `statBudget`, with the engine — not the
SPA — as the sole source of truth for both the cost computation and the resulting values.

This follows the same "engine validates, SPA just asks" pattern already established for `addTrait`
([[character-traits-design]]) and `setMaxStatBudget` ([[campaign-max-stat-budget-design]]): the SPA
sends a target, the engine independently recomputes eligibility from the Character's own current
stored state, applies it or logs a no-op rejection, and the SPA waits for the reload to reflect the
outcome before considering the action finished.

Scope: Timadorus-ruleset Characters only, matching every prior extension of this pattern.

## Key Decisions

1. **The cost formula.** For a single attribute rising from `initial` to `target` (`target >
   initial`; `target == initial` costs 0; `target < initial` is never valid — see Decision 4):
   - The portion of the increase at or below Pot 90 costs **1** statBudget point per Pot point.
   - The portion of the increase above Pot 90 costs **5** statBudget points per Pot point.
   - Formula: `cost = max(0, min(target, 90) - initial) * 1 + max(0, target - max(initial, 90)) * 5`.
   - Example: initial 85 → target 95 costs `(90-85)*1 + (95-90)*5 = 5 + 25 = 30`.
   - A batch's total cost is the sum of this formula across every attribute being changed. This
     exact formula is implemented **twice** — once in the modal (live feedback, Decision 5) and
     once in the engine (authoritative check, Decision 7) — and must stay in sync; the Go unit
     tests and Playwright tests both pin down the same worked examples to catch drift.
   - Display text under the table (Decision 3's fix from the brainstorm): *"Set potential values.
     Pot ≤ 90 equals 1 budget point per attribute point. 91-100 cost 5 budget points per attribute
     point."*

2. **"Initial value" is a per-attribute snapshot, not a hardcoded 50.** For both the modal's
   client-side floor/cost check and the engine's authoritative one, "initial" means that
   attribute's own **current stored Pot value** at the moment of the check — never a hardcoded
   baseline. Today every attribute starts at Pot 50 (`initialAttributeValue`,
   `character_processor.go`), but nothing about this feature assumes that; a future change to
   per-attribute starting values (e.g. a race modifier) needs no changes here. No new field is
   added to `info.stats.attributes[abbr]` — the existing `pot` value already is that snapshot.

3. **Data flow stays `info.stats.*`.** No new top-level shape. `submitPot` only ever rewrites
   `stats.attributes[*].pot` (never `temp` or `bonus` — nothing yet changes Temp, matching
   [[character-attributes-design]]'s existing scope) and `stats.statBudget` (decremented by the
   accepted batch's total cost). Both are written by the engine in the same `SetInfo` call, exactly
   like `tryAddTrait` writes `traitPoints`/`traits` together.

4. **Edit rules (enforced identically in the modal and the engine):**
   - A submitted Pot value can never be **below** that attribute's current stored Pot (no refunds
     or free decreases through this flow).
   - A submitted Pot value can never exceed **100**.
   - The batch's total cost (Decision 1) can never exceed the Character's current stored
     `statBudget`.
   - Any violation of any of these three rules — for any attribute in the batch — rejects the
     **entire batch**, changing nothing (the "Reject the whole batch" decision from the brainstorm,
     matching `tryAddTrait`'s existing all-or-nothing philosophy for a single rejected action).

5. **Modal UI** (new `AssignStatsBudgetModal.vue`, built on the existing `BaseModal.vue`):
   - Opens from a new **"Assign Stats Budget"** button next to `AttributesTable.vue`'s "Attributes"
     header, shown only when `statBudget > 0` (mirrors `BaseInfoTable.vue`'s `traitPoints > 0` gate
     for its own "Add Trait" button).
   - Table: Attribute | Abbr | Pot (editable `<input type="number">`) — Temp and Bonus are omitted,
     matching the request. Seeded once, at modal-open time, from the live `attributes` prop; not
     re-synced from further background reloads while the modal is open (a short-lived draft, like
     `nameDraft`/`traitToAdd` elsewhere — see [[campaign-max-stat-budget-design]]'s later fix for
     why a *persistent* field needs different treatment than a transient one like this).
   - Below the table, its own group showing: current statBudget copy, a live **"Budget remaining"**
     readout (starting value minus the summed cost of every field's current draft value vs. its
     opening value), and the fixed explanation text from Decision 1.
   - Below that, **Cancel** and **Submit** buttons.
   - **Per-field validation on blur**: when a Pot input loses focus, if its new value breaks any
     Decision 4 rule (including — new relative to a single-field check — pushing the *running
     total* over the remaining budget), it resets to its own last valid value, not necessarily the
     original opening value (so a sequence of valid edits followed by one invalid one only reverts
     the invalid one).
   - **Cancel**: closes the modal immediately, discarding the draft — except while a Submit is
     in-flight (Decision 6 below), where it's disabled like Submit.
   - **Submit**: sends the request (Decision 8) and enters a pending state — Cancel, Submit, and
     every Pot input become disabled/read-only, and a "Update requested — refreshing…" line appears
     (matching `ConfigurationPanel.vue`/`BaseInfoTable.vue`'s existing pending copy). The modal does
     **not** close yet.

6. **Confirmation and timeout** (mirrors `BaseInfoTable.vue`'s `tryAddTrait` pending flow exactly):
   a `watch` on the live `attributes` prop checks, on every change, whether every submitted
   abbreviation's Pot now equals its submitted target. Once true, the modal closes automatically. A
   10s timeout (matching the existing `PENDING_TIMEOUT_MS`/`ADD_TRAIT_TIMEOUT_MS` convention)
   guards the case where the batch was rejected server-side (a clean, logged no-op — indistinguishable
   from "still processing" the same way a rejected `addTrait` is): on timeout, status becomes
   `'error'`, an `ErrorBanner` explains the change may not have been applied, and Cancel/Submit
   re-enable so the player can adjust and retry.

7. **Engine-side handling** (`internal/engine/timadorus/character_processor.go`): a new
   `trySubmitPot`, parallel to `tryAddTrait`, dispatched from `handleActionRequested` on
   `{"action":"submitPot","pot":{"<abbr>":<number>,...}}` (Decision 8). Loads the Character
   aggregate, and for every `(abbr, target)` pair in the request:
   - Rejects the whole batch (logs and no-ops, exactly like `tryAddTrait`'s rejections) if `abbr`
     isn't one of the Character's existing `stats.attributes` keys, if `target < current pot`, or
     if `target > 100`.
   - Otherwise accumulates cost via Decision 1's formula, using each attribute's own current stored
     `pot` as `initial`.
   Once every pair passes, if the summed cost exceeds the Character's current `stats.statBudget`,
   rejects the whole batch. Otherwise applies every `pot` update and decrements `statBudget` by the
   total cost, in one `SetInfo` call (mirroring `tryAddTrait`'s single mutate-then-save). A missing
   `stats.attributes` or `stats.statBudget` (non-Timadorus Character, or the engine hasn't seeded it
   yet) is itself a rejection — not an error — for the same reason `tryAddTrait` treats a Character
   with no `traitPoints` field as a clean no-op.

8. **Action payload.** `PUT /characters/{characterId}/action` body:
   ```json
   { "action": "submitPot", "pot": { "ST": 55, "AG": 60, "CO": 50, "QU": 50, "SD": 50, "ME": 50, "RE": 50, "EM": 50, "PR": 50, "IN": 50 } }
   ```
   The SPA always includes **all ten** abbreviations (their current draft value, whether changed or
   not) — matching the original request's "followed by all the attribute abbreviations followed by
   their pot value" — so an unchanged attribute is simply a same-value, zero-cost entry. The
   `characterAction` Go struct (already `{Action, Trait}`) gets a third field, `Pot
   map[string]float64`.

## Data / Component Changes Summary

- `AttributesTable.vue`: new props `characterId: string`, `statBudget: number`; owns the "Assign
  Stats Budget" button, the modal's open/pending/error state, and the `requestAction` call and
  confirmation watch (mirrors `BaseInfoTable.vue` owning `addTrait`'s equivalent state today) —
  keeping `AssignStatsBudgetModal.vue` itself a presentational component (props in, `cancel`/`submit`
  events out, its own local draft/validation state only).
- `CharacterDetailView.vue`: adds a `statBudget` computed (parsed from `character.info`, defaulting
  to `0`, exactly like the existing `traitPoints` computed) and passes both it and `character.id`
  down to `AttributesTable.vue`.
- New `web/src/components/character/AssignStatsBudgetModal.vue`.
- `internal/engine/timadorus/character_processor.go`: extend `characterAction`, add `trySubmitPot`
  and a shared `potCost(initial, target int) int` helper, dispatch it from `handleActionRequested`.

## Testing Strategy

- **Go unit** (`character_processor_test.go`): `potCost` worked examples (85→95 = 30, 50→90 = 40,
  90→95 = 25, no-op 50→50 = 0, a decrease and a see-if-it's-rejected `>100` target); `trySubmitPot`
  accepts a valid batch and correctly decrements `statBudget` while leaving unrelated attributes
  untouched; rejects (no mutation, no error) on: insufficient `statBudget`, a decrease, a target
  over 100, an unknown abbreviation, a non-Timadorus Character, and a Character with no
  `stats.statBudget` seeded yet.
- **Playwright e2e** (new `web/e2e/character-stats-budget.spec.ts`, mirroring
  `character-traits.spec.ts`/`campaign-configuration.spec.ts`'s seed/locator/timing conventions):
  button only shows when `statBudget > 0`; opening the modal shows all 10 attributes' current Pot
  values and the explanation text; a blurred invalid edit (below floor, above 100, or exceeding
  remaining budget) reverts to its last valid value and the live "Budget remaining" readout updates
  correctly across several edits; Cancel closes without sending a request; Submit sends the
  `submitPot` payload with all 10 abbreviations, shows pending state with inputs/buttons disabled,
  and closes once the injected change-feed update reflects the new Pot values; a submit that never
  gets confirmed times out into an error state with Cancel/Submit re-enabled, mirroring
  `campaign-configuration.spec.ts`'s equivalent test.

## Out of Scope

- Any change to Temp or Bonus — this is Pot-only, matching the request.
- Spending statBudget any other way, or ever increasing statBudget itself, through this flow.
- A per-attribute starting value different from today's flat 50 (Decision 2 makes the design
  forward-compatible with that, but seeding it is separate work).
