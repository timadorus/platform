# Real Stat Bonus Formula Design

## Context

`character_processor.go`'s `defaultAttributes()` seeds each of a new Timadorus Character's ten
attributes with `{temp, pot, bonus}`. `bonus` has always been computed by a placeholder,
`attributeBonus(temp int) int` (`floor((temp-50)/10)`), explicitly documented as "standing in for
the real timadorus-engine rules formula, which is not yet specified" — see
[[character-attributes-design]] Decision 4.

That real formula now exists: `internal/engine/timadorus/stat_bonus.go` (added directly to the
working tree, not yet committed) defines `GetStatBonus(stat int) int`, a threshold table (`[]StatBonus{minval, bonus,
spellPoint}`) mapping a stat value to its bonus. This work retires the placeholder and wires
`GetStatBonus` in as the real formula, everywhere `attributeBonus` was.

Nothing in the codebase changes a Character's Temp after creation today — the sole existing site
that ever sets Temp is `defaultAttributes()` itself. Per the user, this stays true for now: Temp
will start changing through **future timadorus-engine functions**, not through any SPA-facing
action shipping in this work. This design's job is narrower than "recompute bonus on every temp
change" sounds: it establishes the one function every future temp-setter must call, and wires the
one call site that exists today (creation) through it — not to invent a temp-changing feature that
doesn't exist yet.

## Key Decisions

1. **`GetStatBonus` replaces `attributeBonus` everywhere, including at creation.**
   `attributeBonus` is deleted. `GetStatBonus(50)` (the baseline creation Temp) returns **`0`** —
   the table was corrected during this design's review specifically to make that true (its "0"
   band originally started at 59, leaving 40-58 including 50 itself in the "-1" band below it; it
   now starts at 41). This means the real formula happens to agree with the placeholder's output
   at the one input that exists today, so **no existing test's expected values change** — the
   value is the same, only where it comes from changes (a real, data-driven table instead of a
   linear placeholder formula).

2. **One choke point for "Temp changed, so Bonus must be recomputed."** A new package-private
   helper,
   ```go
   // setAttributeTemp sets attr's own "temp" key and recomputes "bonus" from it via GetStatBonus
   // in the same call — the one function every timadorus-engine code path that changes a
   // Character's Temp must go through, so Bonus can never drift out of sync with Temp. Pot is
   // left untouched.
   func setAttributeTemp(attr map[string]any, temp int) {
       attr["temp"] = temp
       attr["bonus"] = GetStatBonus(temp)
   }
   ```
   is the only place `"bonus"` is ever written from a Temp value. `defaultAttributes()` becomes the
   first (and today, only) caller: it builds each attribute's `pot` key directly, then calls
   `setAttributeTemp(attr, initialAttributeValue)` to set `temp`/`bonus` together. A future
   temp-changing engine function (out of scope here — see Context) calls the same helper on an
   already-loaded Character's existing attribute map, exactly like `attributeBonusHook`
   (`trait_hooks.go`) already mutates `pot` in place on a loaded `stats` map.

3. **`stat_bonus.go`'s data table is taken as-is (post-fix).** The user corrected two issues
   directly in the file during this design's review: the row-96 value (originally `19`, tied with
   97's `19` and breaking the surrounding +2-per-step pattern; now `17`) and the 41-59 gap above
   (Decision 1). Tests lock in the table's current, corrected behavior — no further changes to the
   `bonuses` table are part of this work.

4. **`GetSpellPointBonus` is untouched and unused.** Out of scope — nothing today computes or
   stores a spell-point value, and wiring it up isn't part of this request (YAGNI).

5. **`stat_bonus.go` gains doc comments, not logic changes.** It currently has zero comments,
   inconsistent with every other file in this package. Adds a short doc comment on `StatBonus`,
   `bonuses`, `GetStatBonus`, and `GetSpellPointBonus` (noting the latter is currently unused) —
   the table's values and `GetStatBonus`/`GetSpellPointBonus`'s logic are byte-for-byte unchanged.
   The file is also `git add`ed for the first time (it exists only as an untracked file today).

## Data / Component Changes Summary

- `internal/engine/timadorus/stat_bonus.go`: doc comments only; newly committed to git.
- `internal/engine/timadorus/character_processor.go`: remove `attributeBonus` (and the now-unused
  `"math"` import); add `setAttributeTemp`; update `defaultAttributes()` to call it; update the
  doc comments that describe the old placeholder.
- Remove `internal/engine/timadorus/attribute_bonus_internal_test.go` (tests a function that no
  longer exists).
- Add `internal/engine/timadorus/stat_bonus_test.go` (external test package — `GetStatBonus` is
  exported, so no white-box test file is needed): table-driven coverage of `GetStatBonus` across
  the corrected table (see Testing Strategy for exact cases).
- Add `internal/engine/timadorus/set_attribute_temp_internal_test.go` (internal — `setAttributeTemp`
  is package-private): confirms it overwrites both `temp` and `bonus` together and leaves `pot`
  untouched.
- `TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStats`
  (`character_processor_test.go`) and `test/e2e/e2e_test.go`'s "creating a Character seeds its
  default stats" `It` are **unchanged** — both already assert `bonus == 0` at creation, which is
  still correct under the real formula (Decision 1).

## Testing Strategy

- **Go unit** (`stat_bonus_test.go`): `GetStatBonus` table-driven over the corrected table,
  verified by actually running the current table's logic (not hand-computed): `150→25` (no upper
  bound), `100→25` (top row), `99→23`, `97→19` and `96→17` (no longer tied, confirming the
  post-fix +2 step), `87→9` and `86→8` (86 falls through to 84's row, not its own — the "between
  two defined thresholds" case), `60→1` and `59→0` (top edge of the zero band), `50→0` (the actual
  creation-time input — the case this whole design exists to get right), `41→0` and `40→-1` (the
  zero band's own lower edge, the exact gap that was fixed), `1→-25` (bottom row), `0→0` and
  `-5→0` (below the table entirely, the function's own fallback).
- **Go unit** (`set_attribute_temp_internal_test.go`): starting from an attribute map with a stale
  `bonus`, calling `setAttributeTemp` with a new `temp` overwrites both `temp` and `bonus`
  (recomputed via `GetStatBonus`) and leaves `pot` untouched.
- **Go integration** (existing `TestCharacterProcessor_CharacterCreated_MatchingRuleset_SeedsStats`,
  unchanged): a real CharacterCreated event still seeds all ten attributes at `temp:50`, `pot:50`,
  `bonus:0` — now produced by `GetStatBonus` instead of the deleted placeholder.
- **Real-cluster e2e** (existing `test/e2e/e2e_test.go` `It`, unchanged, not run in this sandbox):
  same assertion against a real running cluster.

## Out of Scope

- Any actual mechanism that changes a Character's Temp after creation — per the user, that arrives
  through future timadorus-engine functions not yet specified. This design only makes sure that,
  whenever it arrives, there's already one obvious, correct function for it to call.
- `GetSpellPointBonus` and anything spell-point-related.
- Any further changes to `stat_bonus.go`'s `bonuses` table data beyond the two corrections already
  made during this design's review (Decision 3).
